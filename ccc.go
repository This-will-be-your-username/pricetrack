package main

import (
	"net/url"
	"regexp"
	"strings"
)

// CamelCamelCamel tracks Amazon price history. Its pages are behind a
// bot-challenge, so the server never fetches them: instead the product page
// embeds their chart image (loaded by the visitor's own browser) and links out.

// cccRegion maps an Amazon domain to CamelCamelCamel's region code.
var cccRegion = map[string]string{
	"amazon.com": "us", "amazon.com.au": "au", "amazon.ca": "ca", "amazon.co.uk": "uk",
	"amazon.de": "de", "amazon.fr": "fr", "amazon.it": "it", "amazon.es": "es", "amazon.co.jp": "jp",
}

// cccRegionByCountry maps our region codes to CamelCamelCamel regions.
var cccRegionByCountry = map[string]string{
	"US": "us", "AU": "au", "CA": "ca", "GB": "uk", "DE": "de", "FR": "fr", "IT": "it", "ES": "es", "JP": "jp",
}

var reASIN = regexp.MustCompile(`/(?:dp|gp/product|gp/aw/d|product)/([A-Z0-9]{10})(?:[/?]|$)`)

// CCC is the CamelCamelCamel context for an item.
type CCC struct {
	ASIN     string
	ChartURL string
	PageURL  string
}

func cccSite(code string) string {
	if code == "us" {
		return "camelcamelcamel.com"
	}
	return code + ".camelcamelcamel.com"
}

// amazonASIN extracts the Amazon domain's CCC region and the product's ASIN from a listing URL.
func amazonASIN(raw string) (region, asin string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	region, known := cccRegion[host]
	if !known {
		return "", "", false
	}
	m := reASIN.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", false
	}
	return region, m[1], true
}

// cccFor finds an Amazon listing with an ASIN, preferring the user's own region.
func cccFor(urls []string, userRegion string) *CCC {
	want := cccRegionByCountry[userRegion]
	var fallback *CCC
	for _, raw := range urls {
		region, asin, ok := amazonASIN(raw)
		if !ok {
			continue
		}
		c := &CCC{
			ASIN:     asin,
			ChartURL: "https://charts.camelcamelcamel.com/" + region + "/" + asin + "/amazon.png?zero=0&w=600&h=300&desired=false&legend=1&ilt=1&tp=all&fo=0&lang=en",
			PageURL:  "https://" + cccSite(region) + "/product/" + asin,
		}
		if region == want {
			return c
		}
		if fallback == nil {
			fallback = c
		}
	}
	return fallback
}

// cccSearchURL is a CamelCamelCamel search link for the user's region, or "".
func cccSearchURL(userRegion, query string) string {
	region, ok := cccRegionByCountry[userRegion]
	if !ok {
		return ""
	}
	return "https://" + cccSite(region) + "/search?sq=" + url.QueryEscape(query)
}
