package feedback

import "testing"

func TestValidate(t *testing.T) {
	// An unspecified kind is the common case: somebody types and sends.
	kind, message, email, err := validate(Input{Message: "  Arama sonuçları çok iyi  "})
	if err != nil || kind != "other" || message != "Arama sonuçları çok iyi" || email != nil {
		t.Fatalf("unexpected: %q %q %v %v", kind, message, email, err)
	}
	if _, _, _, err = validate(Input{Kind: "spam", Message: "hello there"}); err == nil {
		t.Fatal("expected an unknown kind to be rejected")
	}
	// Four characters is a slip of the hand, not a message. The bound matches the table's
	// own constraint so the sender gets an explanation rather than a 500.
	if _, _, _, err = validate(Input{Message: "  ab  "}); err == nil {
		t.Fatal("expected a too-short message to be rejected")
	}
	if _, _, _, err = validate(Input{Message: "this is long enough", ContactEmail: "not-an-address"}); err == nil {
		t.Fatal("expected an invalid contact address to be rejected")
	}
	_, _, email, err = validate(Input{Kind: "problem", Message: "photos do not load", ContactEmail: " a@b.com "})
	if err != nil || email == nil || *email != "a@b.com" {
		t.Fatalf("expected a trimmed address, got %v %v", email, err)
	}
}
