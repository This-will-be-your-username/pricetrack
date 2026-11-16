package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type toServer struct{ to string }

func (r toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.to)
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

// fakeSearch answers plain queries with a few shops and "site:" queries with that shop.
type fakeSearch struct{ calls int }

func (f *fakeSearch) Search(_ context.Context, q, _ string) ([]SearchResult, error) {
	f.calls++
	if i := strings.Index(q, "site:"); i >= 0 {
		d := q[i+5:]
		return []SearchResult{{Title: "Widget X1 at " + d, URL: "https://" + d + "/p/widget-x1"}}, nil
	}
	var rs []SearchResult
	for n := 1; n <= 4; n++ {
		rs = append(rs, SearchResult{Title: "Widget X1", URL: fmt.Sprintf("https://shop%d.example/p/widget-x1", n)})
	}
	return rs, nil
}

func TestFindOffersWidensSearchToReachTarget(t *testing.T) {
	searchPause = 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<title>Widget X1</title><meta property="product:price:amount" content="%d.00"><meta property="product:price:currency" content="AUD">`, 50+len(r.Host)%7)
	}))
	defer srv.Close()

	rg := Region{Code: "AU", Name: "Australia", Currency: "AUD"}
	for n := 1; n <= 24; n++ {
		rg.Retailers = append(rg.Retailers, Retailer{fmt.Sprintf("Shop %d", n), []string{fmt.Sprintf("shop%d.example", n)}})
	}
	fs := &fakeSearch{}
	app := &App{search: fs, fetch: &Fetcher{client: &http.Client{Transport: toServer{srv.URL}}}}

	offers, err := app.FindOffers(context.Background(), "Widget X1", rg)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) < targetRetailers {
		t.Fatalf("got %d offers, want at least %d (search calls: %d)", len(offers), targetRetailers, fs.calls)
	}
	hosts := map[string]bool{}
	for _, o := range offers {
		hosts[o.Host] = true
	}
	if len(hosts) != len(offers) {
		t.Errorf("duplicate retailers in results")
	}
}
