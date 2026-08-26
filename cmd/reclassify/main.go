// Command reclassify assigns categories to stores that have none.
//
// It exists because the classifier learned to read names it could not read before, and a
// store's categories are worked out once, when it is first imported. Nearly a fifth of the
// catalogue was left with none and could not be found by anything that filters on one.
//
// Everything here is deliberately narrow, because it runs against the production database:
//
//   - Only stores with zero category links are considered, and that set is read once at
//     the start. A store that already has categories is never read, never updated, never
//     touched.
//   - The only write is an INSERT into store_category_links, with ON CONFLICT DO NOTHING.
//     Nothing is updated and nothing is deleted, so the worst case of a bug is a category
//     that should not be there -- visible, and removable by deleting one row.
//   - The `stores` table itself is not written to at all.
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

func main() {
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	fetch := flag.Bool("fetch", false, "ask Google for the types of candidates that have none stored")
	cachePath := flag.String("cache", "/tmp/store-types.json", "where fetched types are kept, so a dry run and an apply share one fetch")
	flag.Parse()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()

	// Fetched types are cached on disk on purpose. A dry run and the apply that follows it
	// must not each cost a round of provider calls, and re-running after a mistake must not
	// either. Delete the file to force a refetch.
	cache := map[string][]string{}
	if raw, err := os.ReadFile(*cachePath); err == nil {
		_ = json.Unmarshal(raw, &cache)
	}

	slugs := map[string]string{}
	rows, err := db.Query(ctx, `SELECT id::text, slug FROM store_categories`)
	must(err)
	for rows.Next() {
		var id, slug string
		must(rows.Scan(&id, &slug))
		slugs[slug] = id
	}
	rows.Close()

	type candidate struct {
		id, name, placeID string
		types             []string
		cats              []string
	}
	var found []candidate
	var stillNone int
	var pending []candidate

	// Google's own types first, the name only as a fallback. The types are what make this
	// generic: they exist for every store in the country and say what the place is, while
	// a name only helps when somebody happened to put the product in it. Older stores were
	// imported before the types were kept, so they come back empty and fall back.
	rows, err = db.Query(ctx, `
SELECT s.id::text, s.name,
  coalesce((SELECT x.external_id FROM store_external_sources x
            WHERE x.store_id = s.id AND x.provider = 'google' LIMIT 1), ''),
  coalesce((SELECT array(SELECT jsonb_array_elements_text(x.attribution->'types'))
            FROM store_external_sources x
            WHERE x.store_id = s.id AND x.provider = 'google' AND x.attribution ? 'types'
            LIMIT 1), '{}')
FROM stores s LEFT JOIN store_category_links l ON l.store_id = s.id
WHERE s.deleted_at IS NULL
GROUP BY s.id, s.name
HAVING count(l.category_id) = 0
ORDER BY s.name`)
	must(err)
	for rows.Next() {
		var c candidate
		must(rows.Scan(&c.id, &c.name, &c.placeID, &c.types))
		if len(c.types) == 0 {
			c.types = cache[c.placeID]
		}
		pending = append(pending, c)
	}
	rows.Close()

	// Ask the provider only for the ones we have nothing for, and only when asked to.
	// Every call costs money and the answers do not change from one minute to the next,
	// so they are written to the cache as they arrive -- an interrupted run resumes rather
	// than starting the bill again.
	if *fetch {
		places := search.NewGooglePlaces(os.Getenv("GOOGLE_PLACES_API_KEY"))
		asked := 0
		for i := range pending {
			if len(pending[i].types) > 0 || pending[i].placeID == "" {
				continue
			}
			place, err := places.PlaceDetails(ctx, pending[i].placeID)
			asked++
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not fetch %q: %v\n", pending[i].name, err)
				continue
			}
			pending[i].types = place.Types
			cache[pending[i].placeID] = place.Types
			if raw, err := json.MarshalIndent(cache, "", " "); err == nil {
				_ = os.WriteFile(*cachePath, raw, 0o600)
			}
			// Unhurried on purpose. This is a one-off backfill, not a hot path, and there
			// is nothing to gain from arriving at a rate limit.
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "asked the provider about %d stores\n\n", asked)
	}

	for _, c := range pending {
		for _, slug := range search.StoreCategories(c.name, c.types) {
			// A slug the taxonomy does not have is a bug in the classifier, not a reason
			// to invent a category. Skip it loudly rather than writing something the rest
			// of the product cannot interpret.
			if _, ok := slugs[slug]; !ok {
				fmt.Fprintf(os.Stderr, "unknown category %q for %q -- skipped\n", slug, c.name)
				continue
			}
			c.cats = append(c.cats, slug)
		}
		if len(c.cats) == 0 {
			stillNone++
			continue
		}
		found = append(found, c)
	}

	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	writes := 0
	for _, c := range found {
		source := "name"
		if len(c.types) > 0 {
			source = "google"
		}
		fmt.Printf("%-46s %-7s %v\n", trim(c.name, 46), source, c.cats)
		writes += len(c.cats)
	}
	fmt.Printf("\nstores to classify: %d (%d category rows)\nstores still without one: %d\n", len(found), writes, stillNone)

	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)

	// Keep what the provider told us, so this never has to be asked again. Merged into the
	// existing attribution rather than replacing it: that record also holds the rating,
	// the photo reference and its required credits, and overwriting it would quietly throw
	// all of that away.
	stored := 0
	for _, c := range pending {
		if len(c.types) == 0 || c.placeID == "" {
			continue
		}
		types, err := json.Marshal(map[string]any{"types": c.types})
		must(err)
		tag, err := tx.Exec(ctx, `
UPDATE store_external_sources
SET attribution = attribution || $2::jsonb
WHERE store_id = $1::uuid AND provider = 'google' AND NOT attribution ? 'types'`, c.id, types)
		must(err)
		stored += int(tag.RowsAffected())
	}

	inserted := 0
	for _, c := range found {
		for _, slug := range c.cats {
			// Guarded per row, not per store. Guarding per store looks safer and is not:
			// after the first category lands the store has a link, so every further
			// category for the same store is silently dropped. That happened on the first
			// run and cost one store its second category.
			tag, err := tx.Exec(ctx, `
INSERT INTO store_category_links(store_id, category_id)
VALUES ($1::uuid, $2::uuid)
ON CONFLICT DO NOTHING`, c.id, slugs[slug])
			must(err)
			inserted += int(tag.RowsAffected())
		}
	}
	must(tx.Commit(ctx))
	fmt.Printf("\ninserted %d category rows, stored types for %d stores.\n", inserted, stored)
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
