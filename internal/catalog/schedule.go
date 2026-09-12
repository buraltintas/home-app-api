package catalog

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Refresher keeps the catalogue from going stale on its own.
//
// Every shop in it came from a list a chain publishes and keeps changing: branches open,
// move and close, and a catalogue nobody re-reads is a catalogue that is quietly wrong a
// month later. Until now the only way to re-read one was for a person to run the command or
// press the button, which means the answer to "how fresh is this" was "as fresh as the last
// time somebody remembered".
//
// One brand at a time, and the one that was read longest ago. That is deliberate rather than
// lazy:
//
//   - It spreads the load over days instead of hammering thirty sites in an hour. The fetcher
//     already keeps to one request a second per host; this keeps to one host at a time.
//   - A failure costs one brand's turn, not the whole sweep, and the next tick simply picks
//     the next oldest.
//   - It is self-levelling. A brand added today is the oldest thing in the table and is read
//     first; after that everything drifts into the same rotation without a schedule to write
//     down anywhere.
//
// With one brand every few hours, a registry of thirty comes round about every four days.
type Refresher struct {
	db       *pgxpool.Pool
	log      *slog.Logger
	every    time.Duration
	resolver *Resolver
}

func NewRefresher(db *pgxpool.Pool, log *slog.Logger, every time.Duration) *Refresher {
	return &Refresher{db: db, log: log, every: every}
}

func (r *Refresher) Run(ctx context.Context) error {
	if r.every <= 0 {
		r.log.Info("catalogue refresh disabled")
		<-ctx.Done()
		return ctx.Err()
	}
	// Not on the first tick of the process. A deploy restarts the worker, and a refresh that
	// began the moment it came up would fetch somebody's website every time we shipped.
	ticker := time.NewTicker(r.every)
	defer ticker.Stop()
	r.log.Info("catalogue refresh scheduled", "every", r.every.String())
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if e := r.refreshOldest(ctx); e != nil && ctx.Err() == nil {
				// One brand's failure is not the sweep's failure: a chain's site can be
				// down for a day without that being a reason to stop reading the other
				// twenty-eight.
				r.log.Error("catalogue refresh failed", "error", e)
			}
		}
	}
}

func (r *Refresher) refreshOldest(ctx context.Context) error {
	var slug string
	// Never imported sorts first, which is what makes a newly registered brand arrive
	// without anybody having to run anything.
	e := r.db.QueryRow(ctx, `
SELECT slug FROM brands
 WHERE active AND locator_kind <> 'none'
 ORDER BY last_imported_at ASC NULLS FIRST
 LIMIT 1`).Scan(&slug)
	if e != nil {
		return e
	}
	brands, e := Brands(ctx, r.db, slug)
	if e != nil || len(brands) != 1 {
		return e
	}
	source, ok, e := SourceFor(brands[0], NewFetcher())
	if e != nil || !ok {
		return e
	}
	if r.resolver == nil {
		// Built once and kept: it reads the whole administrative table, which is seventy
		// thousand rows, and that does not change between imports.
		if r.resolver, e = NewResolver(ctx, r.db); e != nil {
			return e
		}
	}
	started := time.Now()
	report, e := NewImporter(r.db, r.resolver).Run(ctx, source, true)
	if e != nil {
		return e
	}
	r.log.Info("catalogue refreshed",
		"brand", slug, "fetched", report.Fetched, "inserted", report.Inserted,
		"updated", report.Updated, "review", report.Review, "skipped", report.Skipped,
		"took", time.Since(started).Round(time.Second).String())
	return nil
}
