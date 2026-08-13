package auth

import "testing"

func TestNormalizeEmail(t *testing.T) {
	got, e := NormalizeEmail("  Person <JOHN@example.COM> ")
	if e != nil || got != "john@example.com" {
		t.Fatalf("got %q %v", got, e)
	}
	if _, e = NormalizeEmail("not-an-email"); e == nil {
		t.Fatal("invalid email accepted")
	}
}
