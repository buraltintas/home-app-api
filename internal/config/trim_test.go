package config

import (
	"os"
	"strings"
	"testing"
)

// A secret created with `echo` carries a trailing newline. Go refuses to put one in a
// header value, so an API key with a stray newline fails before the request is sent and
// looks exactly like a network outage. Production ran that way for a long time.
func TestSecretsAreTrimmedOfSurroundingWhitespace(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-example-value\n")
	if got := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); strings.ContainsAny(got, "\r\n \t") {
		t.Fatalf("key still carries whitespace: %q", got)
	}
	t.Setenv("EMAIL_FROM", "  spaced@example.com  ")
	if got := env("EMAIL_FROM", ""); got != "spaced@example.com" {
		t.Fatalf("env() = %q, want the trimmed value", got)
	}
	// An entry that is only whitespace is not a value; the fallback has to win.
	t.Setenv("EMAIL_FROM", "   ")
	if got := env("EMAIL_FROM", "fallback"); got != "fallback" {
		t.Fatalf("whitespace-only value = %q, want the fallback", got)
	}
}

// The production key was stored as the whole "NAME=value" line, so every request was
// rejected with a 401 that only appeared in the logs while search quietly ran on the
// deterministic parser. Refusing to start is louder than a log nobody reads.
func TestKeyThatCarriesItsVariableNameIsRejected(t *testing.T) {
	base := func() {
		t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("BFF_SECRETS", "secret")
		t.Setenv("ACCESS_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("OTP_HASH_SECRET", "0123456789abcdef0123456789abcdef")
	}
	base()
	t.Setenv("OPENAI_API_KEY", "OPENAI_API_KEY=sk-real-looking-value")
	if _, e := Load(); e == nil {
		t.Fatal("a key carrying its own variable name was accepted")
	}
	base()
	t.Setenv("OPENAI_API_KEY", "sk-real-looking-value")
	if _, e := Load(); e != nil {
		t.Fatalf("a well-formed key was rejected: %v", e)
	}
}
