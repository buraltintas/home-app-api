package email

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSenderCreatesPrivateDevelopmentMailboxMessage(t *testing.T) {
	dir := t.TempDir()
	id, err := (FileSender{Dir: dir}).Send(context.Background(), Message{To: "dev@example.test", Subject: "OTP", Text: "Kod: 123456"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".eml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "dev@example.test") || !strings.Contains(string(body), "123456") {
		t.Fatalf("mailbox body=%q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mailbox permissions=%o", info.Mode().Perm())
	}
}
