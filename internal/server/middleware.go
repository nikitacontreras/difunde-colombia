package server

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const clientSessionCookieName = "cdfd_sid"

// requestID genera o propaga un id de request para correlacionar logs.
// Nunca contiene IP ni datos personales.
func requestID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 16)
	now := time.Now().UnixNano()
	for i := 0; i < 16; i++ {
		b[i] = hex[(now>>(uint(i)*4))&0xf]
	}
	return string(b)
}

type loggingWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggingWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *loggingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// middleware encadena recover, request id, logging, gzip, seguridad y rate limit.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = requestID()
		}
		w.Header().Set("X-Request-Id", rid)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), payment=(), usb=()")

		start := time.Now()
		lw := &loggingWriter{ResponseWriter: w}

		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "rid", rid, "path", r.URL.Path, "err", rec)
				http.Error(lw, "internal error", http.StatusInternalServerError)
			}
			slog.Info("req",
				"rid", rid,
				"method", r.Method,
				"path", r.URL.Path,
				"status", lw.status,
				"dur_ms", float64(time.Since(start).Milliseconds()),
			)
		}()

		if s.gzipEnabled(r) {
			gz := gzip.NewWriter(w)
			lw.Header().Set("Content-Encoding", "gzip")
			defer gz.Close()
			lw.ResponseWriter = &gzipWriter{ResponseWriter: w, w: gz}
		}

		ensureClientSessionCookie(lw, r)

		next.ServeHTTP(lw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	w *gzip.Writer
}

func (g *gzipWriter) Write(b []byte) (int, error) { return g.w.Write(b) }

func (g *gzipWriter) Flush() {
	_ = g.w.Flush()
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) gzipEnabled(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Path == "/p" || strings.HasPrefix(r.URL.Path, "/probe/") {
		return false
	}
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

func ensureClientSessionCookie(w http.ResponseWriter, r *http.Request) {
	if clientSessionID(r) != "" {
		return
	}
	cookie := &http.Cookie{
		Name:     clientSessionCookieName,
		Value:    newClientSessionID(),
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

func clientSessionID(r *http.Request) string {
	cookie, err := r.Cookie(clientSessionCookieName)
	if err != nil {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(cookie.Value))
	if len(value) != 32 {
		return ""
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return value
}

func newClientSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return requestID() + requestID()
	}
	return hex.EncodeToString(b)
}

func tooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

// bodyLimit limita el tamaño del cuerpo de las peticiones.
func bodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimiter fijo por ventana, por llave (IP + ruta).
// Amplio a propósito: NAT móvil/CGNAT comparten IP.
type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*rlBucket
}

type rlBucket struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, buckets: map[string]*rlBucket{}}
}

func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &rlBucket{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-2 * rl.window)
	for k, b := range rl.buckets {
		if time.Now().After(b.resetAt) || b.resetAt.Before(cutoff) {
			delete(rl.buckets, k)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var _ io.Writer = (*gzipWriter)(nil)
