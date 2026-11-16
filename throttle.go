package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// errRateLimited marks a response from a search engine that means "slow down":
// HTTP 202/403/429/503 or a captcha/anomaly page.
var errRateLimited = errors.New("search engine is rate-limiting")

// RateLimitedError is returned while searching is paused after a rate limit.
type RateLimitedError struct{ Until time.Time }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("the search engine is rate-limiting this server; searching is paused until %s", e.Until.Format("15:04"))
}

// ThrottledSearcher wraps a Searcher so the whole process behaves politely:
//   - one search at a time, with a minimum gap (plus jitter) between requests;
//   - results are cached, so repeating a scan costs nothing;
//   - after a rate-limit response, all searching pauses and the pause doubles
//     on each consecutive hit (2m, 4m, ... up to 30m) instead of retrying.
type ThrottledSearcher struct {
	inner    Searcher
	gap      time.Duration
	cacheTTL time.Duration

	mu      sync.Mutex // also serialises searches
	last    time.Time
	until   time.Time
	backoff time.Duration
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	at time.Time
	rs []SearchResult
}

func NewThrottledSearcher(inner Searcher, gap time.Duration) *ThrottledSearcher {
	return &ThrottledSearcher{inner: inner, gap: gap, cacheTTL: 12 * time.Hour, cache: map[string]cacheEntry{}}
}

func (t *ThrottledSearcher) Search(ctx context.Context, q, region string) ([]SearchResult, error) {
	key := region + "|" + strings.ToLower(strings.TrimSpace(q))
	t.mu.Lock()
	defer t.mu.Unlock()

	if e, ok := t.cache[key]; ok && time.Since(e.at) < t.cacheTTL {
		return e.rs, nil
	}
	if now := time.Now(); now.Before(t.until) {
		return nil, &RateLimitedError{Until: t.until}
	}

	// Space requests out, with jitter so we don't look like a metronome.
	wait := t.gap + time.Duration(rand.Int63n(int64(t.gap/2)+1)) - time.Since(t.last)
	if wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	t.last = time.Now()

	rs, err := t.inner.Search(ctx, q, region)
	switch {
	case err == nil:
		t.backoff = 0
		t.cache[key] = cacheEntry{at: time.Now(), rs: rs}
		return rs, nil
	case errors.Is(err, errRateLimited):
		if t.backoff == 0 {
			t.backoff = 2 * time.Minute
		} else if t.backoff < 30*time.Minute {
			t.backoff *= 2
		}
		t.until = time.Now().Add(t.backoff)
		return nil, &RateLimitedError{Until: t.until}
	}
	return nil, err
}
