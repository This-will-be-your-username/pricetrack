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
	"time"
)

type App struct {
	st     *Store
	fetch  *Fetcher
	tpl    *template.Template
	static http.Handler
	search Searcher
	user   string
	pass   string
}

func NewApp(st *Store, f *Fetcher, web fs.FS, user, pass string) (*App, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"money": money,
		"ago":   ago,
		"abs":   math.Abs,
	}).ParseFS(web, "templates/*.html")
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(web, "static")
	if err != nil {
		return nil, err
	}
	return &App{st: st, fetch: f, tpl: tpl, user: user, pass: pass,
		static: http.StripPrefix("/static/", http.FileServerFS(static))}, nil
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /static/", a.static)
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("POST /products", a.add)
	mux.HandleFunc("POST /products/{id}/refresh", a.refresh)
	mux.HandleFunc("POST /products/{id}/target", a.target)
	mux.HandleFunc("POST /products/{id}/delete", a.remove)
	mux.HandleFunc("GET /find", a.find)
	mux.HandleFunc("GET /settings", a.settings)
	mux.HandleFunc("POST /settings", a.saveSettings)
	mux.HandleFunc("GET /api/products", a.apiList)
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
	if err != nil {
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
		// Cheap CSRF guard: state-changing requests must come from our own origin.
		if r.Method == http.MethodPost {
			if !sameOrigin(r) {
				http.Error(w, "cross-origin request blocked", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- view models ----

type ProductView struct {
	*Product
	Spark     string // polyline points
	Trend     string // up | down | flat
	Delta     float64
	DeltaPct  float64
	HasDelta  bool
	Lowest    bool
	TargetHit bool
	Points    int
}

type IndexData struct {
	Products []ProductView
	Total    int
	Drops    int
	Hits     int
	Msg      string
	Err      string
	Region   Region // zero Code when unset
	Symbol   string
}

// region returns the configured region. set is false until the user picks one.
func (a *App) region() (rg Region, set bool) {
	code := a.st.GetSetting("region")
	if code == AnyRegion {
		return Region{Code: AnyRegion, Name: "Worldwide", Currency: "USD"}, true
	}
	rg, set = FindRegion(code)
	return rg, set
}

func symbol(cur string) string {
	if s := currencySymbols[cur]; s != "" {
		return s
	}
	return cur
}

type FindData struct {
	Query    string
	Searched bool
	Offers   []Offer
	Region   Region
	Err      string
}

func (a *App) find(w http.ResponseWriter, r *http.Request) {
	d := FindData{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	rg, set := a.region()
	if set {
		d.Region = rg
	} else {
		rg = Region{Code: AnyRegion}
	}
	if d.Query != "" {
		d.Searched = true
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		offers, err := a.FindOffers(ctx, d.Query, rg)
		if err != nil {
			d.Err = clip(err.Error())
		}
		d.Offers = offers
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tpl.ExecuteTemplate(w, "find.html", d); err != nil {
		log.Printf("render: %v", err)
	}
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tpl.ExecuteTemplate(w, "settings.html", d); err != nil {
		log.Printf("render: %v", err)
	}
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

func buildView(p *Product, hist []float64) ProductView {
	v := ProductView{Product: p, Trend: "flat", Points: len(hist)}
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
		lo, hi := hist[0], hist[0]
		for _, h := range hist {
			lo, hi = math.Min(lo, h), math.Max(hi, h)
		}
		v.Lowest = p.Price <= lo
		v.Spark = sparkline(hist, lo, hi)
	}
	v.TargetHit = p.HasTarget && p.HasPrice && p.Price <= p.Target
	return v
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
	ps, err := a.st.ListProducts()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d := IndexData{Total: len(ps), Msg: clip(r.URL.Query().Get("msg")), Err: clip(r.URL.Query().Get("err"))}
	if rg, set := a.region(); set {
		d.Region = rg
	}
	d.Symbol = symbol(d.Region.Currency)
	for _, p := range ps {
		hist, _ := a.st.History(p.ID)
		v := buildView(p, hist)
		if v.Trend == "down" {
			d.Drops++
		}
		if v.TargetHit {
			d.Hits++
		}
		d.Products = append(d.Products, v)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tpl.ExecuteTemplate(w, "index.html", d); err != nil {
		log.Printf("render: %v", err)
	}
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

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

func (a *App) check(ctx context.Context, p *Product) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := a.fetch.Fetch(ctx, p.URL)
	if res != nil && res.Currency == "" {
		res.Currency = "USD"
		if rg, set := a.region(); set {
			res.Currency = rg.Currency
		}
	}
	if rerr := a.st.RecordCheck(p.ID, res, err); rerr != nil {
		log.Printf("record %d: %v", p.ID, rerr)
	}
	return err
}

func (a *App) add(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	u, err := normalizeURL(r.FormValue("url"))
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	// Links from "Find retailers" were already filtered for the region (and may be
	// niche suppliers outside the built-in list), so they skip the allow-list.
	if rg, set := a.region(); set && rg.Code != AnyRegion && r.FormValue("found") != "1" {
		if pu, _ := url.Parse(u); pu == nil || !rg.Allows(pu) {
			redirect(w, r, "err", fmt.Sprintf("%s isn't a supported retailer for %s. Change your region in Settings, or pick Worldwide.",
				pu.Hostname(), rg.Name))
			return
		}
	}
	target, err := parseTarget(r.FormValue("target"))
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	id, err := a.st.AddProduct(u, MerchantOf(u), u, target)
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	p, err := a.st.GetProduct(id)
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	if err := a.check(r.Context(), p); err != nil {
		redirect(w, r, "err", "Added, but "+err.Error())
		return
	}
	redirect(w, r, "msg", "Now tracking it")
}

func (a *App) product(w http.ResponseWriter, r *http.Request) *Product {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		var p *Product
		if p, err = a.st.GetProduct(id); err == nil {
			return p
		}
	}
	http.NotFound(w, r)
	return nil
}

func (a *App) refresh(w http.ResponseWriter, r *http.Request) {
	p := a.product(w, r)
	if p == nil {
		return
	}
	if err := a.check(r.Context(), p); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	redirect(w, r, "msg", "Price updated")
}

func (a *App) target(w http.ResponseWriter, r *http.Request) {
	p := a.product(w, r)
	if p == nil {
		return
	}
	t, err := parseTarget(r.FormValue("target"))
	if err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	if err := a.st.SetTarget(p.ID, t); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	redirect(w, r, "msg", "Target saved")
}

func (a *App) remove(w http.ResponseWriter, r *http.Request) {
	p := a.product(w, r)
	if p == nil {
		return
	}
	if err := a.st.DeleteProduct(p.ID); err != nil {
		redirect(w, r, "err", err.Error())
		return
	}
	redirect(w, r, "msg", "Removed")
}

func (a *App) apiList(w http.ResponseWriter, _ *http.Request) {
	ps, err := a.st.ListProducts()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type item struct {
		ID          int64     `json:"id"`
		URL         string    `json:"url"`
		Name        string    `json:"name"`
		Merchant    string    `json:"merchant"`
		Currency    string    `json:"currency"`
		Price       *float64  `json:"price"`
		Target      *float64  `json:"target"`
		LastChecked *int64    `json:"last_checked"`
		History     []float64 `json:"history"`
	}
	out := make([]item, 0, len(ps))
	for _, p := range ps {
		it := item{ID: p.ID, URL: p.URL, Name: p.Name, Merchant: p.Merchant, Currency: p.Currency}
		if p.HasPrice {
			it.Price = &p.Price
		}
		if p.HasTarget {
			it.Target = &p.Target
		}
		if !p.LastChecked.IsZero() {
			t := p.LastChecked.Unix()
			it.LastChecked = &t
		}
		it.History, _ = a.st.History(p.ID)
		out = append(out, it)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// RefreshLoop re-checks every product on the given interval until ctx ends.
func (a *App) RefreshLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ps, err := a.st.ListProducts()
		if err != nil {
			log.Printf("refresh list: %v", err)
			continue
		}
		for _, p := range ps {
			if err := a.check(ctx, p); err != nil {
				log.Printf("refresh %q: %v", p.URL, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second): // be polite to merchants
			}
		}
	}
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
