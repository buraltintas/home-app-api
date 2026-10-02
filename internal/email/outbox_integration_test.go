//go:build integration

package email

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// flakySender fails the first delivery to each address it is told to fail, and remembers
// when each address was last delivered to.
type flakySender struct {
	mu        sync.Mutex
	failFirst map[string]bool
	attempts  map[string]int
	delivered map[string]time.Time
}

func (f *flakySender) Send(_ context.Context, m Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[m.To]++
	if f.failFirst[m.To] && f.attempts[m.To] == 1 {
		return "", &DeliveryError{Status: 503, Retryable: true}
	}
	f.delivered[m.To] = time.Now()
	return "rig-" + uuid.NewString(), nil
}

func (f *flakySender) sent(to string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.delivered[to]
	return ok
}

type outboxRig struct {
	db      *pgxpool.Pool
	worker  *Worker
	sender  *flakySender
	queries atomic.Int64
}

// newOutboxRig is the mail worker running against a throwaway local database, with every
// round trip it makes counted. Its rows are the only ones in the outbox.
func newOutboxRig(t *testing.T) *outboxRig {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL/PostGIS integration suite")
	}
	if parsed, e := url.Parse(raw); e != nil || !map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}[parsed.Hostname()] {
		t.Fatal("refusing to run the mail worker anywhere but a throwaway local database")
	}
	rig := &outboxRig{sender: &flakySender{failFirst: map[string]bool{}, attempts: map[string]int{}, delivered: map[string]time.Time{}}}
	activity := &database.Activity{}
	db, e := database.OpenWatched(context.Background(), raw, activity)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Close)
	rig.db = db
	clean := func() {
		if _, e := db.Exec(context.Background(), `DELETE FROM email_outbox WHERE recipient::text LIKE 'outboxrig-%'`); e != nil {
			t.Fatal(e)
		}
	}
	clean()
	t.Cleanup(clean)
	var others int
	if e := db.QueryRow(context.Background(), `SELECT count(*) FROM email_outbox WHERE status IN ('pending','failed','processing') AND available_at<'infinity'`).Scan(&others); e != nil {
		t.Fatal(e)
	}
	if others > 0 {
		t.Skip("the outbox holds mail of its own; these tests need it to themselves")
	}
	rig.worker = NewWorker(db, rig.sender, "rig@example.test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	activity.OnUse(func() { rig.queries.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = rig.worker.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return rig
}

func (rig *outboxRig) write(t *testing.T, to string) {
	t.Helper()
	if _, e := rig.db.Exec(context.Background(), `INSERT INTO email_outbox(idempotency_key,template,recipient,payload) VALUES($1,'welcome',$2,'{}')`, uuid.NewString(), to); e != nil {
		t.Fatal(e)
	}
	rig.worker.Notify()
}

func (rig *outboxRig) status(t *testing.T, to string) string {
	t.Helper()
	var status string
	if e := rig.db.QueryRow(context.Background(), `SELECT status FROM email_outbox WHERE recipient=$1`, to).Scan(&status); e != nil {
		t.Fatal(e)
	}
	return status
}

func waitFor(t *testing.T, within time.Duration, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, within)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A delivery that failed is tried again when it is due, even when other mail went out in
// between. The worker used to forget the retry the moment it sent something else: the round
// after that found nothing due yet, and an empty round waited the whole ceiling.
func TestARetryIsNotForgottenWhenOtherMailGoesOutFirst(t *testing.T) {
	rig := newOutboxRig(t)
	rig.sender.failFirst["outboxrig-a@example.test"] = true
	rig.write(t, "outboxrig-a@example.test")
	waitFor(t, 10*time.Second, "the first delivery failing", func() bool { return rig.status(t, "outboxrig-a@example.test") == "failed" })
	// The retry is a minute away; bring it to a few seconds so the test does not wait it out.
	if _, e := rig.db.Exec(context.Background(), `UPDATE email_outbox SET available_at=now()+interval '3 seconds' WHERE recipient='outboxrig-a@example.test'`); e != nil {
		t.Fatal(e)
	}
	rig.write(t, "outboxrig-b@example.test")
	waitFor(t, 10*time.Second, "the second mail going out", func() bool { return rig.sender.sent("outboxrig-b@example.test") })
	waitFor(t, 15*time.Second, "the failed mail being tried again", func() bool { return rig.sender.sent("outboxrig-a@example.test") })
}

// A row this process was never told about -- left half-sent by an instance that died -- is
// picked up when it becomes claimable, without anybody nudging the worker.
func TestARowLeftByADeadInstanceIsPickedUpWhenItIsDue(t *testing.T) {
	rig := newOutboxRig(t)
	// The worker looks once at start, finds nothing, and settles down.
	waitFor(t, 5*time.Second, "the first look", func() bool { return rig.worker.lastPoll.Load() != 0 })
	time.Sleep(200 * time.Millisecond)
	// Claimed by somebody else four minutes and fifty-seven seconds ago, so it may be taken
	// over in three seconds.
	if _, e := rig.db.Exec(context.Background(), `INSERT INTO email_outbox(idempotency_key,template,recipient,payload,status,locked_at,attempts) VALUES($1,'welcome','outboxrig-c@example.test','{}','processing',now()-interval '4 minutes 57 seconds',1)`, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	// Any other use of the database is the moment the worker looks again; here, the insert.
	rig.worker.lastPoll.Store(time.Now().Add(-emailPollPiggyback - time.Second).UnixNano())
	rig.worker.DatabaseInUse()
	waitFor(t, 15*time.Second, "the orphaned row going out", func() bool { return rig.sender.sent("outboxrig-c@example.test") })
}

// An empty outbox is asked about once and then left alone.
func TestAnEmptyOutboxIsAskedAboutOnceAndLeftAlone(t *testing.T) {
	rig := newOutboxRig(t)
	waitFor(t, 5*time.Second, "the first look", func() bool { return rig.worker.lastPoll.Load() != 0 })
	time.Sleep(300 * time.Millisecond)
	if n := rig.queries.Load(); n != 1 {
		t.Fatalf("an empty outbox took %d round trips to look at, want one", n)
	}
	time.Sleep(3 * time.Second)
	if n := rig.queries.Load(); n != 1 {
		t.Fatalf("an empty outbox was asked about again (%d round trips)", n)
	}
}
