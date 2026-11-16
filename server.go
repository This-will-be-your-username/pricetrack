package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type App struct {
	st     *Store
	fetch  *Fetcher
	tpl    *template.Template
	static http.Handler
	search Searcher
	user   string
	pass   string

	mu        sync.Mutex
	searching map[int64]bool
}

func NewApp(st *Store, f *Fetcher, web fs.FS, user, pass string) (*App, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"money":   money,
		"ago":     ago,
		"abs":     math.Abs,
		"initial": initial,
		"iconFor": iconFor,
	}).ParseFS(web, "templates/*.html")
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(web, "static")
	if err != nil {
		return nil, err
	}
	return &App{st: st, fetch: f, tpl: tpl, user: user, pass: pass, searching: map[int64]bool{},
		static: http.StripPrefix("/static/", http.FileServerFS(static))}, nil
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /static/", a.static)
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("POST /items", a.add)
	mux.HandleFunc("POST /refresh", a.refreshAll)
	mux.HandleFunc("POST /items/{id}/refresh", a.refresh)
	mux.HandleFunc("POST /items/{id}/target", a.target)
	mux.HandleFunc("POST /items/{id}/delete", a.remove)
	mux.HandleFunc("POST /items/{id}/links", a.addLink)
	mux.HandleFunc("POST /listings/{id}/delete", a.removeListing)
	mux.HandleFunc("POST /listings/{id}/price", a.setPrice)
	mux.HandleFunc("GET /settings", a.settings)
	mux.HandleFunc("POST /settings", a.saveSettings)
	mux.HandleFunc("GET /api/items", a.apiList)
	return a.secure(mux)
}

// sameOrigin decides whether a POST came from our own pages. Browsers send
// Sec-Fetch-Site; older ones fall back to comparing Origin with the Host
// (also honouring X-Forwarded-Host behind a reverse proxy).
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true // non-browser client (curl etc.)
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == r.Host || u.Host == r.Header.Get("X-Forwarded-Host")
}

func (a *App) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src * data:; frame-ancestors 'none'")
		if a.user != "" && r.URL.Path != "/healthz" {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(a.user)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(a.pass)) != 1 {
				h.Set("WWW-Authenticate", `Basic realm="pricetrack"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
			return
		}
		// Remember the visitor's browser identity so merchant requests made on
		// their behalf look like their own browser.
		next.ServeHTTP(w, r.WithContext(WithClient(r.Context(), r)))
	})
}

// ---- region ----

// region returns the configured region. set is false until the user picks one.
func (a *App) region() (rg Region, set bool) {
	code := a.st.GetSetting("region")
	if code == AnyRegion {
		return Region{Code: AnyRegion, Name: "Worldwide", Currency: "USD"}, true
	}
	return FindRegion(code)
}

func (a *App) defaultCurrency() string {
	if rg, set := a.region(); set && rg.Currency != "" {
		return rg.Currency
	}
	return "USD"
}

func symbol(cur string) string {
	if s := currencySymbols[cur]; s != "" {
		return s
	}
	return cur
}

type SettingsData struct {
	Regions  []Region
	Current  string
	Selected Region
	Msg      string
	Err      string
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	rg, set := a.region()
	d := SettingsData{Regions: regions, Selected: rg, Msg: clip(r.URL.Query().Get("msg")), Err: clip(r.URL.Query().Get("err"))}
	if set {
		d.Current = rg.Code
	}
	a.render(w, "settings.html", d)
}

func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("region")
	if _, ok := FindRegion(code); !ok && code != AnyRegion {
		redirectTo(w, r, "/settings", "err", "Choose a region from the list")
		return
	}
	if err := a.st.SetSetting("region", code); err != nil {
		redirectTo(w, r, "/settings", "err", err.Error())
		return
	}
	redirect(w, r, "msg", "Region saved")
}

func (a *App) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// ---- view models ----

type ListingView struct {
	*Listing
	Best bool
}

type ItemView struct {
	*Item
	Listings  []ListingView
	Best      *Listing
	Std       *Listing // cheapest retailer with no conditions attached
	StdPrice  float64
	Savings   float64 // gap between cheapest and priciest retailer
	Spark     string
	Trend     string // up | down | flat
	Delta     float64
	DeltaPct  float64
	HasDelta  bool
	TargetHit bool
	Searching bool
	Priced    int
	CCC       *CCC   // Amazon price history from CamelCamelCamel, when an Amazon listing exists
	CCCSearch string // CamelCamelCamel search link when it doesn't
}

type IndexData struct {
	Items     []ItemView
	Total     int
	Retailers int
	Hits      int
	Searching bool
	Msg       string
	Err       string
	Region    Region // zero Code when unset
	Symbol    string
}

func (a *App) isSearching(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.searching[id]
}

func (a *App) buildItem(it *Item) (ItemView, error) {
	ls, err := a.st.ListListings(it.ID)
	if err != nil {
		return ItemView{}, err
	}
	hist, _ := a.st.History(it.ID)
	v := ItemView{Item: it, Trend: "flat", Searching: a.isSearching(it.ID)}
	region := ""
	if rg, set := a.region(); set {
		region = rg.Code
	}
	var urls []string
	for _, l := range ls {
		urls = append(urls, l.URL)
	}
	if v.CCC = cccFor(urls, region); v.CCC == nil {
		v.CCCSearch = cccSearchURL(region, it.Name)
	}
	v.Best = bestOf(it.Currency, ls)
	hi := 0.0
	for _, l := range ls {
		if l.HasPrice {
			v.Priced++
			if l.Currency == it.Currency && l.Price > hi {
				hi = l.Price
			}
		}
		v.Listings = append(v.Listings, ListingView{Listing: l, Best: v.Best != nil && l.ID == v.Best.ID})
	}
	for _, l := range ls {
		eff := 0.0
		switch {
		case !l.HasPrice || l.Currency != it.Currency:
			continue
		case l.Caveat == "":
			eff = l.Price
		case l.HasStd:
			eff = l.StdPrice
		default:
			continue
		}
		if v.Std == nil || eff < v.StdPrice {
			v.Std, v.StdPrice = l, eff
		}
	}
	if v.Best != nil {
		v.Savings = hi - v.Best.Price
		v.TargetHit = it.HasTarget && v.Best.Price <= it.Target
	}
	if n := len(hist); n >= 2 {
		v.Delta = hist[n-1] - hist[n-2]
		if hist[n-2] != 0 {
			v.DeltaPct = v.Delta / hist[n-2] * 100
		}
		v.HasDelta = true
		switch {
		case v.Delta < 0:
			v.Trend = "down"
		case v.Delta > 0:
			v.Trend = "up"
		}
		lo, high := hist[0], hist[0]
		for _, h := range hist {
			lo, high = math.Min(lo, h), math.Max(high, h)
		}
		v.Spark = sparkline(hist, lo, high)
	}
	return v, nil
}

func sparkline(h []float64, lo, hi float64) string {
	const w, ht, pad = 100.0, 30.0, 3.0
	var sb strings.Builder
	for i, val := range h {
		x := pad + (w-2*pad)*float64(i)/float64(len(h)-1)
		y := ht / 2
		if hi > lo {
			y = pad + (ht-2*pad)*(1-(val-lo)/(hi-lo))
		}
		fmt.Fprintf(&sb, "%.1f,%.1f ", x, y)
	}
	return strings.TrimSpace(sb.String())
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	items, err := a.st.ListItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d := IndexData{Total: len(items), Msg: clip(r.URL.Query().Get("msg")), Err: clip(r.URL.Query().Get("err"))}
	if rg, set := a.region(); set {
		d.Region = rg
	}
	d.Symbol = symbol(a.defaultCurrency())
	for _, it := range items {
		v, err := a.buildItem(it)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		d.Retailers += v.Priced
		if v.TargetHit {
			d.Hits++
		}
		if v.Searching {
			d.Searching = true
		}
		d.Items = append(d.Items, v)
	}
	a.render(w, "index.html", d)
}

// ---- helpers ----

func redirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	redirectTo(w, r, "/", key, msg)
}

func redirectTo(w http.ResponseWriter, r *http.Request, to, key, msg string) {
	if msg != "" {
		to += "?" + key + "=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func parseTarget(s string) (*float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	f, ok := parsePrice(s)
	if !ok {
		return nil, errors.New("target price must be a positive number")
	}
	return &f, nil
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("paste a product URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("that doesn't look like a valid http(s) URL")
	}
	u.Fragment = ""
	return u.String(), nil
}

// looksLikeURL tells a pasted link from a product name or model number.
func looksLikeURL(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	return strings.Contains(s, "://") || strings.HasPrefix(strings.ToLower(s), "www.") ||
		(strings.Contains(s, ".") && strings.Contains(s, "/"))
}

// cleanTitle trims retailer decoration from a page title ("Name | Store").
func cleanTitle(t string) string {
	for _, sep := range []string{" | ", " – ", " — ", " : "} {
		if i := strings.Index(t, sep); i > 8 {
			t = t[:i]
		}
	}
	t = strings.TrimSpace(t)
	if utf8.RuneCountInString(t) > 110 {
		t = string([]rune(t)[:110])
	}
	return t
}

// iconFor maps a price caveat to its icon name (see the key on the main page).
func iconFor(caveat string) string {
	switch caveat {
	case cvRepeat:
		return "repeat"
	case cvMember:
		return "member"
	case cvBusiness:
		return "business"
	case cvStudent:
		return "student"
	}
	return "tag"
}

func initial(s string) string {
	for _, r := range s {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// ---- checking & discovery ----

func (a *App) checkListing(ctx context.Context, l *Listing) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := a.fetch.Fetch(ctx, l.URL)
	if res != nil && res.Currency == "" {
		res.Currency = a.defaultCurrency()
	}
	if rerr := a.st.UpdateListing(l.ID, res, err); rerr != nil {
		log.Printf("update listing %d: %v", l.ID, rerr)
	}
}

// refreshItem re-checks every known listing and updates the best-price history.
func (a *App) refreshItem(ctx context.Context, id int64) {
	ls, err := a.st.ListListings(id)
	if err != nil {
		log.Printf("refresh %d: %v", id, err)
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, l := range ls {
		wg.Add(1)
		go func(l *Listing) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			a.checkListing(ctx, l)
		}(l)
	}
	wg.Wait()
	if err := a.st.RecordBest(id); err != nil {
		log.Printf("record best %d: %v", id, err)
	}
}

// discover searches the web for retailers selling the item and stores their listings.
// begin marks an item as busy; it returns false if an update is already running.
func (a *App) begin(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.searching[id] {
		return false
	}
	a.searching[id] = true
	return true
}

func (a *App) end(id int64) {
	a.mu.Lock()
	delete(a.searching, id)
	a.mu.Unlock()
}

// update refreshes known listings and/or searches for more retailers. It only
// ever runs because a person asked for it.
func (a *App) update(ctx context.Context, id int64, refresh, search bool) {
	if !a.begin(id) {
		return
	}
	defer a.end(id)
	if refresh {
		a.refreshItem(ctx, id)
	}
	if search {
		a.discover(ctx, id)
	}
}

// background detaches work from the request's lifetime but keeps its values
// (the visitor's browser identity).
func background(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 4*time.Minute)
}

func (a *App) discover(ctx context.Context, id int64) {
	it, err := a.st.GetItem(id)
	if err != nil {
		return
	}
	rg, set := a.region()
	if !set {
		rg = Region{Code: AnyRegion}
	}
	offers, serr := a.FindOffers(ctx, it.Name, rg)
	for _, o := range offers {
		if err := a.st.UpsertListing(id, o.URL, o.Retailer, o.Title, o.Price, o.Currency, o.Caveat, o.StdPrice); err != nil {
			log.Printf("upsert listing: %v", err)
		}
		if o.Blocked {
			a.st.MarkListingError(id, o.URL, o.Reason)
		}
	}
	a.fillLookups(id, it.Name, rg)
	if serr != nil {
		var rl *RateLimitedError
		if errors.As(serr, &rl) {
			serr = fmt.Errorf("The search engine is rate-limiting this server, so only retailer links are listed. Searching resumes after %s.", rl.Until.Format("15:04"))
		}
	} else if n := a.priced(id); n < targetRetailers {
		serr = fmt.Errorf("Found prices at %d retailers; the rest are links for you to check by hand.", n)
	}
	if err := a.st.RecordBest(id); err != nil {
		log.Printf("record best %d: %v", id, err)
	}
	a.st.MarkSearched(id, serr)
}

// priced counts the item's listings with a price.
func (a *App) priced(id int64) int {
	ls, _ := a.st.ListListings(id)
	n := 0
	for _, l := range ls {
		if l.HasPrice {
			n++
		}
	}
	return n
}

// fillLookups tops the item's list up to targetRetailers with search links for the
// region's best-known retailers (no search engine involved), and retires lookup
// links for retailers where a real product page has since been found.
func (a *App) fillLookups(id int64, query string, rg Region) {
	ls, err := a.st.ListListings(id)
	if err != nil {
		return
	}
	real := map[string]bool{}
	for _, l := range ls {
		if !l.Lookup {
			real[l.Retailer] = true
		}
	}
	var have []string
	for _, l := range ls {
		if l.Lookup && !l.HasPrice && real[l.Retailer] {
			a.st.DeleteListingQuiet(l.ID)
			continue
		}
		have = append(have, l.URL)
	}
	for _, o := range lookupOffers(query, rg, have, a.st.Dismissed(id), targetRetailers) {
		if err := a.st.UpsertLookup(id, o.URL, o.Retailer, o.Title, o.Reason); err != nil {
			log.Printf("upsert lookup: %v", err)
		}
	}
}

func (a *App) startUpdate(r *http.Request, id int64, refresh bool) {
	ctx, cancel := background(r)
	go func() {
		defer cancel()
		a.update(ctx, id, refresh, true)
	}()
}

// ---- handlers ----

func (a *App) add(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	q := strings.TrimSpace(r.FormValue("q"))
	if q == "" {
		redirect(w, r, "err", "Enter a product name, model number or link")
		return
	}
	target, err := parseTarget(r.FormValue("target"))
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	cur := a.defaultCurrency()

	if !looksLikeURL(q) {
		if utf8.RuneCountInString(q) > 150 {
			q = string([]rune(q)[:150])
		}
		id, err := a.st.AddItem(q, cur, target)
		if err != nil {
			redirect(w, r, "err", err.Error())
			return
		}
		a.startUpdate(r, id, false)
		redirect(w, r, "msg", "Searching retailers for it…")
		return
	}

	// A pasted link seeds the item: read the product name from it, then look for other retailers.
	u, err := normalizeURL(q)
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, ferr := a.fetch.Fetch(ctx, u)
	if res == nil || res.Name == "" {
		redirect(w, r, "err", "Couldn't read a product from that link. Enter the product name or model number instead.")
		return
	}
	if res.Currency == "" {
		res.Currency = cur
	}
	id, err := a.st.AddItem(cleanTitle(res.Name), res.Currency, target)
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	price := 0.0
	if ferr == nil {
		price = res.Price
	}
	caveat, std := "", 0.0
	if ferr == nil {
		caveat, std = res.Caveat, res.StdPrice
	}
	a.st.UpsertListing(id, u, MerchantOf(u), res.Name, price, res.Currency, caveat, std)
	if res.Image != "" {
		a.st.SetItemImage(id, res.Image)
	}
	a.st.RecordBest(id)
	a.startUpdate(r, id, false)
	redirect(w, r, "msg", "Added — now searching other retailers…")
}

func (a *App) item(w http.ResponseWriter, r *http.Request) *Item {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		var it *Item
		if it, err = a.st.GetItem(id); err == nil {
			return it
		}
	}
	http.NotFound(w, r)
	return nil
}

func (a *App) refresh(w http.ResponseWriter, r *http.Request) {
	it := a.item(w, r)
	if it == nil {
		return
	}
	a.startUpdate(r, it.ID, true)
	redirect(w, r, "msg", "Refreshing prices and searching for more retailers…")
}

// refreshAll refreshes every product, one after another, when the user asks.
func (a *App) refreshAll(w http.ResponseWriter, r *http.Request) {
	items, err := a.st.ListItems()
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	go func() {
		defer cancel()
		for i, it := range items {
			if i > 0 {
				time.Sleep(2 * time.Second) // be polite to merchants
			}
			a.update(ctx, it.ID, true, true)
		}
	}()
	redirect(w, r, "msg", "Refreshing everything…")
}

func (a *App) target(w http.ResponseWriter, r *http.Request) {
	it := a.item(w, r)
	if it == nil {
		return
	}
	t, err := parseTarget(r.FormValue("target"))
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	if err := a.st.SetTarget(it.ID, t); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	redirect(w, r, "msg", "Target saved")
}

func (a *App) remove(w http.ResponseWriter, r *http.Request) {
	it := a.item(w, r)
	if it == nil {
		return
	}
	if err := a.st.DeleteItem(it.ID); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	redirect(w, r, "msg", "Removed")
}

// addLink attaches a specific retailer URL to an item.
func (a *App) addLink(w http.ResponseWriter, r *http.Request) {
	it := a.item(w, r)
	if it == nil {
		return
	}
	u, err := normalizeURL(r.FormValue("url"))
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, ferr := a.fetch.Fetch(ctx, u)
	price, cur, title, caveat, std := 0.0, a.defaultCurrency(), "", "", 0.0
	if res != nil {
		title = res.Name
		if ferr == nil {
			price, caveat, std = res.Price, res.Caveat, res.StdPrice
			if res.Currency != "" {
				cur = res.Currency
			}
		}
	}
	if err := a.st.UpsertListing(it.ID, u, MerchantOf(u), title, price, cur, caveat, std); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	a.st.RecordBest(it.ID)
	if ferr != nil {
		redirect(w, r, "err", "Link added, but "+ferr.Error())
		return
	}
	redirect(w, r, "msg", "Retailer added")
}

// setPrice records a price the user checked by hand on a retailer's site.
func (a *App) setPrice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	l, err := a.st.GetListing(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, err := a.st.GetItem(l.ItemID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	raw := strings.TrimSpace(r.FormValue("price"))
	price := 0.0
	if raw != "" {
		p, ok := parsePrice(raw)
		if !ok {
			redirect(w, r, "err", "Enter the price as a number, e.g. 129.95")
			return
		}
		price = p
	}
	if err := a.st.SetManualPrice(id, price, it.Currency); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	a.st.RecordBest(it.ID)
	redirect(w, r, "msg", "Price saved")
}

func (a *App) removeListing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	l, err := a.st.GetListing(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.st.DeleteListing(id); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	a.st.RecordBest(l.ItemID)
	redirect(w, r, "msg", "Retailer removed")
}

func (a *App) apiList(w http.ResponseWriter, _ *http.Request) {
	items, err := a.st.ListItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type listing struct {
		Retailer string   `json:"retailer"`
		URL      string   `json:"url"`
		Price    *float64 `json:"price"`
		Currency string   `json:"currency"`
		Caveat   string   `json:"caveat,omitempty"`
		Manual   bool     `json:"manual,omitempty"`
		StdPrice *float64 `json:"standard_price,omitempty"`
	}
	type item struct {
		ID       int64     `json:"id"`
		Name     string    `json:"name"`
		Currency string    `json:"currency"`
		Target   *float64  `json:"target"`
		Best     *listing  `json:"best"`
		Listings []listing `json:"listings"`
		History  []float64 `json:"history"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		o := item{ID: it.ID, Name: it.Name, Currency: it.Currency, Listings: []listing{}}
		if it.HasTarget {
			o.Target = &it.Target
		}
		ls, _ := a.st.ListListings(it.ID)
		best := bestOf(it.Currency, ls)
		for _, l := range ls {
			e := listing{Retailer: l.Retailer, URL: l.URL, Currency: l.Currency, Caveat: l.Caveat, Manual: l.Manual}
			if l.HasStd {
				sp := l.StdPrice
				e.StdPrice = &sp
			}
			if l.HasPrice {
				p := l.Price
				e.Price = &p
			}
			o.Listings = append(o.Listings, e)
			if best != nil && l.ID == best.ID {
				b := e
				o.Best = &b
			}
		}
		o.History, _ = a.st.History(it.ID)
		out = append(out, o)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ---- template helpers ----

var currencySymbols = map[string]string{"USD": "$", "EUR": "€", "GBP": "£", "JPY": "¥", "CAD": "CA$",
	"AUD": "A$", "NZD": "NZ$", "INR": "₹", "SGD": "S$"}

func money(cur string, v float64) string {
	sym := currencySymbols[cur]
	if sym == "" {
		return fmt.Sprintf("%s %.2f", cur, v)
	}
	if cur == "JPY" {
		return fmt.Sprintf("%s%.0f", sym, v)
	}
	return fmt.Sprintf("%s%.2f", sym, v)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
