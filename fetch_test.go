package main

import (
	"net/url"
	"testing"
)

func TestParsePage(t *testing.T) {
	cases := []struct {
		name, html string
		price      float64
		cur        string
		title      string
	}{
		{"jsonld", `<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Widget &amp; Co","image":["https://x/i.png"],"offers":{"@type":"Offer","price":"1,299.99","priceCurrency":"USD"}}</script>`, 1299.99, "USD", "Widget & Co"},
		{"jsonld graph", `<script type="application/ld+json">{"@graph":[{"@type":"WebPage"},{"@type":["Product"],"name":"G","offers":[{"price":20,"priceCurrency":"EUR"},{"price":15.5,"priceCurrency":"EUR"}]}]}</script>`, 15.5, "EUR", "G"},
		{"meta", `<meta property="og:title" content="Meta Item"><meta property="product:price:amount" content="19,99"><meta property="product:price:currency" content="GBP">`, 19.99, "GBP", "Meta Item"},
		{"amazon", `<span id="productTitle"> Echo Dot </span><span class="a-offscreen">$49.99</span>`, 49.99, "", "Echo Dot"},
	}
	for _, c := range cases {
		r, err := ParsePage(c.html)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if r.Price != c.price || r.Currency != c.cur || r.Name != c.title {
			t.Errorf("%s: got %+v", c.name, r)
		}
	}
	if _, err := ParsePage("<title>Nothing</title>"); err == nil {
		t.Error("expected error when no price present")
	}
}

func TestParsePrice(t *testing.T) {
	for in, want := range map[string]float64{"$1,299.99": 1299.99, "1.299,99 €": 1299.99, "19,99": 19.99, "1,299": 1299, "12": 12} {
		if got, ok := parsePrice(in); !ok || got != want {
			t.Errorf("%q: got %v %v, want %v", in, got, ok, want)
		}
	}
}

func TestMerchantOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.amazon.com/dp/B0": "Amazon", "https://www.bestbuy.com/x": "Best Buy",
		"https://www.amazon.co.uk/dp/x": "Amazon UK", "https://www.jbhifi.com.au/p": "JB Hi-Fi", "https://shop.example.org/p": "Example",
	} {
		if got := MerchantOf(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestRegionAllows(t *testing.T) {
	au, _ := FindRegion("AU")
	for u, want := range map[string]bool{
		"https://www.jbhifi.com.au/x": true, "https://www.amazon.com.au/dp/1": true,
		"https://www.walmart.com/ip/1": false, "https://shop.bunnings.com.au/p": true,
	} {
		pu, _ := url.Parse(u)
		if got := au.Allows(pu); got != want {
			t.Errorf("%s: got %v want %v", u, got, want)
		}
	}
	us, _ := url.Parse("https://www.walmart.com/ip/1")
	if !otherRegionOnly(us, au) {
		t.Error("walmart.com should be filtered for AU")
	}
	niche, _ := url.Parse("https://tinyshop.example.com.au/p")
	if otherRegionOnly(niche, au) {
		t.Error("unknown niche shop should be kept")
	}
}

func TestRelevant(t *testing.T) {
	q := "Logitech MX Master 3S"
	if !relevant(q, "Logitech MX Master 3S Wireless Mouse - JB Hi-Fi") {
		t.Error("should match")
	}
	if relevant(q, "Logitech MX Master 3 mouse") {
		t.Error("different model should not match")
	}
	if relevant(q, "Logitech MX Keys keyboard 3S") {
		t.Error("different product should not match")
	}
}

func TestLooksLikeURL(t *testing.T) {
	for in, want := range map[string]bool{"https://a.com/x": true, "www.a.com": true, "a.com/p/1": true,
		"WH-1000XM5": false, "rtx 4070": false, "sony wh-1000xm5.5": false} {
		if got := looksLikeURL(in); got != want {
			t.Errorf("%q: got %v", in, got)
		}
	}
}

func TestConditionalPrices(t *testing.T) {
	member := `<script type="application/ld+json">{"@type":"Product","name":"Thing","offers":[
	 {"@type":"Offer","price":"100","priceCurrency":"AUD"},
	 {"@type":"Offer","name":"Member price","price":"89.50","priceCurrency":"AUD"}]}</script>`
	r, err := ParsePage(member)
	if err != nil || r.Price != 89.5 || r.Caveat != cvMember || r.StdPrice != 100 || r.Currency != "AUD" {
		t.Errorf("member: %+v %v", r, err)
	}
	sub := `<script type="application/ld+json">{"@type":"Product","name":"Pods","offers":{"@type":"Offer","price":50,"priceCurrency":"USD",
	 "priceSpecification":[{"price":50,"priceType":"https://schema.org/ListPrice"},{"price":45,"name":"Subscribe & Save"}]}}</script>`
	r, _ = ParsePage(sub)
	if r.Price != 45 || r.Caveat != cvRepeat || r.StdPrice != 50 {
		t.Errorf("subscription: %+v", r)
	}
	html := `<meta property="product:price:amount" content="40.00"><meta property="product:price:currency" content="GBP">
	 <div>Subscribe &amp; Save <span>£34.00</span></div>`
	r, _ = ParsePage(html)
	if r.Price != 34 || r.Caveat != cvRepeat || r.StdPrice != 40 {
		t.Errorf("page text: %+v", r)
	}
	plain, _ := ParsePage(`<meta property="product:price:amount" content="40.00"><div>Members price <span>£1.00</span></div>`)
	if plain.Price != 40 || plain.Caveat != "" {
		t.Errorf("implausible conditional price should be ignored: %+v", plain)
	}
}
