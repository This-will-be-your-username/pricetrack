package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type toServer struct{ to string }

func (r toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.to)
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func fakeRegion(n int) Region {
	rg := Region{Code: "AU", Name: "Australia", Currency: "AUD"}
	for i := 1; i <= n; i++ {
		rg.Retailers = append(rg.Retailers, Retailer{Name: fmt.Sprintf("Shop %d", i), Domains: []string{fmt.Sprintf("shop%d.example", i)}})
	}
	return rg
}

// countingSearch answers plain queries with a few shops, and "site:a OR site:b" queries
// with one result per named domain.
type countingSearch struct{ queries []string }

func (f *countingSearch) Search(_ context.Context, q, _ string) ([]SearchResult, error) {
	f.queries = append(f.queries, q)
	var rs []SearchResult
	if strings.Contains(q, "site:") {
		for _, part := range strings.Fields(q) {
			if d, ok := strings.CutPrefix(strings.Trim(part, "()"), "site:"); ok {
				rs = append(rs, SearchResult{Title: "Widget X1", URL: "https://" + d + "/p/widget-x1"})
			}
		}
		return rs, nil
	}
	for n := 1; n <= 3; n++ {
		rs = append(rs, SearchResult{Title: "Widget X1", URL: fmt.Sprintf("https://shop%d.example/p/widget-x1", n)})
	}
	return rs, nil
}

func newShopApp(t *testing.T, s Searcher) *App {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<title>Widget X1</title><meta property="product:price:amount" content="55.00"><meta property="product:price:currency" content="AUD">`)
	}))
	t.Cleanup(srv.Close)
	return &App{search: s, fetch: &Fetcher{client: &http.Client{Transport: toServer{srv.URL}}}}
}

func TestFindOffersStaysWithinSearchBudget(t *testing.T) {
	cs := &countingSearch{}
	offers, err := newShopApp(t, cs).FindOffers(context.Background(), "Widget X1", fakeRegion(24))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.queries) > maxSearchesPerScan {
		t.Errorf("%d searches, budget is %d: %q", len(cs.queries), maxSearchesPerScan, cs.queries)
	}
	if len(offers) < 8 {
		t.Errorf("combined site: queries should surface more retailers, got %d", len(offers))
	}
}

func TestLookupOffersFillToTarget(t *testing.T) {
	rg := fakeRegion(24)
	have := []string{"https://shop2.example/p/1", "https://shop5.example/p/9"}
	got := lookupOffers("Widget X1", rg, have, map[string]bool{}, targetRetailers)
	if len(have)+len(got) != targetRetailers {
		t.Fatalf("have %d + lookups %d, want %d", len(have), len(got), targetRetailers)
	}
	for _, o := range got {
		if o.Retailer == "Shop 2" || o.Retailer == "Shop 5" {
			t.Errorf("retailer already listed was offered again: %s", o.Retailer)
		}
		if !o.Lookup || !strings.Contains(o.URL, "site%3Ashop") {
			t.Errorf("bad lookup link %+v", o)
		}
	}
	// dismissed links are not offered again
	again := lookupOffers("Widget X1", rg, have, map[string]bool{got[0].URL: true}, targetRetailers)
	for _, o := range again {
		if o.URL == got[0].URL {
			t.Error("dismissed lookup was re-offered")
		}
	}
	// a short region list just yields what it has
	if n := len(lookupOffers("x", fakeRegion(5), nil, nil, 15)); n != 5 {
		t.Errorf("want 5, got %d", n)
	}
}

func TestRegionSearchURLs(t *testing.T) {
	au, _ := FindRegion("AU")
	if u := au.Retailers[0].SearchURL("dog food 15kg"); u != "https://www.amazon.com.au/s?k=dog+food+15kg" {
		t.Errorf("amazon: %s", u)
	}
	if u := (Retailer{Name: "X", Domains: []string{"x.example"}}).SearchURL("a b"); !strings.HasPrefix(u, "https://www.google.com/search?q=site%3Ax.example") {
		t.Errorf("fallback: %s", u)
	}
	for _, rg := range regions {
		if len(rg.Retailers) < targetRetailers && rg.Code != "NZ" && rg.Code != "CA" {
			t.Errorf("%s lists only %d retailers", rg.Code, len(rg.Retailers))
		}
	}
}

type flaky struct {
	calls int
	err   error
}

func (f *flaky) Search(context.Context, string, string) ([]SearchResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return []SearchResult{{Title: "t", URL: "https://a.example"}}, nil
}

func TestThrottleBacksOffAfterRateLimitAndCaches(t *testing.T) {
	inner := &flaky{err: fmt.Errorf("HTTP 202: %w", errRateLimited)}
	th := NewThrottledSearcher(inner, time.Millisecond)
	_, err := th.Search(context.Background(), "q", "AU")
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("want RateLimitedError, got %v", err)
	}
	// while paused the engine must not be contacted again
	for i := 0; i < 5; i++ {
		th.Search(context.Background(), "other", "AU")
	}
	if inner.calls != 1 {
		t.Errorf("engine hit %d times during backoff, want 1", inner.calls)
	}

	ok := NewThrottledSearcher(&flaky{}, time.Millisecond)
	ok.Search(context.Background(), "Same Query", "AU")
	ok.Search(context.Background(), "same query ", "AU")
	if ok.inner.(*flaky).calls != 1 {
		t.Errorf("repeat query should be served from cache")
	}
}
