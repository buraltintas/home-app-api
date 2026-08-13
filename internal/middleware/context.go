package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
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
				httpapi.WriteError(w, httpapi.ErrInvalidToken)
				return
			}
			u, s, err := tokens.ParseAccess(parts[1])
			if err != nil {
				httpapi.WriteError(w, httpapi.ErrInvalidToken)
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
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			httpapi.WriteError(w, httpapi.ErrAuthRequired)
			return
		}
		next.ServeHTTP(w, r)
	})
}
