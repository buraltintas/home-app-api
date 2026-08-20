package email

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderFeedback(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{
		"kind": "problem", "message": "Fotoğraflar yüklenmiyor.\nİkinci satır.",
		"contact_email": "a@b.com", "author": "", "locale": "tr",
	})
	m, err := renderFeedback(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Subject, "Sorun") {
		t.Fatalf("the subject should name the kind in the operator's language: %q", m.Subject)
	}
	// The point of this notice is reading the message without opening anything else.
	if !strings.Contains(m.Text, "Fotoğraflar yüklenmiyor.") || !strings.Contains(m.HTML, "Fotoğraflar yüklenmiyor.") {
		t.Fatal("expected the message itself in both parts")
	}
	if !strings.Contains(m.Text, "a@b.com") {
		t.Fatal("expected the reply address to travel with it")
	}
	// A sender who gave neither a name nor an address is anonymous, not blank.
	bare, _ := json.Marshal(map[string]string{"kind": "other", "message": "merhaba dünya"})
	m, err = renderFeedback(bare)
	if err != nil || !strings.Contains(m.Text, "anonim") {
		t.Fatalf("expected an anonymous sender to be named as such: %v %q", err, m.Text)
	}
	// Markup in a message must not become markup in our inbox.
	esc, _ := json.Marshal(map[string]string{"kind": "other", "message": "<script>alert(1)</script>"})
	m, _ = renderFeedback(esc)
	if strings.Contains(m.HTML, "<script>") {
		t.Fatal("expected the message to be escaped in the HTML part")
	}
}
