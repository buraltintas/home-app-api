package store

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

// testCatalog is a Catalog whose database is a counter, whose clock is a variable and whose
// idea of whether the database is awake is a switch.
type testCatalog struct {
	*Catalog
	reads atomic.Int32
	fail  atomic.Bool
	gate  chan struct{}
	clock time.Time
	up    atomic.Bool
	mu    sync.Mutex
}

func newTestCatalog(t *testing.T) *testCatalog {
	t.Helper()
	tc := &testCatalog{clock: time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC)}
	f := newFixture()
	f.add(1, "İzmir", 38.4, 27.1, "lighting")
	f.add(2, "İzmir", 38.41, 27.1, "lighting")
	tc.Catalog = newCatalog(func(ctx context.Context) (*Snapshot, error) {
		tc.reads.Add(1)
		if tc.gate != nil {
			<-tc.gate
		}
		if tc.fail.Load() {
			return nil, errors.New("database unreachable")
		}
		return f.build(), nil
	}, tc.up.Load, 6*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tc.Catalog.now = tc.now
	if e := tc.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
	tc.reads.Store(0)
	return tc
}

func (tc *testCatalog) now() time.Time {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.clock
}

func (tc *testCatalog) advance(d time.Duration) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.clock = tc.clock.Add(d)
}

// The point of the whole thing: a held copy answers without asking the database anything,
// however many readers ask.
func TestAHeldCopyAnswersWithoutTouchingTheDatabase(t *testing.T) {
	tc := newTestCatalog(t)
	for i := 0; i < 1000; i++ {
		if tc.Current(context.Background()) == nil {
			t.Fatal("no copy to answer from")
		}
	}
	tc.advance(59 * time.Minute)
	tc.up.Store(true)
	tc.Current(context.Background())
	if n := tc.reads.Load(); n != 0 {
		t.Fatalf("the database was read %d times for answers already held", n)
	}
}

// An hour-old copy is replaced only while the database is awake anyway. Asleep, it is left
// alone until the maximum age, which is the one read allowed to wake it.
func TestAnOldCopyIsReadAgainOnlyOnAnAwakeDatabase(t *testing.T) {
	tc := newTestCatalog(t)
	tc.advance(2 * time.Hour)
	tc.Current(context.Background())
	if n := tc.reads.Load(); n != 0 {
		t.Fatalf("a two-hour-old copy woke a sleeping database (%d reads)", n)
	}
	tc.up.Store(true)
	snap := tc.Current(context.Background())
	if n := tc.reads.Load(); n != 1 || snap.LoadedAt() != tc.now() {
		t.Fatalf("an awake database was not used to refresh: %d reads, copy from %s", n, snap.LoadedAt())
	}
	tc.up.Store(false)
	tc.advance(6 * time.Hour)
	tc.Current(context.Background())
	if n := tc.reads.Load(); n != 2 {
		t.Fatalf("a copy at its maximum age was not read again (%d reads)", n)
	}
}

// A write made here must be visible here on the next page: until the catalogue has been read
// again, nothing is answered from the old copy.
func TestAWriteHereSendsReadsToTheDatabaseUntilReadAgain(t *testing.T) {
	tc := newTestCatalog(t)
	before := tc.Current(context.Background())
	tc.advance(catalogReloadEvery)
	tc.Invalidate()
	after := tc.Current(context.Background())
	if n := tc.reads.Load(); n != 1 || after == nil || after == before {
		t.Fatalf("the write was not followed by a fresh read: %d reads", n)
	}
	tc.fail.Store(true)
	tc.Invalidate()
	if got := tc.Current(context.Background()); got != nil {
		t.Fatal("an out-of-date copy answered after a write it does not contain")
	}
}

// A write that lands while a read is under way may not be in what that read returns, so the
// copy it produces is not trusted for it.
func TestAWriteDuringAReadIsNotLost(t *testing.T) {
	tc := newTestCatalog(t)
	tc.gate = make(chan struct{})
	tc.advance(catalogReloadEvery)
	tc.Invalidate()
	done := make(chan *Snapshot)
	go func() { done <- tc.Current(context.Background()) }()
	for tc.reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	// Meanwhile everybody else falls through rather than waiting or reading the old copy.
	if got := tc.Current(context.Background()); got != nil {
		t.Fatal("a reader was answered from the copy the write made stale")
	}
	tc.Invalidate()
	close(tc.gate)
	if got := <-done; got != nil {
		t.Fatal("a copy read before the second write was trusted after it")
	}
	tc.advance(catalogReloadEvery)
	if got := tc.Current(context.Background()); got == nil || tc.reads.Load() != 2 {
		t.Fatal("the next reader did not read the catalogue again")
	}
}

// A favourite changes one page. That page goes to the database until the next read; every
// other answer stays in memory.
func TestAChangeToOneShopLeavesTheRestInMemory(t *testing.T) {
	tc := newTestCatalog(t)
	tc.InvalidateStore(storeID(1))
	snap := tc.Current(context.Background())
	if _, ok := snap.Store(storeID(1).String(), i18n.LocaleTR); ok {
		t.Fatal("the changed shop's page was answered from before the change")
	}
	if _, ok := snap.Store(storeID(2).String(), i18n.LocaleTR); !ok {
		t.Fatal("another shop's page was sent to the database")
	}
	if _, ok := snap.Nearby(storeID(1).String(), 6); !ok {
		t.Fatal("the changed shop's neighbours do not depend on its favourites")
	}
	if n := tc.reads.Load(); n != 0 {
		t.Fatalf("one page changing read the whole catalogue (%d reads)", n)
	}
	tc.advance(catalogReloadEvery)
	tc.Invalidate()
	if _, ok := tc.Current(context.Background()).Store(storeID(1).String(), i18n.LocaleTR); !ok {
		t.Fatal("a copy read after the change still treats the page as changed")
	}
}

// The database being unreachable at the moment a refresh is due changes nothing a reader can
// see: the old copy goes on answering, and the database is not asked again for a minute.
func TestAnUnreachableDatabaseKeepsTheOldCopy(t *testing.T) {
	tc := newTestCatalog(t)
	held := tc.Current(context.Background())
	tc.fail.Store(true)
	tc.up.Store(true)
	tc.advance(2 * time.Hour)
	for i := 0; i < 50; i++ {
		if got := tc.Current(context.Background()); got != held {
			t.Fatal("a failed refresh dropped the copy that was answering")
		}
	}
	if n := tc.reads.Load(); n != 1 {
		t.Fatalf("an unreachable database was asked %d times in a minute", n)
	}
	tc.fail.Store(false)
	tc.advance(catalogRetryAfter)
	if got := tc.Current(context.Background()); got == held || tc.reads.Load() != 2 {
		t.Fatal("the copy was not replaced once the database answered again")
	}
}

// Readers never wait for a read somebody else is doing: they keep the copy they have, or,
// once it is past its maximum age, go to the database.
func TestReadersDoNotWaitForARefresh(t *testing.T) {
	tc := newTestCatalog(t)
	held := tc.Current(context.Background())
	tc.gate = make(chan struct{})
	tc.up.Store(true)
	tc.advance(2 * time.Hour)
	done := make(chan struct{})
	go func() { tc.Current(context.Background()); close(done) }()
	for tc.reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if got := tc.Current(context.Background()); got != held || time.Since(start) > 100*time.Millisecond {
		t.Fatal("a reader waited on somebody else's refresh")
	}
	tc.gate <- struct{}{}
	<-done

	tc.up.Store(false)
	tc.advance(6 * time.Hour)
	go tc.Current(context.Background())
	for tc.reads.Load() == 1 {
		time.Sleep(time.Millisecond)
	}
	start = time.Now()
	if got := tc.Current(context.Background()); got != nil || time.Since(start) > 100*time.Millisecond {
		t.Fatal("a reader waited on a refresh, or was answered from a copy past its maximum age")
	}
	close(tc.gate)
}

// A copy that could not be replaced by its maximum age is not answered from: the most a page
// may lag is the maximum age, whatever is wrong with reading the catalogue. The database
// answers instead, and is asked for a new copy once a minute until it gives one.
func TestACopyPastItsMaximumAgeIsNeverServed(t *testing.T) {
	tc := newTestCatalog(t)
	tc.fail.Store(true)
	tc.advance(6 * time.Hour)
	for i := 0; i < 50; i++ {
		if got := tc.Current(context.Background()); got != nil {
			t.Fatal("a copy past its maximum age answered because it could not be replaced")
		}
	}
	if n := tc.reads.Load(); n != 1 {
		t.Fatalf("the catalogue was read %d times in a minute", n)
	}
	tc.fail.Store(false)
	tc.advance(catalogRetryAfter)
	if got := tc.Current(context.Background()); got == nil || got.LoadedAt() != tc.now() {
		t.Fatal("the copy was not replaced once the catalogue could be read again")
	}
}

// An administrator making edit after edit has the catalogue read at most once a minute; in
// between, the database answers, so every edit still shows on the next page.
func TestWritesInQuickSuccessionReadTheCatalogueOnceAMinute(t *testing.T) {
	tc := newTestCatalog(t)
	for i := 0; i < 10; i++ {
		tc.Invalidate()
		if got := tc.Current(context.Background()); got != nil {
			t.Fatal("a copy from before a write answered after it")
		}
		tc.advance(5 * time.Second)
	}
	if n := tc.reads.Load(); n != 0 {
		t.Fatalf("ten edits in under a minute read the catalogue %d times", n)
	}
	tc.advance(catalogReloadEvery)
	if got := tc.Current(context.Background()); got == nil || tc.reads.Load() != 1 {
		t.Fatal("the catalogue was not read again a minute after the last read")
	}
}

// A copy that could not be read at startup is not tried again by the first request: that
// request would wait out the same failure. It waits for the retry interval like any other.
func TestAFailedStartupReadIsNotRetriedAtOnce(t *testing.T) {
	tc := newTestCatalog(t)
	failing := newCatalog(func(context.Context) (*Snapshot, error) {
		tc.reads.Add(1)
		return nil, errors.New("database unreachable")
	}, tc.up.Load, 6*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	failing.now = tc.now
	if failing.Load(context.Background()) == nil {
		t.Fatal("a failed read reported success")
	}
	if failing.Current(context.Background()) != nil || tc.reads.Load() != 1 {
		t.Fatalf("the first request read again straight after a failed startup (%d reads)", tc.reads.Load())
	}
	tc.advance(catalogRetryAfter)
	failing.Current(context.Background())
	if n := tc.reads.Load(); n != 2 {
		t.Fatalf("the catalogue was not tried again after the retry interval (%d reads)", n)
	}
}

// Two writes at once may take their numbers in one order and record them in the other. The
// later number must win, or a copy read between them would be trusted with the later write.
func TestAnEarlierWriteRecordedLastDoesNotHideALaterOne(t *testing.T) {
	tc := newTestCatalog(t)
	tc.stale.Store(10)
	tc.dirty.Store(storeID(1), uint64(10))
	tc.Invalidate()
	tc.InvalidateStore(storeID(1))
	if got := tc.stale.Load(); got != 10 {
		t.Fatalf("an earlier write lowered the mark from 10 to %d", got)
	}
	if got, _ := tc.dirty.Load(storeID(1)); got.(uint64) != 10 {
		t.Fatalf("an earlier change to a shop lowered its mark from 10 to %d", got)
	}
}

// A catalogue that has never been read answers nothing; the database does, as it did before.
func TestNoCopyMeansTheDatabaseAnswers(t *testing.T) {
	var c *Catalog
	if c.Current(context.Background()) != nil {
		t.Fatal("a missing catalogue answered")
	}
	c.Invalidate()
	c.InvalidateStore(storeID(1))
	tc := newTestCatalog(t)
	tc.fail.Store(true)
	tc.Invalidate()
	tc.advance(catalogRetryAfter + time.Second)
	if tc.Current(context.Background()) != nil {
		t.Fatal("answered without a copy")
	}
}
