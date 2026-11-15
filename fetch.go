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
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
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
			if r.Price == 0 {
				if pr, cur, ok := offerPrice(p["offers"]); ok {
					r.Price, r.Currency = pr, cur
				}
			}
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
	if r.Price == 0 {
		for _, k := range []string{"product:price:amount", "og:price:amount", "twitter:data1", "price"} {
			if v, ok := meta[k]; ok {
				if p, ok := parsePrice(v); ok {
					r.Price = p
					r.Currency = firstNonEmpty(meta["product:price:currency"], meta["og:price:currency"], meta["pricecurrency"], currencyOf(v))
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
	if r.Price == 0 {
		if m := reAmzOff.FindStringSubmatch(page); m != nil {
			if p, ok := parsePrice(m[1]); ok {
				r.Price, r.Currency = p, currencyOf(m[1])
			}
		}
	}

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

func offerPrice(v any) (float64, string, bool) {
	switch t := v.(type) {
	case []any:
		best, cur, found := 0.0, "", false
		for _, e := range t {
			if p, c, ok := offerPrice(e); ok && (!found || p < best) {
				best, cur, found = p, c, true
			}
		}
		return best, cur, found
	case map[string]any:
		cur := str(t["priceCurrency"])
		for _, k := range []string{"price", "lowPrice"} {
			if p, ok := parsePrice(t[k]); ok {
				return p, cur, true
			}
		}
		for _, k := range []string{"priceSpecification", "offers"} {
			if c, ok := t[k]; ok {
				if p, c2, ok := offerPrice(c); ok {
					return p, firstNonEmpty(cur, c2), true
				}
			}
		}
	}
	return 0, "", false
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
