package email

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// An empty outbox is not asked about again until the ceiling. The climb from a second used
// to put six extra queries into the first two hours of every instance and after every mail
// sent, and on a quiet night each was a separate wake of the database.
func TestAnEmptyOutboxWaitsTheWholeCeiling(t *testing.T) {
	if got := nextPoll(0, false, 0); got != emailPollIdle {
		t.Fatalf("an empty poll waits %s, want the ceiling %s", got, emailPollIdle)
	}
}

// The two branches that were right stay as they were: a queue that had mail is drained, and
// a retry this process scheduled is waited for exactly.
func TestBusyAndDueBranchesAreUnchanged(t *testing.T) {
	if got := nextPoll(0, true, 0); got != emailPollBusy {
		t.Fatalf("after sending, wait %s, want %s", got, emailPollBusy)
	}
	if got := nextPoll(4*time.Minute, false, 0); got != 4*time.Minute {
		t.Fatalf("a due retry should be waited for exactly, got %s", got)
	}
	if got := nextPoll(4*time.Minute, true, 0); got != 4*time.Minute {
		t.Fatalf("a due retry wins over a busy queue, got %s", got)
	}
}

// A round that could not ask the outbox says nothing about whether it is empty, and the
// sign-in code somebody is waiting for may be in it. It retries soon and backs off.
func TestAFailedRoundBacksOffFromASecond(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := nextPoll(0, false, i+1); got != w {
			t.Fatalf("failure %d waits %s, want %s", i+1, got, w)
		}
	}
	if got := nextPoll(0, false, 64); got != emailPollIdle {
		t.Fatalf("a long run of failures is capped at the ceiling, got %s", got)
	}
}

// A query made for some other reason is a free moment to look: the database is already up.
// Only when the last look is old, so a busy database does not turn into a busy outbox poll.
func TestDatabaseInUseLooksOnlyWhenTheLastLookIsOld(t *testing.T) {
	w := NewWorker(nil, nil, "", nil, nil)
	w.DatabaseInUse()
	if len(w.wake) != 0 {
		t.Fatal("a worker that has not polled yet is about to; it needs no nudge")
	}
	w.lastPoll.Store(time.Now().Add(-time.Minute).UnixNano())
	w.DatabaseInUse()
	if len(w.wake) != 0 {
		t.Fatal("looked again a minute after the last look")
	}
	w.lastPoll.Store(time.Now().Add(-emailPollPiggyback - time.Second).UnixNano())
	w.DatabaseInUse()
	if len(w.wake) != 1 {
		t.Fatal("did not look again when the database was awake and the last look was old")
	}
	var nobody *Worker
	nobody.DatabaseInUse()
}

// The ceiling is only safe because nothing that writes mail waits for it: every writer tells
// the worker after its commit. A new writer that forgot would leave somebody's sign-in code
// sitting in the outbox for six hours, so this reads the source for every INSERT INTO
// email_outbox and insists the same file says so.
func TestEveryOutboxWriterNotifiesTheWorker(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	insert := regexp.MustCompile(`(?i)INSERT\s+INTO\s+email_outbox`)
	writers := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !insert.Match(source) {
			return nil
		}
		writers++
		if !strings.Contains(string(source), "notifyMail()") {
			t.Errorf("%s writes to email_outbox but never calls notifyMail() after its commit", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if writers == 0 {
		t.Fatal("found no outbox writer at all; the scan is looking in the wrong place")
	}
	main, err := os.ReadFile(filepath.Join(root, "cmd", "api", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(main), "SetMailNotifier(emailWorker.Notify)"); got < 2 {
		t.Fatalf("the API wires %d writers to the worker, want the auth and feedback services both", got)
	}
}
