// Command align-categories brings the whole catalogue into line with what the classifier
// now knows, once.
//
// Two thirds of the catalogue was classified blind: Google's types were not kept when
// those stores were imported, so 343 of 517 stores were sorted on their name alone. The
// types are the generic evidence -- they exist for every business in the country and say
// what the place is -- so they are fetched first and everything is decided from them.
//
// Everything here is deliberately narrow, because it runs against the production database:
//
//   - A category is added whenever the classifier can derive it and the store lacks it.
//   - A category is removed only against evidence: the store must have Google types, and
//     the classifier must have derived a non-empty set from them. A store the classifier
//     can say nothing about is never stripped of what it already has.
//   - A store is retired -- soft-deleted, reversible by clearing deleted_at -- only when
//     the classifier says it is not a home and living business at all.
//   - A store that carries anybody's review, favourite or verified visit is never retired,
//     whatever the classifier says. Community work outranks a directory. Those are listed
//     instead, for a person to decide.
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
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	id, name  string
	placeID   string
	types     []string
	current   []string
	protected bool

	add    []string
	retire bool
}

func main() {
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	fetch := flag.Bool("fetch", false, "ask Google for the types of stores that have none stored")
	cachePath := flag.String("cache", "/tmp/store-types-all.json", "where fetched types are kept, so a dry run and an apply share one fetch")
	report := flag.String("report", "/tmp/align-report.txt", "where the full store-by-store decision is written")
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

	cache := map[string][]string{}
	if raw, err := os.ReadFile(*cachePath); err == nil {
		_ = json.Unmarshal(raw, &cache)
	}

	slugs := map[string]string{}
	rows, err := db.Query(ctx, `SELECT id::text, slug FROM store_categories WHERE active`)
	must(err)
	for rows.Next() {
		var id, slug string
		must(rows.Scan(&id, &slug))
		slugs[slug] = id
	}
	rows.Close()

	// Everything in the catalogue, with what we hold about it. A store carrying somebody's
	// review, favourite or verified visit is marked here and never retired below.
	rows, err = db.Query(ctx, `
SELECT s.id::text, s.name,
  coalesce((SELECT x.external_id FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' LIMIT 1),''),
  coalesce((SELECT array(SELECT jsonb_array_elements_text(x.attribution->'types'))
            FROM store_external_sources x
            WHERE x.store_id=s.id AND x.provider='google' AND x.attribution ? 'types' LIMIT 1),'{}'),
  coalesce((SELECT array_agg(c.slug) FROM store_category_links k
            JOIN store_categories c ON c.id=k.category_id WHERE k.store_id=s.id),'{}'),
  EXISTS(SELECT 1 FROM posts p WHERE p.store_id=s.id AND p.deleted_at IS NULL)
   OR EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=s.id)
   OR EXISTS(SELECT 1 FROM store_visit_verifications v WHERE v.store_id=s.id)
FROM stores s
WHERE s.deleted_at IS NULL
ORDER BY s.name`)
	must(err)
	var all []store
	for rows.Next() {
		var st store
		must(rows.Scan(&st.id, &st.name, &st.placeID, &st.types, &st.current, &st.protected))
		if len(st.types) == 0 {
			st.types = cache[st.placeID]
		}
		all = append(all, st)
	}
	rows.Close()
	must(rows.Err())

	blind := 0
	for _, st := range all {
		if len(st.types) == 0 {
			blind++
		}
	}
	fmt.Fprintf(os.Stderr, "catalogue: %d stores, %d with no types to judge them by\n", len(all), blind)

	if *fetch {
		places := search.NewGooglePlaces(os.Getenv("GOOGLE_PLACES_API_KEY"))
		asked := 0
		for i := range all {
			if len(all[i].types) > 0 || all[i].placeID == "" {
				continue
			}
			place, err := places.PlaceDetails(ctx, all[i].placeID)
			asked++
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not fetch %q: %v\n", all[i].name, err)
				continue
			}
			all[i].types = place.Types
			cache[all[i].placeID] = place.Types
			if raw, err := json.MarshalIndent(cache, "", " "); err == nil {
				_ = os.WriteFile(*cachePath, raw, 0o600)
			}
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "asked the provider about %d stores\n\n", asked)
	}

	var retire, protected, adds []store
	addRows := 0
	for i := range all {
		st := &all[i]
		if !search.IsHomeLivingStore(st.name, st.types) {
			if st.protected {
				protected = append(protected, *st)
				continue
			}
			st.retire = true
			retire = append(retire, *st)
			continue
		}
		desired := map[string]bool{}
		for _, slug := range search.StoreCategories(st.name, st.types) {
			if _, ok := slugs[slug]; !ok {
				fmt.Fprintf(os.Stderr, "unknown category %q for %q -- skipped\n", slug, st.name)
				continue
			}
			desired[slug] = true
		}
		held := map[string]bool{}
		for _, slug := range st.current {
			held[slug] = true
		}
		for slug := range desired {
			if !held[slug] {
				st.add = append(st.add, slug)
			}
		}
		// Nothing is removed, and that is a decision taken after measuring rather than
		// before. Removing whatever the classifier could not re-derive was tried first,
		// and the dry run caught it doing harm: "Yataş" lost bedding and home textile, and
		// a shop called "Nevresim Takımı" -- a duvet set -- lost bedding, because Google
		// types both of them only as home_goods_store and the name concepts hold no word
		// for a duvet. Three rows in the whole catalogue would have gone and two of them
		// were right, so the rule was buying nothing and risking real data.
		//
		// The categories that genuinely did not belong sat on businesses that are not
		// shops at all, and those are retired above. That is the honest fix for them.
		sort.Strings(st.add)
		if len(st.add) > 0 {
			adds = append(adds, *st)
			addRows += len(st.add)
		}
	}

	writeReport(*report, all)
	fmt.Printf(`alignment
  stores in the catalogue      %4d
  to retire (not home/living)  %4d
  kept despite the verdict     %4d  (carry a review, favourite or visit)
  gaining a category           %4d  (%d rows)
  losing a category               0  (nothing is ever removed; see the source)

full store-by-store decision: %s
`, len(all), len(retire), len(protected), len(adds), addRows, *report)

	sample("to retire", retire, 12)
	sample("kept despite the verdict", protected, 12)
	sample("gaining a category", adds, 8)

	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)

	// Keep what the provider told us so this never has to be asked again, merged into the
	// existing record rather than replacing it: that record also holds the rating, the
	// photo reference and its required credits.
	storedTypes := 0
	for _, st := range all {
		if len(st.types) == 0 || st.placeID == "" {
			continue
		}
		raw, err := json.Marshal(map[string]any{"types": st.types})
		must(err)
		tag, err := tx.Exec(ctx, `
UPDATE store_external_sources SET attribution = attribution || $2::jsonb
WHERE store_id=$1::uuid AND provider='google' AND NOT attribution ? 'types'`, st.id, raw)
		must(err)
		storedTypes += int(tag.RowsAffected())
	}

	inserted, retired := 0, 0
	for _, st := range all {
		if st.retire {
			tag, err := tx.Exec(ctx, `UPDATE stores SET deleted_at=now() WHERE id=$1::uuid AND deleted_at IS NULL`, st.id)
			must(err)
			retired += int(tag.RowsAffected())
			continue
		}
		for _, slug := range st.add {
			tag, err := tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) VALUES($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, st.id, slugs[slug])
			must(err)
			inserted += int(tag.RowsAffected())
		}
	}
	must(tx.Commit(ctx))
	fmt.Printf("\nretired %d stores, added %d category rows, stored types for %d stores.\n", retired, inserted, storedTypes)
}

func sample(title string, list []store, n int) {
	if len(list) == 0 {
		return
	}
	fmt.Printf("\n%s -- %d, showing %d:\n", title, len(list), min(n, len(list)))
	for i, st := range list {
		if i >= n {
			break
		}
		detail := strings.Join(st.types, ",")
		if len(st.add) > 0 {
			detail = "+" + strings.Join(st.add, ",")
		}
		fmt.Printf("  %-44s %s\n", trim(st.name, 44), trim(detail, 70))
	}
}

// writeReport keeps the whole decision on disk. Nobody should have to read five hundred
// lines to approve this, but the lines have to exist: a summary nobody can check is not
// evidence of anything.
func writeReport(path string, all []store) {
	var b strings.Builder
	for _, st := range all {
		verdict := "keep"
		if st.retire {
			verdict = "RETIRE"
		}
		fmt.Fprintf(&b, "%s\t%s\tnow=%s\tadd=%s\ttypes=%s\n",
			verdict, st.name, strings.Join(st.current, ","), strings.Join(st.add, ","),
			strings.Join(st.types, ","))
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o600)
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
