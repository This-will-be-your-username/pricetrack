package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SearchResult is one web hit.
type SearchResult struct{ Title, URL string }

// Searcher finds web pages for a query, biased to a region.
type Searcher interface {
	Search(ctx context.Context, query, regionCode string) ([]SearchResult, error)
}

// NewSearcher returns Brave Search when an API key is configured, otherwise a
// keyless DuckDuckGo HTML scraper.
func NewSearcher(f *Fetcher, braveKey string) Searcher {
	if braveKey != "" {
		return NewThrottledSearcher(&braveSearch{c: f.client, key: braveKey}, 1200*time.Millisecond)
	}
	return NewThrottledSearcher(&ddgSearch{c: f.client}, 6*time.Second)
}

// ddg "kl" region codes; Brave uses the lowercase country code.
var ddgRegion = map[string]string{
	"AU": "au-en", "US": "us-en", "CA": "ca-en", "GB": "uk-en", "NZ": "nz-en", "DE": "de-de",
	"FR": "fr-fr", "ES": "es-es", "IT": "it-it", "NL": "nl-nl", "IN": "in-en", "SG": "sg-en", "JP": "jp-jp",
}

func get(ctx context.Context, c *http.Client, u string, hdr map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	applyClient(ctx, req)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusAccepted, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			return b, fmt.Errorf("search provider returned HTTP %d: %w", resp.StatusCode, errRateLimited)
		}
		return b, fmt.Errorf("search provider returned HTTP %d", resp.StatusCode)
	}
	return b, nil
}

// ---- DuckDuckGo (no key) ----

type ddgSearch struct{ c *http.Client }

var reDDGLink = regexp.MustCompile(`(?is)<a\s[^>]*class="[^"]*result__a[^"]*"[^>]*>(.*?)</a>`)
var reTag = regexp.MustCompile(`<[^>]+>`)

func (d *ddgSearch) Search(ctx context.Context, q, region string) ([]SearchResult, error) {
	v := url.Values{"q": {q}}
	if kl := ddgRegion[region]; kl != "" {
		v.Set("kl", kl)
	}
	b, err := get(ctx, d.c, "https://html.duckduckgo.com/html/?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out []SearchResult
	for _, m := range reDDGLink.FindAllStringSubmatch(string(b), -1) {
		var href string
		for _, a := range reAttr.FindAllStringSubmatch(m[0], -1) {
			if strings.EqualFold(a[1], "href") {
				href = html.UnescapeString(firstNonEmpty(a[2], a[3]))
			}
		}
		if href == "" {
			continue
		}
		if strings.HasPrefix(href, "//") {
			href = "https:" + href
		}
		if u, err := url.Parse(href); err == nil && strings.HasSuffix(u.Hostname(), "duckduckgo.com") {
			if t := u.Query().Get("uddg"); t != "" { // unwrap redirect
				href = t
			} else {
				continue // ad / internal link
			}
		}
		out = append(out, SearchResult{Title: html.UnescapeString(reTag.ReplaceAllString(m[1], "")), URL: href})
	}
	if len(out) == 0 {
		if strings.Contains(string(b), "anomaly") || strings.Contains(strings.ToLower(string(b)), "captcha") {
			return nil, fmt.Errorf("search engine challenge page: %w", errRateLimited)
		}
		return nil, errors.New("the search engine returned no results")
	}
	return out, nil
}

// ---- Brave Search API ----

type braveSearch struct {
	c   *http.Client
	key string
}

func (s *braveSearch) Search(ctx context.Context, q, region string) ([]SearchResult, error) {
	v := url.Values{"q": {q}, "count": {"20"}}
	if region != "" && region != AnyRegion {
		v.Set("country", strings.ToLower(region))
	}
	b, err := get(ctx, s.c, "https://api.search.brave.com/res/v1/web/search?"+v.Encode(),
		map[string]string{"X-Subscription-Token": s.key, "Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Web struct {
			Results []struct{ Title, URL string } `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(resp.Web.Results))
	for _, r := range resp.Web.Results {
		out = append(out, SearchResult{r.Title, r.URL})
	}
	return out, nil
}

// ---- retailer discovery ----

// Offer is a retailer page confirmed to show a price.
type Offer struct {
	Retailer string
	Host     string
	Title    string
	URL      string
	Price    float64
	Currency string
	Caveat   string
	StdPrice float64
	Lookup   bool   // a direct search link for manual lookup, not a found product
	Blocked  bool   // relevant retailer whose price couldn't be read; needs a manual check
	Reason   string // why it couldn't be read
	InRegion bool   // a known retailer for the user's region
}

var skipHosts = []string{
	"google.", "bing.com", "youtube.com", "reddit.com", "facebook.com", "instagram.com", "twitter.com",
	"x.com", "pinterest.", "wikipedia.org", "tiktok.com", "quora.com", "medium.com", "tripadvisor.",
	"choice.com.au", "which.co.uk", "cnet.com", "pcmag.com", "techradar.com", "rtings.com", "whirlpool.net.au",
}

// otherRegionOnly reports whether u is a known retailer, but not for rg.
func otherRegionOnly(u *url.URL, rg Region) bool {
	if rg.Allows(u) {
		return false
	}
	return retailerName(u) != ""
}

// targetRetailers is how many retailers a scan tries to list. Retailers whose
// price we could read come first; the rest of the 15 are filled with direct
// "search this retailer" links for manual lookup (see lookupOffers).
const targetRetailers = 15

// maxSearchesPerScan bounds how many search-engine requests one scan may make.
const maxSearchesPerScan = 4

type cand struct {
	Offer
	rank int
}

func (c *cand) kept() bool { return c.Price > 0 || c.Blocked }

// FindOffers searches the web for retailers selling query, keeps those relevant
// to rg, and verifies each by fetching the page and extracting a price.
//
// Searching is deliberately frugal (a handful of queries per scan, all routed
// through the throttled searcher). If the search engine rate-limits us, the
// offers found so far are still returned together with the error.
func (a *App) FindOffers(ctx context.Context, query string, rg Region) ([]Offer, error) {
	where := ""
	if rg.Code != AnyRegion && rg.Name != "" {
		where = " " + rg.Name
	}
	seen := map[string]bool{} // hosts already considered
	var all []*cand
	var searchErr error
	searched, budget := false, maxSearchesPerScan

	count := func() int {
		n := 0
		for _, c := range all {
			if c.kept() {
				n++
			}
		}
		return n
	}

	// collect runs one search and returns unseen, in-region candidates.
	collect := func(q string) []*cand {
		if budget <= 0 || searchErr != nil {
			return nil
		}
		budget--
		rs, err := a.search.Search(ctx, q, rg.Code)
		if err != nil {
			searchErr = err
			return nil
		}
		searched = true

		var out []*cand
		tld := strings.ToLower(rg.Code)
		for _, r := range rs {
			u, err := url.Parse(r.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
				continue
			}
			host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
			skip := false
			for _, s := range skipHosts {
				if strings.Contains(host, s) {
					skip = true
				}
			}
			if skip || seen[host] {
				continue
			}
			if rg.Code != AnyRegion && otherRegionOnly(u, rg) {
				continue // e.g. don't offer US retailers to AU users
			}
			seen[host] = true
			c := &cand{Offer: Offer{Retailer: MerchantOf(r.URL), Host: host, Title: r.Title, URL: r.URL}}
			c.InRegion = rg.Code != AnyRegion && rg.Allows(u)
			switch {
			case c.InRegion:
				c.rank = 0
			case tld != "" && (strings.HasSuffix(host, "."+tld) || strings.Contains(host, ".com."+tld) || strings.Contains(host, ".co."+tld)):
				c.rank = 1
			default:
				c.rank = 2
			}
			out = append(out, c)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].rank < out[j].rank })
		return out
	}

	// verify fetches each candidate concurrently, keeping pages with a readable price.
	verify := func(cs []*cand) {
		if len(cs) > 25 {
			cs = cs[:25]
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, 6)
		for _, c := range cs {
			wg.Add(1)
			go func(c *cand) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				res, ferr := a.fetch.Fetch(fctx, c.URL)
				if res == nil || res.Price <= 0 {
					// Couldn't read a price (often anti-bot blocking). Keep local-looking, on-topic
					// retailers so the user can check them by hand.
					if c.rank <= 1 && relevant(query, c.Title+" "+c.URL) {
						c.Blocked = true
						if ferr != nil {
							c.Reason = ferr.Error()
						} else {
							c.Reason = "no price found on the page"
						}
					}
					return
				}
				if !relevant(query, res.Name+" "+c.Title+" "+c.URL) {
					return
				}
				c.Price, c.Currency, c.Caveat, c.StdPrice = res.Price, res.Currency, res.Caveat, res.StdPrice
				if c.Currency == "" {
					c.Currency = rg.Currency
				}
				if c.Currency == "" {
					c.Currency = "USD"
				}
				if res.Name != "" {
					c.Title = res.Name
				}
			}(c)
		}
		wg.Wait()
		all = append(all, cs...)
	}

	// Round 1: two phrasings of the same question.
	verify(collect(query + " buy online" + where))
	verify(collect(query + " price" + where))

	// Round 2: one combined query per handful of the region's known retailers we haven't met
	// ("site:a.com OR site:b.com ...") — far fewer requests than one search per retailer.
	if rg.Code != AnyRegion && count() < targetRetailers {
		var doms []string
		for _, rt := range rg.Retailers {
			met := false
			for _, c := range all {
				if u, err := url.Parse(c.URL); err == nil && rt.matches(u) {
					met = true
					break
				}
			}
			if !met {
				dom, _, _ := strings.Cut(rt.Domains[0], "/")
				doms = append(doms, "site:"+dom)
			}
		}
		for len(doms) > 0 && budget > 0 && count() < targetRetailers {
			n := min(6, len(doms))
			verify(collect(query + " (" + strings.Join(doms[:n], " OR ") + ")"))
			doms = doms[n:]
		}
	}

	if !searched && searchErr != nil {
		return nil, searchErr
	}

	var offers, blocked []Offer
	for _, c := range all {
		switch {
		case c.Price > 0:
			offers = append(offers, c.Offer)
		case c.Blocked && len(blocked) < targetRetailers:
			blocked = append(blocked, c.Offer)
		}
	}
	offers = dropOutliers(offers)
	sort.SliceStable(offers, func(i, j int) bool {
		if offers[i].Currency == offers[j].Currency {
			return offers[i].Price < offers[j].Price
		}
		return offers[i].InRegion && !offers[j].InRegion
	})
	return append(offers, blocked...), searchErr
}

// lookupOffers returns direct "search this retailer" links for the region's
// best-known retailers that aren't in have yet, so a product always lists the
// obvious places to check by hand — with no search engine involved. It adds
// links until have plus the new ones reach want.
func lookupOffers(query string, rg Region, have []string, dismissed map[string]bool, want int) []Offer {
	if rg.Code == AnyRegion || len(rg.Retailers) == 0 {
		return nil
	}
	var urls []*url.URL
	for _, h := range have {
		if u, err := url.Parse(h); err == nil {
			urls = append(urls, u)
		}
	}
	n := len(have)
	var out []Offer
	for _, rt := range rg.Retailers {
		if n >= want {
			break
		}
		met := false
		for _, u := range urls {
			if rt.matches(u) {
				met = true
				break
			}
		}
		link := rt.SearchURL(query)
		if met || dismissed[link] {
			continue
		}
		dom, _, _ := strings.Cut(rt.Domains[0], "/")
		out = append(out, Offer{Retailer: rt.Name, Host: dom, URL: link, Title: query, Lookup: true, InRegion: true,
			Reason: "Not checked automatically — open the page, find the product, and enter its price with the pencil."})
		n++
	}
	return out
}

var reNonAlnum = regexp.MustCompile("[^a-z0-9]+")

// relevant checks that a page looks like it is about the product searched for:
// every model-number-like token (contains a digit) must appear, and most of the
// remaining words must too. This keeps accessories and unrelated products out.
func relevant(query, text string) bool {
	hay := strings.ToLower(text)
	flat := reNonAlnum.ReplaceAllString(hay, "")
	words := " " + reNonAlnum.ReplaceAllString(hay, " ") + " "
	var plain, plainHit int
	for _, tok := range strings.Fields(reNonAlnum.ReplaceAllString(strings.ToLower(query), " ")) {
		hasDigit := strings.ContainsAny(tok, "0123456789")
		switch {
		case hasDigit:
			if !strings.Contains(flat, tok) {
				return false
			}
		case len(tok) >= 3 && !stopWords[tok]:
			plain++
			if strings.Contains(words, " "+tok) {
				plainHit++
			}
		}
	}
	return plain == 0 || plainHit*10 >= plain*6
}

var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "buy": true, "online": true, "new": true}

// dropOutliers removes offers priced far below the median (typically cases,
// spare parts or accessories that slipped through).
func dropOutliers(offers []Offer) []Offer {
	byCur := map[string][]float64{}
	for _, o := range offers {
		byCur[o.Currency] = append(byCur[o.Currency], o.Price)
	}
	med := map[string]float64{}
	for c, ps := range byCur {
		if len(ps) >= 3 {
			sort.Float64s(ps)
			med[c] = ps[len(ps)/2]
		}
	}
	out := offers[:0:0]
	for _, o := range offers {
		if m, ok := med[o.Currency]; ok && o.Price < 0.4*m {
			continue
		}
		out = append(out, o)
	}
	return out
}
