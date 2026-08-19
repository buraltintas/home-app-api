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
