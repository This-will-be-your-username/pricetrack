package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Result is what we managed to extract from a product page.
type Result struct {
	Name     string
	Image    string
	Price    float64
	Currency string
	Caveat   string  // condition attached to Price (e.g. "Member price"); empty if none
	StdPrice float64 // cheapest unconditional price when Price carries a caveat
}

type clientKey struct{}

// Browser identity headers forwarded to merchants so requests look like the
// visitor's own browser rather than a bot.
var clientHeaders = []string{"User-Agent", "Accept-Language", "Sec-CH-UA", "Sec-CH-UA-Mobile", "Sec-CH-UA-Platform"}

// WithClient records the visitor's browser identity on the context.
func WithClient(ctx context.Context, r *http.Request) context.Context {
	h := http.Header{}
	for _, k := range clientHeaders {
		if v := r.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	return context.WithValue(ctx, clientKey{}, h)
}

// applyClient sets the visitor's browser headers on req, falling back to a
// generic browser identity when none was captured.
func applyClient(ctx context.Context, req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if h, ok := ctx.Value(clientKey{}).(http.Header); ok {
		for k, v := range h {
			req.Header[k] = v
		}
	}
}

// Fetcher downloads product pages and extracts prices.
type Fetcher struct{ client *http.Client }

func NewFetcher(allowPrivate bool) *Fetcher {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		// Guard against SSRF: refuse loopback / private / link-local targets.
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
				ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
				return errors.New("refusing to fetch private address (set ALLOW_PRIVATE=1 to allow)")
			}
			return nil
		}
	}
	tr := &http.Transport{
		DialContext:         d.DialContext,
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        10,
	}
	return &Fetcher{client: &http.Client{
		Transport: tr,
		Timeout:   25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

// Fetch retrieves url and parses it. On failure to find a price it still
// returns any partial data (name, image) alongside the error.
func (f *Fetcher) Fetch(ctx context.Context, u string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	applyClient(ctx, req)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("merchant returned HTTP %d (it may block automated requests)", resp.StatusCode)
	}
	return ParsePage(string(body))
}

var (
	reLD     = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	reMeta   = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	reAttr   = regexp.MustCompile(`([\w:.-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reTitle  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reAmzOff = regexp.MustCompile(`(?is)class="a-offscreen"[^>]*>\s*([^<\s][^<]*)<`)
	reAmzTtl = regexp.MustCompile(`(?is)id="productTitle"[^>]*>\s*(.*?)\s*</span>`)
	reAmzImg = regexp.MustCompile(`(?is)id="landingImage"[^>]*\ssrc="([^"]+)"`)
	reNum    = regexp.MustCompile(`[0-9][0-9.,\s]*`)
)

// ParsePage extracts product data from HTML using JSON-LD, OpenGraph/product
// meta tags and a few merchant-specific fallbacks.
func ParsePage(page string) (*Result, error) {
	r := &Result{}
	var cands []priceCand

	// 1. schema.org JSON-LD
	for _, m := range reLD.FindAllStringSubmatch(page, -1) {
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &v); err != nil {
			continue
		}
		var prods []map[string]any
		findProducts(v, &prods)
		for _, p := range prods {
			if r.Name == "" {
				r.Name = str(p["name"])
			}
			if r.Image == "" {
				r.Image = imageOf(p["image"])
			}
			collectOffers(p["offers"], "", "", &cands)
		}
	}

	// 2. meta tags
	meta := map[string]string{}
	for _, tag := range reMeta.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, a := range reAttr.FindAllStringSubmatch(tag, -1) {
			v := a[2]
			if v == "" {
				v = a[3]
			}
			attrs[strings.ToLower(a[1])] = v
		}
		key := attrs["property"]
		if key == "" {
			key = attrs["name"]
		}
		if key == "" {
			key = attrs["itemprop"]
		}
		if key != "" {
			if _, seen := meta[strings.ToLower(key)]; !seen {
				meta[strings.ToLower(key)] = attrs["content"]
			}
		}
	}
	if r.Name == "" {
		r.Name = meta["og:title"]
	}
	if r.Image == "" {
		r.Image = meta["og:image"]
	}
	if !hasStandard(cands) {
		for _, k := range []string{"product:price:amount", "og:price:amount", "twitter:data1", "price"} {
			if v, ok := meta[k]; ok {
				if p, ok := parsePrice(v); ok {
					cur := firstNonEmpty(meta["product:price:currency"], meta["og:price:currency"], meta["pricecurrency"], currencyOf(v))
					cands = append(cands, priceCand{price: p, cur: cur})
					break
				}
			}
		}
	}

	// 3. Amazon-style markup
	if r.Name == "" {
		if m := reAmzTtl.FindStringSubmatch(page); m != nil {
			r.Name = m[1]
		}
	}
	if r.Image == "" {
		if m := reAmzImg.FindStringSubmatch(page); m != nil {
			r.Image = m[1]
		}
	}
	if !hasStandard(cands) {
		if m := reAmzOff.FindStringSubmatch(page); m != nil {
			if p, ok := parsePrice(m[1]); ok {
				cands = append(cands, priceCand{price: p, cur: currencyOf(m[1])})
			}
		}
	}

	// 4. Conditional prices mentioned in the page body (member / subscription).
	cands = append(cands, pageConditionalPrices(page, cands)...)
	pickPrice(r, cands)

	if r.Name == "" {
		if m := reTitle.FindStringSubmatch(page); m != nil {
			r.Name = m[1]
		}
	}
	r.Name = strings.Join(strings.Fields(html.UnescapeString(r.Name)), " ")
	if len(r.Name) > 200 {
		r.Name = r.Name[:200]
	}
	r.Image = html.UnescapeString(strings.TrimSpace(r.Image))
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	if r.Price <= 0 {
		return r, errors.New("couldn't find a price on that page (the merchant may block automated requests)")
	}
	return r, nil
}

func findProducts(v any, out *[]map[string]any) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			findProducts(e, out)
		}
	case map[string]any:
		if hasType(t["@type"], "Product") {
			*out = append(*out, t)
		}
		for _, k := range []string{"@graph", "mainEntity", "mainEntityOfPage", "itemListElement", "item"} {
			if c, ok := t[k]; ok {
				findProducts(c, out)
			}
		}
	}
}

func hasType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return strings.EqualFold(t, want)
	case []any:
		for _, e := range t {
			if hasType(e, want) {
				return true
			}
		}
	}
	return false
}

// priceCand is one price seen on a page. caveat is empty for the ordinary
// price anyone can pay, otherwise a short label for the condition attached.
type priceCand struct {
	price  float64
	cur    string
	caveat string
}

func hasStandard(c []priceCand) bool {
	for _, p := range c {
		if p.caveat == "" {
			return true
		}
	}
	return false
}

// Labels for conditional prices.
const (
	cvMember   = "Member price"
	cvRepeat   = "With repeat delivery"
	cvBusiness = "Business customers only"
	cvStudent  = "Student price"
)

// caveatFrom classifies descriptive text (offer names, customer types, ...).
func caveatFrom(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "member") || strings.Contains(t, "loyalty") || strings.Contains(t, "club price"):
		return cvMember
	case strings.Contains(t, "subscri") || strings.Contains(t, "repeat") || strings.Contains(t, "recurring") ||
		strings.Contains(t, "autoship") || strings.Contains(t, "auto-ship") || strings.Contains(t, "auto-delivery") ||
		strings.Contains(t, "auto delivery") || strings.Contains(t, "autodelivery") || strings.Contains(t, "replenish"):
		return cvRepeat
	case strings.Contains(t, "business") || strings.Contains(t, "trade customer"):
		return cvBusiness
	case strings.Contains(t, "student") || strings.Contains(t, "educat"):
		return cvStudent
	}
	return ""
}

// textOf joins the string (or string-list) values of the given keys.
func textOf(m map[string]any, keys ...string) string {
	var parts []string
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			parts = append(parts, v)
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					parts = append(parts, s)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

// collectOffers walks schema.org Offer / AggregateOffer / PriceSpecification
// structures, gathering every selling price with any condition attached.
func collectOffers(v any, inheritCur, inheritCaveat string, out *[]priceCand) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			collectOffers(e, inheritCur, inheritCaveat, out)
		}
	case map[string]any:
		pt := strings.ToLower(str(t["priceType"]))
		if strings.Contains(pt, "list") || strings.Contains(pt, "srp") || strings.Contains(pt, "strikethrough") ||
			strings.Contains(pt, "minimumadvertised") || strings.Contains(pt, "invoice") {
			return // reference prices, not what you pay
		}
		cur := firstNonEmpty(str(t["priceCurrency"]), inheritCur)
		cv := inheritCaveat
		if c := caveatFrom(textOf(t, "name", "description", "category", "eligibleCustomerType", "priceType")); c != "" {
			cv = c
		}
		for _, k := range []string{"price", "lowPrice"} {
			if p, ok := parsePrice(t[k]); ok {
				*out = append(*out, priceCand{price: p, cur: cur, caveat: cv})
				break
			}
		}
		for _, k := range []string{"priceSpecification", "offers"} {
			if c, ok := t[k]; ok {
				collectOffers(c, cur, cv, out)
			}
		}
	}
}

const priceTail = `(?:<[^>]*>|[^<]){0,160}?([$€£]\s?\d[\d.,]*)`

var condPatterns = []struct {
	re     *regexp.Regexp
	caveat string
}{
	{regexp.MustCompile(`(?is)subscribe\s*(?:&amp;|&|and)\s*save` + priceTail), cvRepeat},
	{regexp.MustCompile(`(?is)(?:repeat|recurring|auto[- ]?(?:ship|delivery|replenish\w*))\s*(?:delivery|order|price)` + priceTail), cvRepeat},
	{regexp.MustCompile(`(?is)\bmembers?(?:hip)?(?:\s+only)?\s*(?:price|pricing|deal)` + priceTail), cvMember},
	{regexp.MustCompile(`(?is)([$€£]\s?\d[\d.,]*)(?:<[^>]*>|[^<]){0,30}\b(?:members?\s*(?:price|only)|for members|with membership)`), cvMember},
}

// pageConditionalPrices looks for member / subscription prices in page text. Page
// text is noisy, so a hit is only believed when it undercuts a known standard
// price by a plausible amount.
func pageConditionalPrices(page string, have []priceCand) []priceCand {
	std, cur := 0.0, ""
	for _, c := range have {
		if c.caveat == "" && (std == 0 || c.price < std) {
			std, cur = c.price, c.cur
		}
	}
	if std == 0 {
		return nil
	}
	var out []priceCand
	for _, pat := range condPatterns {
		for _, m := range pat.re.FindAllStringSubmatch(page, 6) {
			if p, ok := parsePrice(m[1]); ok && p < std && p >= 0.3*std {
				out = append(out, priceCand{price: p, cur: firstNonEmpty(currencyOf(m[1]), cur), caveat: pat.caveat})
			}
		}
	}
	return out
}

// pickPrice sets r.Price to the cheapest candidate, including conditional ones,
// recording the caveat and the cheapest unconditional price.
func pickPrice(r *Result, cands []priceCand) {
	cur := ""
	for _, c := range cands { // the first standard price decides the currency
		if c.caveat == "" && c.cur != "" {
			cur = c.cur
			break
		}
	}
	if cur == "" {
		for _, c := range cands {
			if c.cur != "" {
				cur = c.cur
				break
			}
		}
	}
	var best, std *priceCand
	for i := range cands {
		c := &cands[i]
		if c.price <= 0 || (c.cur != "" && cur != "" && c.cur != cur) {
			continue
		}
		if best == nil || c.price < best.price {
			best = c
		}
		if c.caveat == "" && (std == nil || c.price < std.price) {
			std = c
		}
	}
	if best == nil {
		return
	}
	r.Price, r.Currency, r.Caveat = best.price, firstNonEmpty(cur, best.cur), best.caveat
	if best.caveat != "" && std != nil {
		r.StdPrice = std.price
	}
}

func imageOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			return imageOf(t[0])
		}
	case map[string]any:
		return str(t["url"])
	}
	return ""
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// parsePrice accepts numbers or strings like "$1,299.99", "1.299,99 €", "19.9".
func parsePrice(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, t > 0
	case string:
		m := reNum.FindString(strings.TrimSpace(t))
		m = strings.ReplaceAll(strings.TrimSpace(m), " ", "")
		m = strings.TrimRight(m, ".,")
		if m == "" {
			return 0, false
		}
		dot, comma := strings.LastIndex(m, "."), strings.LastIndex(m, ",")
		switch {
		case dot >= 0 && comma >= 0:
			if comma > dot { // 1.299,99
				m = strings.ReplaceAll(m, ".", "")
				m = strings.Replace(m, ",", ".", 1)
			} else { // 1,299.99
				m = strings.ReplaceAll(m, ",", "")
			}
		case comma >= 0:
			if len(m)-comma-1 == 2 && strings.Count(m, ",") == 1 { // 19,99
				m = strings.Replace(m, ",", ".", 1)
			} else { // 1,299
				m = strings.ReplaceAll(m, ",", "")
			}
		}
		f, err := strconv.ParseFloat(m, 64)
		return f, err == nil && f > 0
	}
	return 0, false
}

func currencyOf(s string) string {
	switch {
	case strings.Contains(s, "€"):
		return "EUR"
	case strings.Contains(s, "£"):
		return "GBP"
	case strings.Contains(s, "¥"):
		return "JPY"
	case strings.Contains(s, "CA$"), strings.Contains(s, "C$"):
		return "CAD"
	}
	// A bare "$" is ambiguous (USD/AUD/CAD/NZD...); the caller falls back to the region currency.
	return ""
}

var merchants = map[string]string{
	"amazon": "Amazon", "walmart": "Walmart", "target": "Target", "bestbuy": "Best Buy",
	"ebay": "eBay", "etsy": "Etsy", "newegg": "Newegg", "costco": "Costco",
	"homedepot": "Home Depot", "lowes": "Lowe's", "aliexpress": "AliExpress",
	"macys": "Macy's", "nike": "Nike", "apple": "Apple", "bhphotovideo": "B&H",
	"wayfair": "Wayfair", "ikea": "IKEA", "zara": "Zara", "adidas": "Adidas",
	"nordstrom": "Nordstrom", "kohls": "Kohl's", "sephora": "Sephora", "gamestop": "GameStop",
}

// MerchantOf derives a friendly merchant name from a URL.
func MerchantOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	if n := retailerName(u); n != "" {
		return n
	}
	if net.ParseIP(u.Hostname()) != nil || !strings.Contains(u.Hostname(), ".") {
		return u.Hostname()
	}
	parts := strings.Split(strings.ToLower(u.Hostname()), ".")
	// find the registrable label: skip tld (and co.uk style second-level)
	idx := len(parts) - 2
	if idx > 0 && len(parts[len(parts)-1]) == 2 && len(parts[idx]) <= 3 {
		idx--
	}
	if idx < 0 {
		idx = 0
	}
	label := parts[idx]
	if n, ok := merchants[label]; ok {
		return n
	}
	if label == "" {
		return ""
	}
	return strings.ToUpper(label[:1]) + label[1:]
}
