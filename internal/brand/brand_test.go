package brand

import "testing"

func TestCanonicalIdentity(t *testing.T) {
	if ProductName != "Boşa Gezme!" || Domain != "bosagezme.com" || WebsiteURL != "https://bosagezme.com" {
		t.Fatalf("unexpected public identity: %q %q %q", ProductName, Domain, WebsiteURL)
	}
	if DefaultEmailFrom != "no-reply@bosagezme.com" || JWTIssuer != WebsiteURL || JWTAudience != "bosagezme-clients" {
		t.Fatalf("unexpected runtime identity: %q %q %q", DefaultEmailFrom, JWTIssuer, JWTAudience)
	}
}
