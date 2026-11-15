package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	health := flag.Bool("healthcheck", false, "probe the local server and exit (for container health checks)")
	flag.Parse()

	addr := env("ADDR", ":8080")
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *health {
		os.Exit(healthcheck(addr))
	}

	dataDir := env("DATA_DIR", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := OpenStore(filepath.Join(dataDir, "pricetrack.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	interval, err := time.ParseDuration(env("REFRESH_INTERVAL", "6h"))
	if err != nil || interval < time.Minute {
		log.Fatalf("REFRESH_INTERVAL must be a duration of at least 1m (got %q)", os.Getenv("REFRESH_INTERVAL"))
	}
	allowPrivate, _ := strconv.ParseBool(os.Getenv("ALLOW_PRIVATE"))

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	app, err := NewApp(st, NewFetcher(allowPrivate), sub, os.Getenv("AUTH_USER"), os.Getenv("AUTH_PASS"))
	if err != nil {
		log.Fatal(err)
	}
	app.search = NewSearcher(app.fetch, os.Getenv("SEARCH_API_KEY"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.RefreshLoop(ctx, interval)

	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("pricetrack %s listening on %s (refresh every %s)", version, addr, interval)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func healthcheck(addr string) int {
	host := addr
	if len(host) > 0 && host[0] == ':' {
		host = "127.0.0.1" + host
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + host + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
