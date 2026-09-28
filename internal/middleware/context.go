package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	principalKey    contextKey = "principal"
	requestIDKey    contextKey = "request_id"
	visitorKey      contextKey = "visitor"
	trustedProxyKey contextKey = "trusted_proxy"
)

// ForwardedClientIP is the address of the person the request is for, as stated by a caller
// that has already proved it is ours.
const ForwardedClientIP = "X-Client-IP"

// WithTrustedProxy marks a request as delivered by a caller that matched the shared
// secret. Nothing else may set it, which is what makes the forwarded address believable.
func WithTrustedProxy(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), trustedProxyKey, true))
}

func trustedProxy(ctx context.Context) bool {
	ok, _ := ctx.Value(trustedProxyKey).(bool)
	return ok
}

// ClientIP is whose request this is, rather than who handed it over.
//
// Every request from the website arrives from the web server, so `RemoteAddr` is one
// address for the entire product. That was not a wasted signal, it was a wrong one: the
// per-address cap on sign-in codes counted the whole site into a single bucket of ten an
// hour, so ten requests from anyone at all left nobody able to sign in until the hour
// turned. Measured on the live database before this was written -- every verification code
// ever issued, across six different addresses and a month of them, sat in one bucket.
//
// The web server states the real address in a header. It is believed only on a request
// that already matched the shared secret to get this far, and the header is never read on
// the handful of routes that sit outside that gate.
//
// A caller that matched the secret and states nothing is not the website: the mobile app
// holds a key of its own and reaches this service directly, so for its requests the
// connection is the person. It keeps the address it always had. This deliberately does not
// fall through to nothing -- an earlier draft did, and it turned the per-address cap on
// sign-in codes off for every mobile request, which is a worse fault than the one being
// fixed and in the same family.
func ClientIP(r *http.Request) string {
	if trustedProxy(r.Context()) {
		if forwarded := strings.TrimSpace(r.Header.Get(ForwardedClientIP)); forwarded != "" {
			if ip := net.ParseIP(forwarded); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

type Principal struct{ UserID, SessionID uuid.UUID }

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
func RequestIDFrom(ctx context.Context) string { v, _ := ctx.Value(requestIDKey).(string); return v }
func VisitorID(r *http.Request) (uuid.UUID, bool) {
	v, err := uuid.Parse(strings.TrimSpace(r.Header.Get("X-Visitor-Session-ID")))
	return v, err == nil
}

func OptionalAuth(tokens *security.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := strings.TrimSpace(r.Header.Get("Authorization"))
			if h == "" {
				next.ServeHTTP(w, r)
				return
			}
			parts := strings.SplitN(h, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				httpapi.WriteError(w, httpapi.ErrInvalidToken, r.Context())
				return
			}
			u, s, err := tokens.ParseAccess(parts[1])
			if err != nil {
				httpapi.WriteError(w, httpapi.ErrInvalidToken, r.Context())
				return
			}
			// Preserve the authenticated context on the original request as well as
			// for downstream handlers. Outer observability middleware logs after the
			// handler returns and must see the resolved principal without parsing or
			// retaining the bearer token itself.
			*r = *r.WithContext(context.WithValue(r.Context(), principalKey, Principal{u, s}))
			next.ServeHTTP(w, r)
		})
	}
}

// ActiveAccount rejects access tokens whose session has been revoked or whose
// account is no longer active. JWT validity alone is intentionally insufficient
// for immediate logout and account-deactivation enforcement.
func ActiveAccount(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			var active bool
			err := db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.user_id=$2 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.status='active' AND u.deleted_at IS NULL)`, principal.SessionID, principal.UserID).Scan(&active)
			if err != nil || !active {
				httpapi.WriteError(w, httpapi.ErrInvalidToken, r.Context())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			httpapi.WriteError(w, httpapi.ErrAuthRequired, r.Context())
			return
		}
		next.ServeHTTP(w, r)
	})
}
