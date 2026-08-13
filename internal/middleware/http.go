package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if len(id) > 128 || id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(s int) { r.status = s; r.ResponseWriter.WriteHeader(s) }
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &recorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			attrs := []any{"request_id", RequestIDFrom(r.Context()), "method", r.Method, "route", route, "status", rw.status, "duration_ms", time.Since(start).Milliseconds(), "client_type", strings.TrimSpace(r.Header.Get("X-Client-Type"))}
			if p, ok := PrincipalFrom(r.Context()); ok {
				attrs = append(attrs, "user_id", p.UserID.String())
			}
			if v, ok := VisitorID(r); ok {
				attrs = append(attrs, "visitor_session_id", v.String())
			}
			log.Info("http request", attrs...)
		})
	}
}
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					log.Error("panic recovered", "request_id", RequestIDFrom(r.Context()), "panic", v, "stack", string(debug.Stack()))
					httpapi.WriteError(w, httpapi.E(500, "INTERNAL_ERROR", "An unexpected error occurred"), r.Context())
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
func BFF(secrets []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !security.MatchSecret(r.Header.Get("X-BFF-Secret"), secrets) {
				httpapi.WriteError(w, httpapi.ErrInvalidClient, r.Context())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type visitor struct {
	lim  *rate.Limiter
	seen time.Time
}
type Limiter struct {
	mu      sync.Mutex
	clients map[string]*visitor
	r       rate.Limit
	burst   int
}

func NewLimiter(perMinute int, burst int) *Limiter {
	return &Limiter{clients: map[string]*visitor{}, r: rate.Limit(float64(perMinute) / 60), burst: burst}
}
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientIP(r)
		if p, ok := PrincipalFrom(r.Context()); ok {
			key = "u:" + p.UserID.String()
		}
		l.mu.Lock()
		v := l.clients[key]
		if v == nil {
			v = &visitor{rate.NewLimiter(l.r, l.burst), time.Now()}
			l.clients[key] = v
		}
		v.seen = time.Now()
		allowed := v.lim.Allow()
		if len(l.clients) > 10000 {
			cut := time.Now().Add(-15 * time.Minute)
			for k, x := range l.clients {
				if x.seen.Before(cut) {
					delete(l.clients, k)
				}
			}
		}
		l.mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(60))
			httpapi.WriteError(w, httpapi.E(429, "RATE_LIMITED", "Too many requests"), r.Context())
			return
		}
		next.ServeHTTP(w, r)
	})
}
func clientIP(r *http.Request) string {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return h
	}
	return r.RemoteAddr
}
