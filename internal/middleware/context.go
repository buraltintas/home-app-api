package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	principalKey contextKey = "principal"
	requestIDKey contextKey = "request_id"
	visitorKey   contextKey = "visitor"
)

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
