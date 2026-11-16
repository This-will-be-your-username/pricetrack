package main

import "testing"

func TestCCC(t *testing.T) {
	urls := []string{
		"https://www.jbhifi.com.au/products/x",
		"https://www.amazon.com/dp/B000000001/ref=x",
		"https://www.amazon.com.au/Logitech-Master/dp/B0FNV6GP6K?th=1",
	}
	c := cccFor(urls, "AU")
	if c == nil || c.ASIN != "B0FNV6GP6K" || c.PageURL != "https://au.camelcamelcamel.com/product/B0FNV6GP6K" ||
		c.ChartURL[:len("https://charts.camelcamelcamel.com/au/B0FNV6GP6K/amazon.png")] != "https://charts.camelcamelcamel.com/au/B0FNV6GP6K/amazon.png" {
		t.Fatalf("AU: %+v", c)
	}
	if c := cccFor(urls, "US"); c == nil || c.ASIN != "B000000001" || c.PageURL != "https://camelcamelcamel.com/product/B000000001" {
		t.Fatalf("US: %+v", c)
	}
	if cccFor([]string{"https://www.amazon.com.au/s?k=dog+food", "https://www.kmart.com.au/p/1"}, "AU") != nil {
		t.Error("search links have no ASIN")
	}
	if u := cccSearchURL("GB", "mx master"); u != "https://uk.camelcamelcamel.com/search?sq=mx+master" {
		t.Errorf("search: %s", u)
	}
	if cccSearchURL("NZ", "x") != "" {
		t.Error("NZ has no CamelCamelCamel site")
	}
}
