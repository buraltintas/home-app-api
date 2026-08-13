package middleware

import (
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
