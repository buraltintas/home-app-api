package database

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) { return OpenWatched(ctx, url, nil) }

// OpenWatched is Open with somebody listening: activity hears about every connection this
// process hands back to the pool, which is the only honest way for the process to know the
// database is awake without asking it.
func OpenWatched(ctx context.Context, url string, activity *Activity) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	cfg.MaxConns = 20
	// Nothing is held open while nobody is asking. The database suspends itself
	// after five minutes without a connection and is billed for the hours it is
	// awake, and a floor of two connections meant it never once qualified: it
	// stayed up every hour of every night for a service whose nights are a
	// crawler and nobody else. A warm connection is worth about a second on the
	// first request after a quiet spell, which is not worth a night's compute.
	cfg.MinConns = 0
	cfg.MaxConnLifetime = time.Hour
	// Long enough that a burst of requests shares one connection, short enough
	// that a single query at three in the morning does not hold the database up
	// for the next quarter of an hour.
	cfg.MaxConnIdleTime = 90 * time.Second
	if activity != nil {
		cfg.ConnConfig.Tracer = activity
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Activity remembers when this process last finished something with the database.
//
// It exists so that work which can be done at any time is done while the database is
// already awake for somebody else. The database suspends after five quiet minutes and bills
// every minute it is up; a refresh that wakes it costs those five minutes, and the same
// refresh made a few seconds after a reader's query costs a few seconds. The difference is
// only knowable here, from the connections going back into the pool -- asking the database
// whether it is awake would wake it.
//
// It is a pool tracer rather than an AfterRelease hook on purpose: AfterRelease makes the
// pool hand every connection back on a goroutine of its own, and this must not change how
// the pool behaves, only listen to it.
type Activity struct {
	last      atomic.Int64
	mu        sync.Mutex
	listeners atomic.Pointer[[]func()]
}

// UsedWithin reports whether this process finished a database round trip in the last d.
func (a *Activity) UsedWithin(d time.Duration) bool {
	if a == nil {
		return false
	}
	last := a.last.Load()
	return last != 0 && time.Since(time.Unix(0, last)) <= d
}

// OnUse registers f to be told after every round trip. It is called on the releasing
// goroutine, so it must return at once: note something, nudge something, never query.
func (a *Activity) OnUse(f func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var next []func()
	if current := a.listeners.Load(); current != nil {
		next = append(next, *current...)
	}
	next = append(next, f)
	a.listeners.Store(&next)
}

func (a *Activity) touch() {
	a.last.Store(time.Now().UnixNano())
	if listeners := a.listeners.Load(); listeners != nil {
		for _, f := range *listeners {
			f()
		}
	}
}

func (a *Activity) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (a *Activity) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TraceRelease is the pool handing a connection back: whatever it was used for is over, so
// the database was awake a moment ago.
func (a *Activity) TraceRelease(*pgxpool.Pool, pgxpool.TraceReleaseData) { a.touch() }
