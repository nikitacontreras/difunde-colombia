package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cachedJSON struct {
	body         []byte
	etag         string
	cacheControl string
	expiresAt    time.Time
}

type responseCache struct {
	mu         sync.RWMutex
	items      map[string]cachedJSON
	maxEntries int
}

type fingerprintTracker struct {
	mu          sync.Mutex
	seen        map[string]map[string]time.Time
	maxDistinct int
	window      time.Duration
}

func newResponseCache(maxEntries int) *responseCache {
	return &responseCache{
		items:      make(map[string]cachedJSON),
		maxEntries: maxEntries,
	}
}

func newFingerprintTracker(maxDistinct int, window time.Duration) *fingerprintTracker {
	return &fingerprintTracker{
		seen:        make(map[string]map[string]time.Time),
		maxDistinct: maxDistinct,
		window:      window,
	}
}

func (c *responseCache) get(key string) (cachedJSON, bool) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(item.expiresAt) {
		return cachedJSON{}, false
	}
	item.body = append([]byte(nil), item.body...)
	return item, true
}

func (c *responseCache) set(key string, item cachedJSON) {
	item.body = append([]byte(nil), item.body...)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxEntries {
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiresAt) {
				delete(c.items, k)
			}
		}
		if len(c.items) >= c.maxEntries {
			c.items = make(map[string]cachedJSON)
		}
	}
	c.items[key] = item
}

func (c *responseCache) clear() {
	c.mu.Lock()
	c.items = make(map[string]cachedJSON)
	c.mu.Unlock()
}

func (t *fingerprintTracker) allow(ip, fp string) bool {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	fps, ok := t.seen[ip]
	if !ok {
		fps = make(map[string]time.Time)
		t.seen[ip] = fps
	}

	for k, ts := range fps {
		if now.Sub(ts) > t.window {
			delete(fps, k)
		}
	}
	if len(fps) == 0 {
		delete(t.seen, ip)
		fps = make(map[string]time.Time)
		t.seen[ip] = fps
	}

	if _, ok := fps[fp]; ok {
		fps[fp] = now
		return true
	}
	if len(fps) >= t.maxDistinct {
		return false
	}
	fps[fp] = now
	return true
}

func cacheKey(parts ...string) string {
	return strings.Join(parts, "|")
}

func cacheEntry(body []byte, cacheControl string, ttl time.Duration) cachedJSON {
	sum := sha256.Sum256(body)
	return cachedJSON{
		body:         append([]byte(nil), body...),
		etag:         `"` + hex.EncodeToString(sum[:8]) + `"`,
		cacheControl: cacheControl,
		expiresAt:    time.Now().Add(ttl),
	}
}

func writeCachedJSON(w http.ResponseWriter, r *http.Request, item cachedJSON) {
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, item.etag) {
		w.Header().Set("ETag", item.etag)
		w.Header().Set("Cache-Control", item.cacheControl)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", item.cacheControl)
	w.Header().Set("ETag", item.etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(item.body)
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
}

func retryAfterSeconds(ttl time.Duration) string {
	if ttl <= 0 {
		return "60"
	}
	return strconv.Itoa(int(ttl.Seconds()))
}
