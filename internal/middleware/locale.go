package middleware

import (
	"net/http"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RequestLocale(defaultLocale i18n.Locale) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locale, explicit := i18n.ResolveRequest(r.Header.Get("X-Locale"), r.Header.Get("Accept-Language"), defaultLocale)
			ctx := i18n.WithLocale(r.Context(), locale)
			if explicit {
				ctx = i18n.WithExplicitLocale(ctx, locale)
			}
			*r = *r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

// UserLocale applies the authenticated private preference only when the client
// did not explicitly select X-Locale. It intentionally falls back to the
// already-resolved Accept-Language/default locale on lookup failure.
func UserLocale(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if i18n.HasExplicitLocale(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			if principal, ok := PrincipalFrom(r.Context()); ok {
				var raw string
				if err := db.QueryRow(r.Context(), `SELECT preferred_locale FROM users WHERE id=$1 AND deleted_at IS NULL`, principal.UserID).Scan(&raw); err == nil {
					if locale, supported := i18n.Normalize(raw); supported {
						*r = *r.WithContext(i18n.WithLocale(r.Context(), locale))
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
