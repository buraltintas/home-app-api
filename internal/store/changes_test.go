package store

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/changes"
	"github.com/burakaltintas/home-app-api/internal/changes/changestest"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/google/uuid"
)

// sharedDatabase is the one database every instance reads its copy from.
type sharedDatabase struct {
	mu sync.Mutex
	f  *fixture
}

func (d *sharedDatabase) firstReview(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := storeID(n)
	d.f.rows.withPosts[id] = true
	d.f.rows.stats[id] = Stats{ReviewCount: 1, PostCount: 1, RatingCount: 1, AverageRating: 5}
}

// instance is one API process as the catalogue sees it: its own copy of the shared
// database, its own marker over the one bucket, and a count of its reads of the catalogue.
type instance struct {
	*Catalog
	marker *changes.Marker
	loads  atomic.Int32
}

type clusterRig struct {
	db      *sharedDatabase
	objects *changestest.Objects
	clock   time.Time
	mu      sync.Mutex
}

func newClusterRig() *clusterRig {
	f := newFixture()
	f.add(1, "İzmir", 38.4, 27.1, "lighting")
	f.add(2, "İzmir", 38.41, 27.1, "lighting")
	return &clusterRig{db: &sharedDatabase{f: f}, objects: &changestest.Objects{}, clock: time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC)}
}

func (rig *clusterRig) now() time.Time {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	return rig.clock
}

func (rig *clusterRig) advance(d time.Duration) {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	rig.clock = rig.clock.Add(d)
}

// start brings up an instance the way main does: the marker first, then the catalogue. Its
// marker looks on every read, so a test decides when a look happens by when it reads; how
// often a look is allowed is the marker's own test.
func (rig *clusterRig) start(t *testing.T) *instance {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	in := &instance{marker: changes.New(rig.objects, "_cache/test/catalog-changes.json", time.Nanosecond, quiet)}
	in.Catalog = newCatalog(func(context.Context) (*Snapshot, error) {
		in.loads.Add(1)
		rig.db.mu.Lock()
		defer rig.db.mu.Unlock()
		return rig.db.f.build(), nil
	}, func() bool { return false }, 6*time.Hour, quiet)
	in.Catalog.now = rig.now
	if e := in.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
	in.loads.Store(0)
	return in
}

// forget is what the server does with news, as far as the catalogue is concerned
// (server.forget does the same, and drops the read cache besides).
func (in *instance) forget(c changes.Change) {
	if c.Full || c.AllPages {
		in.Invalidate()
		return
	}
	for _, id := range c.Stores {
		in.InvalidateStore(id)
	}
}

// changed is a write landing on this instance: dropped here, then told.
func (in *instance) changed(c changes.Change) {
	in.forget(c)
	in.marker.Publish(c)
}

// read is an anonymous store page arriving at this instance: the look, then the copy. False
// when the copy leaves the page to the database.
func (in *instance) read(t *testing.T, n int) bool {
	t.Helper()
	before := in.loads.Load()
	in.marker.Check(context.Background(), in.forget)
	if in.loads.Load() != before {
		t.Fatal("looking for other instances' writes read the database")
	}
	snap := in.Current(context.Background())
	if snap == nil {
		return false
	}
	_, ok := snap.Store(storeID(n).String(), i18n.LocaleTR)
	return ok
}

// The owner's requirement, end to end: a shop's first review written through one instance
// is on the other's next look, so its page is no longer answered from the copy that predates
// it -- the database answers it, as it answers every shop with a review. The look itself
// asks only the bucket.
func TestAReviewOnOneInstanceIsShownByTheOtherAfterItsNextLook(t *testing.T) {
	rig := newClusterRig()
	a, b := rig.start(t), rig.start(t)
	if !b.read(t, 1) {
		t.Fatal("a quiet shop was not answered from memory to begin with")
	}
	rig.db.firstReview(1)
	a.changed(changes.Change{Stores: []uuid.UUID{storeID(1)}, Full: true, Reviews: true})
	if a.read(t, 1) {
		t.Fatal("the instance that took the review answered from the copy before it")
	}
	if b.read(t, 1) {
		t.Fatal("the other instance answered from the copy before the review after its look")
	}
	// The copy was read under a minute ago, so the database answers until then; after it,
	// the catalogue is read again and the shop is left to the database by what it now holds.
	rig.advance(catalogReloadEvery)
	if b.read(t, 1) || b.loads.Load() != 1 {
		t.Fatalf("after the look the shop was still answered from memory (%d reads)", b.loads.Load())
	}
	if !b.read(t, 2) {
		t.Fatal("a shop nobody wrote about left memory")
	}
}

// A favourite is one shop's page. On the other instance that page goes to the database and
// every other answer stays in memory: the catalogue is not read again for it.
func TestAFavouriteOnOneInstanceSendsOnlyThatPageToTheDatabaseOnTheOther(t *testing.T) {
	rig := newClusterRig()
	a, b := rig.start(t), rig.start(t)
	a.changed(changes.Change{Stores: []uuid.UUID{storeID(2)}})
	if b.read(t, 2) {
		t.Fatal("the changed page was answered from before the change")
	}
	if !b.read(t, 1) || b.loads.Load() != 0 {
		t.Fatalf("one shop's favourite moved another page or read the catalogue (%d reads)", b.loads.Load())
	}
}

// With Cloud Storage down nothing is learned and nothing is dropped: the copy goes on
// answering, without asking the database, until its maximum age -- the rule from before the
// marker existed -- and is then read again with the write in it.
func TestWithCloudStorageDownTheCopyLastsUntilItsMaximumAge(t *testing.T) {
	rig := newClusterRig()
	a, b := rig.start(t), rig.start(t)
	rig.objects.FailReads.Store(true)
	rig.db.firstReview(1)
	a.changed(changes.Change{Stores: []uuid.UUID{storeID(1)}, Full: true, Reviews: true})
	if a.read(t, 1) {
		t.Fatal("the instance that took the review answered from before it")
	}
	rig.advance(5 * time.Hour)
	if !b.read(t, 1) || b.loads.Load() != 0 {
		t.Fatal("an unreachable bucket sent reads to the database before the copy's maximum age")
	}
	rig.advance(time.Hour)
	if b.read(t, 1) || b.loads.Load() != 1 {
		t.Fatalf("at its maximum age the copy was not read again (%d reads)", b.loads.Load())
	}
}
