package middleware

import (
	"context"
	"log/slog"
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

// BFF admits the callers that hold one of `secrets`, and separately decides which of them
// may also speak for somebody else.
//
// Those are two different questions and they were one. Every caller carries a key, but the
// mobile app carries its key inside the application package -- anything shipped to a device
// is readable on that device, so that key is a door handle rather than a lock. It is enough
// to be let in; it is not enough to be believed about who the request is for. Only a caller
// holding a secret from `addressBearers` -- our own web server, whose copy never leaves a
// machine we run -- has its `X-Client-IP` read.
//
// Leaving `addressBearers` empty means nobody may state an address, and the rate limits
// fall back to the connection. That is the safe direction rather than the useful one, so
// main says so out loud at startup when it happens.
func BFF(secrets, addressBearers []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			offered := r.Header.Get("X-BFF-Secret")
			if !security.MatchSecret(offered, secrets) {
				httpapi.WriteError(w, httpapi.ErrInvalidClient, r.Context())
				return
			}
			if len(addressBearers) > 0 && security.MatchSecret(offered, addressBearers) {
				next.ServeHTTP(w, WithTrustedProxy(r))
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
		// Two years, subdomains included. The API is reachable only over TLS already; this
		// is what stops a browser trying the other scheme even once.
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
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

// One address is not one person. A household, an office and -- the case that matters in
// Turkey -- a mobile carrier's shared address all put many readers behind one of them, so
// an address bucket held to a single person's allowance would refuse people who did
// nothing. It is a ceiling on rotation, not a per-person limit: the caller-written session
// id gives an unlimited budget on its own, and this is what caps it. Twelve is a guess at
// how many strangers plausibly share an address at once, and it is meant to be raised if
// real traffic says otherwise, not lowered.
const addressShare = 12

func (l *Limiter) bucket(key string) *visitor {
	v := l.clients[key]
	if v != nil {
		return v
	}
	r, burst := l.r, l.burst
	if strings.HasPrefix(key, "a:") {
		r, burst = r*addressShare, burst*addressShare
	}
	v = &visitor{rate.NewLimiter(r, burst), time.Now()}
	l.clients[key] = v
	return v
}
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		// Every bucket this request belongs to, and it has to satisfy all of them. One
		// key was enough while the key could not be chosen by the caller; the session id
		// can be, and a fresh one on every request emptied no bucket at all. Spending
		// from the address as well is what makes rotating it pointless.
		allowed := true
		for _, key := range limiterKeys(r) {
			v := l.bucket(key)
			v.seen = time.Now()
			// Not short-circuited: a request that is refused still costs every bucket it
			// belongs to, or the cheapest way past a full bucket would be to keep asking.
			allowed = v.lim.Allow() && allowed
		}
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

// Who this request is for, not who delivered it.
//
// A signed-in person is one bucket and nothing else: the account is proof, and two devices
// on one account are still one person.
//
// Everyone else belongs to as many buckets as we can name them by, because neither name is
// sound on its own. The browsing session is the better signal -- it is what stopped a
// results page prefetching two dozen store links from emptying one shared bucket and then
// getting 429s for stores that plainly exist -- but the caller writes it, so a new one on
// every request is a budget with no end to it. The address cannot be chosen that way, and
// since ClientIP now returns the person's own address rather than the web server's, it is
// a real second name for the same request.
func limiterKeys(r *http.Request) []string {
	if p, ok := PrincipalFrom(r.Context()); ok {
		return []string{"u:" + p.UserID.String()}
	}
	keys := make([]string, 0, 2)
	if visitor := strings.TrimSpace(r.Header.Get("X-Visitor-Session-ID")); visitor != "" {
		if _, err := uuid.Parse(visitor); err == nil {
			keys = append(keys, "v:"+visitor)
		}
	}
	if ip := ClientIP(r); ip != "" {
		keys = append(keys, "a:"+ip)
	}
	// A first request carrying neither is rare and still has to be counted somewhere.
	if len(keys) == 0 {
		keys = append(keys, "a:unknown")
	}
	return keys
}
