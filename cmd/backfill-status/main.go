// Command backfill-status fills in the closure status Google publishes for a store.
//
// The warning that tells somebody a store may have closed reads this value, and the value
// is written when a store is imported or when a search turns it up again. On the day the
// warning shipped not one store in the catalogue held it, so the feature was complete and
// silent. This asks once for the stores already here.
//
// Everything here is deliberately narrow, because it runs against the production database:
//
//   - Only records holding no status are read at all.
//   - The only write merges business_status into the existing attribution, guarded a second
//     time in the statement. Nothing else in that record is touched.
//   - Nothing is inserted and nothing is deleted.
//   - It prints what it would do and stops. Applying requires -apply, and even then the
//     whole thing is one transaction.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

type candidate struct {
	id, name, placeID, status string
}

func main() {
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	fetch := flag.Bool("fetch", false, "ask Google for the status of stores that have none stored")
	cachePath := flag.String("cache", "/tmp/store-status.json", "where fetched statuses are kept, so a dry run and an apply share one fetch")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	must(err)
	defer db.Close()

	cache := map[string]string{}
	if raw, err := os.ReadFile(*cachePath); err == nil {
		_ = json.Unmarshal(raw, &cache)
	}

	rows, err := db.Query(ctx, `
SELECT s.id::text, s.name, x.external_id
FROM stores s
JOIN store_external_sources x ON x.store_id = s.id AND x.provider = 'google'
WHERE s.deleted_at IS NULL AND NOT (x.attribution ? 'business_status')
ORDER BY s.name`)
	must(err)
	var pending []candidate
	for rows.Next() {
		var c candidate
		must(rows.Scan(&c.id, &c.name, &c.placeID))
		c.status = cache[c.placeID]
		pending = append(pending, c)
	}
	rows.Close()
	must(rows.Err())
	fmt.Fprintf(os.Stderr, "stores with no status stored: %d\n", len(pending))

	if *fetch {
		places := search.NewGooglePlaces(os.Getenv("GOOGLE_PLACES_API_KEY"))
		asked := 0
		for i := range pending {
			if _, cached := cache[pending[i].placeID]; cached {
				continue
			}
			place, err := places.PlaceDetails(ctx, pending[i].placeID)
			asked++
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not fetch %q: %v\n", pending[i].name, err)
				continue
			}
			pending[i].status = place.BusinessStatus
			cache[pending[i].placeID] = place.BusinessStatus
			if raw, err := json.MarshalIndent(cache, "", " "); err == nil {
				_ = os.WriteFile(*cachePath, raw, 0o600)
			}
			// Unhurried on purpose. This is a one-off backfill, not a hot path.
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "asked the provider about %d stores\n\n", asked)
	}

	counts := map[string]int{}
	var found, closed []candidate
	for _, c := range pending {
		if c.status == "" {
			continue
		}
		counts[c.status]++
		found = append(found, c)
		if c.status != "OPERATIONAL" {
			closed = append(closed, c)
		}
	}
	var kinds []string
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Printf("  %-24s %d\n", k, counts[k])
	}
	if len(closed) > 0 {
		fmt.Println("\nstores Google no longer calls open:")
		for _, c := range closed {
			fmt.Printf("  %-46s %s\n", trim(c.name, 46), c.status)
		}
	}
	fmt.Printf("\nstores to update: %d\nstores the provider said nothing about: %d\n", len(found), len(pending)-len(found))

	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)
	written := 0
	for _, c := range found {
		raw, err := json.Marshal(map[string]any{"business_status": c.status})
		must(err)
		tag, err := tx.Exec(ctx, `
UPDATE store_external_sources SET attribution = attribution || $2::jsonb
WHERE store_id = $1::uuid AND provider = 'google' AND NOT (attribution ? 'business_status')`, c.id, raw)
		must(err)
		written += int(tag.RowsAffected())
	}
	must(tx.Commit(ctx))
	fmt.Printf("\nstored a status for %d stores.\n", written)
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
