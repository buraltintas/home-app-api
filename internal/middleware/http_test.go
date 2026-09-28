package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
)

func TestBFFRejectsMissingAndInvalidSecret(t *testing.T) {
	h := RequestID(BFF([]string{"valid-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	for _, secret := range []string{"", "wrong"} {
		r := httptest.NewRequest("GET", "/v1/feed", nil)
		if secret != "" {
			r.Header.Set("X-BFF-Secret", secret)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("secret %q status=%d", secret, w.Code)
		}
	}
}

func TestLocalizedAuthRequiredKeepsStableCode(t *testing.T) {
	handler := RequestLocale(i18n.LocaleTR)(RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	for _, locale := range i18n.Supported() {
		req := httptest.NewRequest(http.MethodPost, "/v1/protected", nil)
		req.Header.Set("X-Locale", string(locale))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		var payload struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code != "AUTH_REQUIRED" || payload.Error.Message != i18n.Translate(locale, "AUTH_REQUIRED") {
			t.Fatalf("locale=%s payload=%+v", locale, payload)
		}
	}
}
func TestBFFAllowsAnonymousBrowse(t *testing.T) {
	h := BFF([]string{"valid-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); ok {
			t.Fatal("unexpected principal")
		}
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("GET", "/v1/feed", nil)
	r.Header.Set("X-BFF-Secret", "valid-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestBFFAllowsEveryConfiguredRotationSecret(t *testing.T) {
	h := BFF([]string{"current-secret", "previous-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, secret := range []string{"current-secret", "previous-secret"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/feed", nil)
		r.Header.Set("X-BFF-Secret", secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("configured secret %q status=%d", secret, w.Code)
		}
	}
}
func TestOptionalAndRequiredAuth(t *testing.T) {
	m := security.NewTokenManager("an-access-secret-that-is-at-least-32-bytes", time.Minute, time.Hour)
	raw, _, _ := m.Access(uuid.New(), uuid.New(), time.Now())
	protected := OptionalAuth(m)(RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	anon := httptest.NewRequest("POST", "/v1/posts", nil)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, anon)
	if w.Code != 401 {
		t.Fatalf("anonymous status=%d", w.Code)
	}
	valid := httptest.NewRequest("POST", "/v1/posts", nil)
	valid.Header.Set("Authorization", "Bearer "+raw)
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, valid)
	if w.Code != 204 {
		t.Fatalf("valid status=%d", w.Code)
	}
	if _, ok := PrincipalFrom(valid.Context()); !ok {
		t.Fatal("resolved principal was not preserved for outer observability middleware")
	}
	invalid := httptest.NewRequest("GET", "/v1/feed", nil)
	invalid.Header.Set("Authorization", "Bearer invalid")
	w = httptest.NewRecorder()
	OptionalAuth(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, invalid)
	if w.Code != 401 {
		t.Fatalf("invalid optional token silently became anonymous: %d", w.Code)
	}
}

// Every request reaches this service from the web server, so keying the limit on the
// delivering address put the entire product in one bucket. A results page prefetching two
// dozen store links emptied it, and the stores it had just listed came back as 429s.
func TestTheLimitCountsThePersonNotTheDeliveringServer(t *testing.T) {
	visitor := uuid.New().String()
	other := uuid.New().String()

	shared := httptest.NewRequest(http.MethodGet, "/v1/stores/x", nil)
	shared.RemoteAddr = "10.0.0.1:4242"
	shared = WithTrustedProxy(shared)
	shared.Header.Set(ForwardedClientIP, "203.0.113.9")
	mine := shared.Clone(shared.Context())
	mine.Header.Set("X-Visitor-Session-ID", visitor)
	theirs := shared.Clone(shared.Context())
	theirs.Header.Set("X-Visitor-Session-ID", other)

	if sameKeys(limiterKeys(mine), limiterKeys(theirs)) {
		t.Fatal("two browsing sessions from the same web server share every bucket")
	}
	if !has(limiterKeys(mine), "v:"+visitor) {
		t.Errorf("visitor bucket missing: %v", limiterKeys(mine))
	}
	// And the address as well, so that rotating the session id does not buy a fresh
	// allowance -- the whole reason the session id alone was not enough.
	if !has(limiterKeys(mine), "a:203.0.113.9") || !has(limiterKeys(theirs), "a:203.0.113.9") {
		t.Errorf("two sessions from one address do not share an address bucket: %v / %v", limiterKeys(mine), limiterKeys(theirs))
	}

	signedIn := shared.Clone(context.WithValue(shared.Context(), principalKey, Principal{UserID: uuid.New()}))
	if sameKeys(limiterKeys(signedIn), limiterKeys(shared)) {
		t.Error("a signed-in request should be counted against the account")
	}
}

// The forwarded address is a claim, and a claim is only worth the proof behind it. On a
// route that never matched the shared secret there is no proof, so the header is not read
// at all -- believing it there would let anyone hand us whichever address they liked.
func TestAForwardedAddressIsIgnoredWithoutTheSharedSecret(t *testing.T) {
	forged := httptest.NewRequest(http.MethodGet, "/health", nil)
	forged.RemoteAddr = "10.0.0.1:4242"
	forged.Header.Set(ForwardedClientIP, "203.0.113.9")
	if got := ClientIP(forged); got != "10.0.0.1" {
		t.Fatalf("an unproven forwarded address was believed: %q", got)
	}

	proven := WithTrustedProxy(forged.Clone(forged.Context()))
	if got := ClientIP(proven); got != "203.0.113.9" {
		t.Fatalf("the web server's own statement was not used: %q", got)
	}

	// Nonsense in the header is not a licence to fall back to the delivering address:
	// that address is the one bucket this whole mechanism exists to stop using.
	silent := WithTrustedProxy(httptest.NewRequest(http.MethodGet, "/v1/feed", nil))
	silent.RemoteAddr = "10.0.0.1:4242"
	silent.Header.Set(ForwardedClientIP, "not-an-address")
	if got := ClientIP(silent); got != "" {
		t.Fatalf("an unreadable forwarded address fell back to the web server: %q", got)
	}
}

func has(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
