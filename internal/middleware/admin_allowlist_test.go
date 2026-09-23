package middleware

import "testing"

func TestAdminAllowlistMatchesRegardlessOfCaseAndSpacing(t *testing.T) {
	list := NewAdminAllowlist([]string{"  Boss@Example.COM ", "", "second@example.com"})
	if len(list) != 2 {
		t.Fatalf("entries=%d", len(list))
	}
	for _, address := range []string{"boss@example.com", " BOSS@example.com ", "second@example.com"} {
		if !list.Has(address) {
			t.Fatalf("%q should be allowed", address)
		}
	}
}

func TestAdminAllowlistRefusesEverythingElse(t *testing.T) {
	list := NewAdminAllowlist([]string{"boss@example.com"})
	// A prefix, a suffix and a stranger: none of them is the address.
	for _, address := range []string{"boss@example.co", "boss@example.comm", "someone@example.com", ""} {
		if list.Has(address) {
			t.Fatalf("%q should not be allowed", address)
		}
	}
}

// An unconfigured deployment closes the door rather than opening it.
func TestEmptyAdminAllowlistAllowsNobody(t *testing.T) {
	for _, list := range []AdminAllowlist{NewAdminAllowlist(nil), NewAdminAllowlist([]string{"", "   "})} {
		if list.Has("boss@example.com") {
			t.Fatal("empty allowlist allowed an address")
		}
	}
}
