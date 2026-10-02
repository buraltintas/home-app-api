package changes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/changes"
	"github.com/burakaltintas/home-app-api/internal/changes/changestest"
	"github.com/google/uuid"
)

const name = "_cache/test/catalog-changes.json"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuffer is a log both a test and a marker may write and read at once.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) count(fragment string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Count(s.b.String(), fragment)
}

// instance is one API process as far as the marker is concerned: its marker, and what it
// was told to drop.
type instance struct {
	*changes.Marker
	log    *syncBuffer
	mu     sync.Mutex
	forgot []changes.Change
}

const interval = 30 * time.Second

func newInstance(objects changes.Objects, clk *clock) *instance {
	log := &syncBuffer{}
	in := &instance{log: log, Marker: changes.New(objects, name, interval, slog.New(slog.NewTextHandler(log, nil)))}
	in.SetClock(clk.now)
	return in
}

// read is an anonymous catalogue read arriving at this instance: it looks, if a look is due,
// and returns what that made it drop.
func (in *instance) read() []changes.Change {
	in.mu.Lock()
	in.forgot = nil
	in.mu.Unlock()
	in.Check(context.Background(), func(c changes.Change) {
		in.mu.Lock()
		defer in.mu.Unlock()
		in.forgot = append(in.forgot, c)
	})
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.forgot
}

func start() *clock { return &clock{t: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)} }

func same(a, b []uuid.UUID) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	slices.SortFunc(b, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	return slices.Equal(a, b)
}

// The case the marker exists for: a review written through one instance reaches the other's
// next look -- exactly that shop, and the lists, and the highlights -- and nothing sooner
// than its interval allows, so the look is one metadata read per interval and no more.
func TestAWriteOnOneInstanceReachesTheOtherWithinTheInterval(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	if got := b.read(); got != nil {
		t.Fatalf("an empty bucket was news: %+v", got)
	}
	shop := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{shop}, Full: true, Reviews: true})
	looked := objects.Generations.Load()
	if got := b.read(); got != nil || objects.Generations.Load() != looked {
		t.Fatalf("a look was taken before its interval: %+v", got)
	}
	clk.advance(interval)
	got := b.read()
	if len(got) != 1 || !same(got[0].Stores, []uuid.UUID{shop}) || !got[0].Full || !got[0].Reviews || got[0].AllPages {
		t.Fatalf("the other instance was told %+v", got)
	}
	// Nothing new: one metadata read, not the object.
	reads := objects.Reads.Load()
	clk.advance(interval)
	if got := b.read(); got != nil || objects.Reads.Load() != reads {
		t.Fatalf("an unchanged marker was read again or told again: %+v", got)
	}
	// The writer does not drop again what it dropped when it wrote.
	if got := a.read(); got != nil {
		t.Fatalf("the writer was told its own write: %+v", got)
	}
	// A favourite is one shop's page, not every list.
	other := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{other}})
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{other}) || got[0].Full || got[0].Reviews {
		t.Fatalf("one shop's page was told as %+v", got)
	}
}

// Telling the others can fail; the write it is about must not. What could not be told is
// kept and told with the next write, or at the next look, whichever comes first.
func TestAFailedTellingNeverFailsTheWriteAndIsToldLater(t *testing.T) {
	for _, later := range []string{"next write", "next look"} {
		t.Run(later, func(t *testing.T) {
			objects := &changestest.Objects{}
			clk := start()
			a, b := newInstance(objects, clk), newInstance(objects, clk)
			b.read()
			objects.FailWrites.Store(true)
			first := uuid.New()
			done := make(chan struct{})
			go func() { a.Publish(changes.Change{Stores: []uuid.UUID{first}, Full: true}); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("a write was held up by the marker it could not write")
			}
			if a.log.count("could not be told") != 1 {
				t.Fatal("the failure was not logged")
			}
			clk.advance(interval)
			if got := b.read(); got != nil {
				t.Fatalf("news that was never written arrived: %+v", got)
			}
			objects.FailWrites.Store(false)
			second := uuid.New()
			want := []uuid.UUID{first}
			if later == "next write" {
				a.Publish(changes.Change{Stores: []uuid.UUID{second}})
				want = append(want, second)
			} else {
				clk.advance(interval)
				a.read()
			}
			clk.advance(interval)
			got := b.read()
			if len(got) != 1 || !same(got[0].Stores, want) || !got[0].Full {
				t.Fatalf("after the failure the other instance was told %+v, want %v", got, want)
			}
		})
	}
}

// Cloud Storage takes about one write a second to one object and answers a faster one with
// 429; a 408 or 5xx asks for the same patience. Such a write is waited for and tried again,
// not given up at the first refusal -- given up, it would wait for this instance's next write
// or look, and be lost with the instance if neither came. What still does not fit in a write's
// time is kept, like any failure, and never holds the write up longer than that.
func TestABusyStoreIsWaitedForAndTriedAgain(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	b.read()
	shop := uuid.New()
	objects.Busy.Store(1)
	began := time.Now()
	a.Publish(changes.Change{Stores: []uuid.UUID{shop}, Full: true})
	if took := time.Since(began); took < 500*time.Millisecond {
		t.Fatalf("tried again after %v, sooner than the store takes a second write", took)
	}
	if n := a.log.count("could not be told"); n != 0 {
		t.Fatal("a write the store took at the second try was logged as not told")
	}
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{shop}) || !got[0].Full {
		t.Fatalf("after one busy answer the other instance was told %+v", got)
	}

	objects.Busy.Store(1000)
	other := uuid.New()
	began = time.Now()
	a.Publish(changes.Change{Stores: []uuid.UUID{other}})
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("a write was held up %v by a store that stayed busy", took)
	}
	if n := a.log.count("could not be told"); n != 1 {
		t.Fatalf("a store busy past the write's time was logged %d times", n)
	}
	objects.Busy.Store(0)
	clk.advance(interval)
	a.read()
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{other}) {
		t.Fatalf("news a busy store refused was told as %+v", got)
	}
}

// slowWrites is a store whose writes, once switched on, take as long as the caller allows.
type slowWrites struct {
	*changestest.Objects
	slow atomic.Bool
}

func (o *slowWrites) Write(ctx context.Context, name string, body []byte, generation int64) error {
	if o.slow.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return o.Objects.Write(ctx, name, body, generation)
}

// A look is what the reading request is waiting on, so news kept from an earlier failure is
// told in the time the look leaves, not before it: a store slow to take that write -- busy,
// or hanging -- cannot cost the reader the look.
func TestTellingKeptNewsCannotCostTheLook(t *testing.T) {
	objects := &changestest.Objects{}
	slow := &slowWrites{Objects: objects}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(slow, clk)
	b.read()
	objects.FailWrites.Store(true)
	b.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	objects.FailWrites.Store(false)
	shop := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{shop}})
	slow.slow.Store(true)
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{shop}) {
		t.Fatalf("with its own news held up the look was told %+v", got)
	}
	if n := b.log.count("could not be read"); n != 0 {
		t.Fatal("the look failed because telling its own news took the time")
	}
}

// What a failed write left behind is told when the process is stopped, if it had no later
// write or look to tell it with.
func TestFlushTellsWhatAFailedWriteLeft(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	b.read()
	if e := a.Flush(context.Background()); e != nil || objects.Writes.Load() != 0 {
		t.Fatalf("nothing to tell wrote %d times, %v", objects.Writes.Load(), e)
	}
	objects.FailWrites.Store(true)
	shop := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{shop}, Full: true})
	if e := a.Flush(context.Background()); e == nil {
		t.Fatal("a flush the store refused reported success")
	}
	objects.FailWrites.Store(false)
	if e := a.Flush(context.Background()); e != nil {
		t.Fatal(e)
	}
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{shop}) || !got[0].Full {
		t.Fatalf("what the stopping instance flushed was told as %+v", got)
	}
}

// When Cloud Storage does not answer, a look is skipped, not retried by every request: one
// attempt and one log line per interval. Nothing is dropped, so the copies go on answering
// until their maximum age, as they did before the marker existed. What was written before
// the outage is not lost by it.
func TestAnUnreachableStoreIsSkippedOncePerInterval(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	b.read()
	shop := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{shop}, Full: true})
	objects.FailReads.Store(true)
	clk.advance(interval)
	before := objects.Generations.Load()
	for i := 0; i < 500; i++ {
		if got := b.read(); got != nil {
			t.Fatalf("an unreadable marker was taken as news: %+v", got)
		}
	}
	if n := objects.Generations.Load() - before; n != 1 {
		t.Fatalf("%d attempts in one interval", n)
	}
	if n := b.log.count("could not be read"); n != 1 {
		t.Fatalf("%d log lines in one interval", n)
	}
	clk.advance(interval)
	b.read()
	if n := b.log.count("could not be read"); n != 2 {
		t.Fatalf("the next interval's failure was logged %d times in all", n)
	}
	objects.FailReads.Store(false)
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{shop}) {
		t.Fatalf("a write made before the outage was not told after it: %+v", got)
	}
}

// interleaved lets another instance write between this one's read and its write, exactly
// once: the race a conditional write exists for.
type interleaved struct {
	*changestest.Objects
	once  sync.Once
	other func()
}

func (o *interleaved) Write(ctx context.Context, name string, body []byte, generation int64) error {
	o.once.Do(o.other)
	return o.Objects.Write(ctx, name, body, generation)
}

// Two writes at once must both arrive. The second writer's read is out of date by the time
// it writes; the write is refused, and it reads the first writer's entry and adds to it.
func TestConcurrentWritesDoNotLoseAChange(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	b := newInstance(objects, clk)
	b.read()
	first, second := uuid.New(), uuid.New()
	c := newInstance(objects, clk)
	a := newInstance(&interleaved{Objects: objects, other: func() { c.Publish(changes.Change{Stores: []uuid.UUID{second}}) }}, clk)
	a.Publish(changes.Change{Stores: []uuid.UUID{first}})
	if objects.Conflicts.Load() != 1 {
		t.Fatalf("%d conflicts; the interleaving did not happen", objects.Conflicts.Load())
	}
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{first, second}) {
		t.Fatalf("after two writes at once the other instance was told %+v", got)
	}

	// And many at once, from several instances, with nothing scripted: every one of them
	// arrives, whether it was written at once or kept and written at its instance's next look.
	writers := []*instance{newInstance(objects, clk), newInstance(objects, clk), newInstance(objects, clk)}
	var want []uuid.UUID
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		shop := uuid.New()
		want = append(want, shop)
		w := writers[i%len(writers)]
		wg.Add(1)
		go func() { defer wg.Done(); w.Publish(changes.Change{Stores: []uuid.UUID{shop}}) }()
	}
	wg.Wait()
	clk.advance(interval)
	for _, w := range writers {
		w.read()
	}
	got := b.read()
	if len(got) != 1 || !same(got[0].Stores, want) {
		t.Fatalf("of %d concurrent changes the other instance was told %+v", len(want), got)
	}
	var held struct {
		Seq     int64 `json:"seq"`
		Changes []struct {
			Seq int64 `json:"seq"`
		} `json:"changes"`
	}
	if e := json.Unmarshal(objects.Body(name), &held); e != nil {
		t.Fatal(e)
	}
	if held.Seq != 14 || len(held.Changes) != 14 {
		t.Fatalf("the marker holds %d entries up to %d, want 14", len(held.Changes), held.Seq)
	}
	for i, x := range held.Changes {
		if x.Seq != int64(i+1) {
			t.Fatalf("entry %d is numbered %d", i, x.Seq)
		}
	}
}

// An instance that falls so far behind that entries it never saw have been dropped cannot
// tell what changed, so it drops everything. Slow and safe, never fast and wrong.
func TestAnInstanceThatMissedEntriesDropsEverything(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	b.read()
	for i := 0; i < 70; i++ {
		a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	}
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !got[0].AllPages || !got[0].Full || !got[0].Reviews {
		t.Fatalf("an instance that missed entries was told only %+v", got)
	}
	if len(objects.Body(name)) > 64<<10 {
		t.Fatalf("the marker grew to %d bytes", len(objects.Body(name)))
	}
}

// A new instance read the catalogue when it started, so it takes only what was written
// since then -- and not the history every instance before it already took.
func TestANewInstanceTakesOnlyWhatWasWrittenSinceItStarted(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a := newInstance(objects, clk)
	old := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{old}, Full: true})
	clk.advance(time.Hour)
	b := newInstance(objects, clk)
	if got := b.read(); got != nil {
		t.Fatalf("a new instance was told about a write from before it started: %+v", got)
	}
	c := newInstance(objects, clk)
	fresh := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{fresh}})
	if got := c.read(); len(got) != 1 || !same(got[0].Stores, []uuid.UUID{fresh}) || got[0].Full {
		t.Fatalf("a new instance was told %+v, want only the write since it started", got)
	}
}

// A marker somebody broke by hand is not trusted: whoever reads it drops everything, and the
// next write starts it again, which every instance that had read further also takes as
// everything.
func TestABrokenMarkerIsTakenAsEverything(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	for i := 0; i < 3; i++ {
		a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	}
	b.read()
	before := run(t, objects)
	objects.Put(name, []byte("{not json"))
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !got[0].AllPages {
		t.Fatalf("a broken marker was taken as %+v", got)
	}
	a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !got[0].AllPages {
		t.Fatalf("a marker started again was taken as %+v", got)
	}
	if after := run(t, objects); after.Run == before.Run || after.Seq != 1 {
		t.Fatalf("the writer did not start the marker again: %s", objects.Body(name))
	}
}

// A marker started again whose numbers happen to match where an instance had got to is
// still a different marker: its name says so.
func TestAMarkerStartedAgainIsNotMistakenForTheOldOne(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	b.read()
	a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	clk.advance(interval)
	b.read()
	// Broken and written again before the other instance looks: one entry, numbered one,
	// exactly where it had got to.
	objects.Put(name, []byte("{not json"))
	a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	if m := run(t, objects); m.Seq != 1 {
		t.Fatalf("the marker was not started again: %+v", m)
	}
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !got[0].AllPages {
		t.Fatalf("a marker started again was taken for the old one: %+v", got)
	}
}

type heldMarker struct {
	Run string `json:"run"`
	Seq int64  `json:"seq"`
}

func run(t *testing.T, objects *changestest.Objects) heldMarker {
	t.Helper()
	var m heldMarker
	if e := json.Unmarshal(objects.Body(name), &m); e != nil {
		t.Fatal(e)
	}
	return m
}

// A change naming more shops than an entry should carry is told as every page.
func TestAChangeNamingManyShopsIsToldAsEveryPage(t *testing.T) {
	objects := &changestest.Objects{}
	clk := start()
	a, b := newInstance(objects, clk), newInstance(objects, clk)
	b.read()
	for i := 0; i < 60; i++ {
		a.Publish(changes.Change{Stores: []uuid.UUID{uuid.New()}})
	}
	clk.advance(interval)
	if got := b.read(); len(got) != 1 || !got[0].AllPages || len(got[0].Stores) != 0 {
		t.Fatalf("sixty shops were told as %+v", got)
	}
}

// Switched off, the marker is nil and everything it does is nothing.
func TestANilMarkerIsAWorkingConfiguration(t *testing.T) {
	var m *changes.Marker
	m.Publish(changes.Change{Full: true})
	m.Check(context.Background(), func(c changes.Change) { t.Fatalf("a nil marker told %+v", c) })
	if e := m.Flush(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestObjectNameKeepsEnvironmentsApart(t *testing.T) {
	if a, b := changes.ObjectName("production"), changes.ObjectName("development"); a == b || !strings.HasPrefix(a, "_cache/") {
		t.Fatalf("%q and %q", a, b)
	}
}
