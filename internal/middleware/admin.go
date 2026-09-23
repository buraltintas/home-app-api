package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminEmailKeyType struct{}

var adminEmailKey adminEmailKeyType

// AdminEmailFrom returns the signed-in administrator's address. It is present only on
// requests that already passed RequireAdmin, and exists so that an audit row can record
// who acted without re-reading the account.
func AdminEmailFrom(ctx context.Context) string {
	v, _ := ctx.Value(adminEmailKey).(string)
	return v
}

// AdminAllowlist is the set of addresses a deployment names as administrators, prepared for
// comparison.
//
// It is a type rather than a loop written twice because two places now need the same answer
// -- the middleware that guards the admin routes, and the panel's own sign-in, which must
// not post a code to an address that could never use it. Two copies of "is this an
// administrator" is one copy too many: the one that drifts is the way in.
type AdminAllowlist []string

// NewAdminAllowlist normalises the configured entries and drops the empty ones. An empty
// result means nobody, never everybody.
func NewAdminAllowlist(entries []string) AdminAllowlist {
	out := make(AdminAllowlist, 0, len(entries))
	for _, entry := range entries {
		if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// Has reports whether an address is on the list. The comparison is constant time: the
// address is not a secret in the way a password is, but timing that reveals how much of an
// allowlisted address a guess matched is a needless thing to hand out. Every entry is
// compared, so the answer takes the same work whether the match is first or last or absent.
func (a AdminAllowlist) Has(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(a) == 0 {
		return false
	}
	match := 0
	for _, entry := range a {
		match |= subtle.ConstantTimeCompare([]byte(entry), []byte(email))
	}
	return match == 1
}

// RequireAdmin authorises the small allowlist of addresses permitted to use the admin
// surface. It deliberately runs after RequireAuth: administration reuses the ordinary
// email sign-in rather than introducing a second credential to protect.
//
// A caller who is not on the list is answered exactly as if the route did not exist. A 403
// would confirm that it does, which is a free hint to anyone probing for it.
func RequireAdmin(db *pgxpool.Pool, allowed []string) func(http.Handler) http.Handler {
	normalised := NewAdminAllowlist(allowed)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			notFound := httpapi.E(404, "NOT_FOUND", "Not found")
			// An empty allowlist closes the surface completely. A deployment that forgot
			// to configure it must not fall open.
			if len(normalised) == 0 {
				httpapi.WriteError(w, notFound, r.Context())
				return
			}
			principal, ok := PrincipalFrom(r.Context())
			if !ok {
				httpapi.WriteError(w, notFound, r.Context())
				return
			}
			var email string
			if e := db.QueryRow(r.Context(), `SELECT primary_email::text FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL`, principal.UserID).Scan(&email); e != nil {
				httpapi.WriteError(w, notFound, r.Context())
				return
			}
			email = strings.ToLower(strings.TrimSpace(email))
			if !normalised.Has(email) {
				httpapi.WriteError(w, notFound, r.Context())
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminEmailKey, email)))
		})
	}
}
