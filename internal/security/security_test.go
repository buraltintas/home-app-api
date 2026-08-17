package security

import (
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/google/uuid"
)

func TestMatchSecretRotation(t *testing.T) {
	accepted := []string{"old-secret", "new-secret"}
	if !MatchSecret("new-secret", accepted) || !MatchSecret("old-secret", accepted) {
		t.Fatal("rotation secrets should be accepted")
	}
	if MatchSecret("wrong", accepted) || MatchSecret("", accepted) {
		t.Fatal("invalid secret accepted")
	}
}
func TestSealAndHashDoNotPersistPlaintext(t *testing.T) {
	key := []byte("a-key-that-is-definitely-more-than-32-bytes")
	sealed, e := Seal(key, "483921")
	if e != nil {
		t.Fatal(e)
	}
	if sealed == "483921" {
		t.Fatal("ciphertext equals plaintext")
	}
	plain, e := Open(key, sealed)
	if e != nil || plain != "483921" {
		t.Fatalf("round trip failed: %q %v", plain, e)
	}
	if EqualHash(Hash(key, "483921"), Hash(key, "483922")) {
		t.Fatal("different codes matched")
	}
}
func TestAccessTokenValidation(t *testing.T) {
	m := NewTokenManager("an-access-secret-that-is-at-least-32-bytes", time.Minute, time.Hour)
	if m.issuer != brand.JWTIssuer || m.audience != brand.JWTAudience {
		t.Fatalf("unexpected token identity: issuer=%q audience=%q", m.issuer, m.audience)
	}
	u, s := uuid.New(), uuid.New()
	raw, _, e := m.Access(u, s, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	gotU, gotS, e := m.ParseAccess(raw)
	if e != nil || gotU != u || gotS != s {
		t.Fatalf("unexpected claims: %v %v %v", gotU, gotS, e)
	}
	if _, _, e = NewTokenManager("another-access-secret-at-least-32-bytes", time.Minute, time.Hour).ParseAccess(raw); e == nil {
		t.Fatal("token verified with wrong key")
	}
}
func TestNumericCodeIsSixDigits(t *testing.T) {
	for i := 0; i < 100; i++ {
		c, e := NumericCode(6)
		if e != nil || len(c) != 6 {
			t.Fatalf("bad code %q: %v", c, e)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Fatalf("non-numeric code %q", c)
			}
		}
	}
}
