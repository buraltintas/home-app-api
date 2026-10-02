package store

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Catalog holds the current Snapshot and decides when to read a new one.
//
// The whole point is that reading it must not wake the database, so neither may keeping it
// fresh. There are three occasions to read the catalogue again, and only the last costs a
// wake of its own:
//
//   - Something forces it: the process has just written to the catalogue itself, or holds
//     no copy at all. The request asking is about to go to the database anyway, so the read
//     rides on a wake that was already happening.
//   - The copy is over an hour old and the database is already awake for somebody else --
//     it answered this process within the last half minute, so it is up for at least four
//     more. The read costs those seconds of compute, not five minutes of it.
//   - The copy has reached its maximum age. Then it is read whether the database is awake
//     or not, which is at most one wake per instance per maximum age.
//
// Every read happens inside the request that found the need for it, never on a goroutine of
// its own. Cloud Run throttles the CPU of an instance between requests, and a query left
// half-run on a throttled instance keeps the database awake until the next request arrives
// -- up to a quarter of an hour at night. A reader therefore waits the second or two a read
// takes, once an hour at most; everybody else keeps reading the old copy meanwhile, without
// a lock, and swaps to the new one when it is ready. If the read fails the old copy stays.
type Catalog struct {
	load     func(context.Context) (*Snapshot, error)
	awake    func() bool
	maxAge   time.Duration
	freshFor time.Duration
	now      func() time.Time
	log      *slog.Logger

	current atomic.Pointer[Snapshot]
	// Every write this process makes to the catalogue takes a number from epoch. A copy
	// remembers the number current when it began reading; it is too old for any write with a
	// later one. stale is the number of the last write that changed everything, and dirty
	// holds, per shop, the number of the last write that changed only that shop's page.
	epoch   atomic.Uint64
	stale   atomic.Uint64
	dirty   sync.Map
	loading atomic.Bool
	retryAt atomic.Int64
}

const (
	// How long after this process last heard from the database it is still certainly awake.
	// It suspends after five quiet minutes; half a minute leaves the read nowhere near that.
	awakeWindow = 30 * time.Second
	// How old a copy may get before a database that is awake anyway is used to replace it.
	catalogFreshFor = time.Hour
	// A read of the catalogue takes a second or two. One that has not finished in this long
	// is not going to, and the request waiting on it has better things to do.
	catalogLoadTimeout = 30 * time.Second
	// After a failed read the database is left alone this long, so an outage is not met with
	// a read attempt from every request.
	catalogRetryAfter = time.Minute
)

// NewCatalog returns a catalogue that reads from db and learns from activity whether the
// database is awake. maxAge is the most a copy may lag a change made outside this process.
func NewCatalog(db *pgxpool.Pool, activity *database.Activity, maxAge time.Duration, log *slog.Logger) *Catalog {
	return newCatalog(func(ctx context.Context) (*Snapshot, error) { return loadSnapshot(ctx, db) },
		func() bool { return activity.UsedWithin(awakeWindow) }, maxAge, log)
}

func newCatalog(load func(context.Context) (*Snapshot, error), awake func() bool, maxAge time.Duration, log *slog.Logger) *Catalog {
	if log == nil {
		log = slog.Default()
	}
	return &Catalog{load: load, awake: awake, maxAge: maxAge, freshFor: min(catalogFreshFor, maxAge), now: time.Now, log: log}
}

// Load reads the catalogue now. It is meant for startup, before the port opens: Cloud Run
// gives a starting container its CPU, and the database was just woken by the connection
// check anyway.
func (c *Catalog) Load(ctx context.Context) error {
	c.loading.Store(true)
	defer c.loading.Store(false)
	return c.reload(ctx, "startup")
}

// Current returns the copy to answer from, or nil when the database must answer instead. It
// may first read the catalogue again, within the calling request, by the rules above.
func (c *Catalog) Current(ctx context.Context) *Snapshot {
	if c == nil {
		return nil
	}
	snap := c.current.Load()
	switch {
	case snap == nil || snap.epoch < c.stale.Load():
		reason := "invalidated"
		if snap == nil {
			reason = "missing"
		}
		c.refresh(ctx, reason)
		if snap = c.current.Load(); snap == nil || snap.epoch < c.stale.Load() {
			return nil
		}
	case c.now().Sub(snap.loadedAt) >= c.maxAge:
		c.refresh(ctx, "max_age")
		snap = c.current.Load()
	case c.now().Sub(snap.loadedAt) >= c.freshFor && c.awake():
		c.refresh(ctx, "awake")
		snap = c.current.Load()
	}
	return snap
}

// Invalidate says this process has just changed the catalogue in a way that can move any
// list -- a review, a merge, a category, a new shop. Until a copy read after this moment is
// in place, every catalogue read goes to the database, so whoever made the change sees it at
// once; the next catalogue read after it reads the catalogue again.
func (c *Catalog) Invalidate() {
	if c == nil {
		return
	}
	c.stale.Store(c.epoch.Add(1))
}

// InvalidateStore says this process has just changed one shop's own page and nothing that
// any list shows -- a favourite count, a premium or catalogue flag. That page goes to the
// database until a copy read after this moment is in place; everything else stays here.
func (c *Catalog) InvalidateStore(id uuid.UUID) {
	if c == nil {
		return
	}
	c.dirty.Store(id, c.epoch.Add(1))
}

func (c *Catalog) refresh(ctx context.Context, reason string) {
	if c.now().UnixNano() < c.retryAt.Load() || !c.loading.CompareAndSwap(false, true) {
		return
	}
	defer c.loading.Store(false)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), catalogLoadTimeout)
	defer cancel()
	if e := c.reload(ctx, reason); e != nil {
		c.retryAt.Store(c.now().Add(catalogRetryAfter).UnixNano())
	}
}

func (c *Catalog) reload(ctx context.Context, reason string) error {
	epoch := c.epoch.Load()
	started := c.now()
	snap, e := c.load(ctx)
	if e != nil {
		observability.CatalogLoad(reason, false, 0, 0)
		c.log.Error("catalog snapshot read failed; keeping the copy already held", "reason", reason, "error", e)
		return e
	}
	snap.epoch, snap.loadedAt, snap.dirty = epoch, started, &c.dirty
	c.current.Store(snap)
	c.dirty.Range(func(id, marked any) bool {
		if marked.(uint64) <= epoch {
			c.dirty.CompareAndDelete(id, marked)
		}
		return true
	})
	stores, bytes := snap.Stores()
	observability.CatalogLoad(reason, true, stores, bytes)
	c.log.Info("catalog snapshot read", "reason", reason, "stores", stores, "approx_bytes", bytes, "took_ms", c.now().Sub(started).Milliseconds())
	return nil
}
