package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func Hash(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
func EqualHash(a, b []byte) bool { return hmac.Equal(a, b) }

func Seal(key []byte, plaintext string) (string, error) {
	s := sha256.Sum256(key)
	block, e := aes.NewCipher(s[:])
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	out := g.Seal(n, n, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(out), nil
}
func Open(key []byte, encoded string) (string, error) {
	b, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil {
		return "", e
	}
	s := sha256.Sum256(key)
	block, e := aes.NewCipher(s[:])
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if len(b) < g.NonceSize() {
		return "", fmt.Errorf("invalid ciphertext")
	}
	p, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	return string(p), e
}

func RandomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func NumericCode(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, e := rand.Int(rand.Reader, max)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("%0*d", digits, n.Int64()), nil
}

func MatchSecret(got string, accepted []string) bool {
	matched := 0
	for _, candidate := range accepted {
		gh := sha256.Sum256([]byte(got))
		ch := sha256.Sum256([]byte(candidate))
		matched |= subtle.ConstantTimeCompare(gh[:], ch[:])
	}
	return matched == 1
}

type Claims struct {
	SessionID string `json:"sid"`
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

type Tokens struct {
	AccessToken, RefreshToken         string
	AccessExpiresAt, RefreshExpiresAt time.Time
	SessionID                         uuid.UUID
}

type TokenManager struct {
	secret                []byte
	accessTTL, refreshTTL time.Duration
	issuer, audience      string
}

func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{[]byte(secret), accessTTL, refreshTTL, brand.JWTIssuer, brand.JWTAudience}
}

func (m *TokenManager) Access(userID, sessionID uuid.UUID, now time.Time) (string, time.Time, error) {
	exp := now.Add(m.accessTTL)
	c := Claims{sessionID.String(), "access", jwt.RegisteredClaims{Subject: userID.String(), Issuer: m.issuer, Audience: jwt.ClaimStrings{m.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)), ExpiresAt: jwt.NewNumericDate(exp), ID: uuid.NewString()}}
	t, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.secret)
	return t, exp, err
}
func (m *TokenManager) ParseAccess(raw string) (uuid.UUID, uuid.UUID, error) {
	c := new(Claims)
	t, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithAudience(m.audience), jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())
	if err != nil || !t.Valid || c.TokenType != "access" {
		return uuid.Nil, uuid.Nil, fmt.Errorf("invalid token")
	}
	u, e := uuid.Parse(c.Subject)
	if e != nil {
		return uuid.Nil, uuid.Nil, e
	}
	s, e := uuid.Parse(c.SessionID)
	return u, s, e
}
func (m *TokenManager) RefreshTTL() time.Duration { return m.refreshTTL }
