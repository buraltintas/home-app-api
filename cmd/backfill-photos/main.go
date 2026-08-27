// Command backfill-photos gives a store its photograph when the provider has one and we
// never went back to look.
//
// A store's photo reference is captured when it is imported and refreshed whenever a
// search turns the store up again. That covers everything anyone has looked for; it covers
// nothing for a store imported before it had a photograph and never searched for since.
// Google's photographs arrive over time -- somebody visits and uploads one -- so a store
// that had none on the day it was imported may well have ten today. Twelve did.
//
// Everything here is deliberately narrow, because it runs against the production database:
//
//   - Only stores whose stored record holds no photo reference are read at all.
//   - The only write merges photo_name and photo_attributions into the existing
//     attribution, guarded a second time in the statement, so a record that gained a
//     photograph between the read and the write is left alone. Nothing else in that
//     record -- the rating, the credits, the types -- is touched.
//   - Nothing is inserted and nothing is deleted.
//   - It prints what it would do and stops. Applying requires -apply, and even then the
//     whole thing is one transaction.
//
// Google requires the attributions that come with a photograph to be displayed with it, so
// they are stored together and never separately.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

type photo struct {
	Name         string   `json:"name"`
	Attributions []string `json:"attributions"`
}

type candidate struct {
	id, name, placeID string
	photo             photo
}

func main() {
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	fetch := flag.Bool("fetch", false, "ask Google for the photographs of stores that have none stored")
	cachePath := flag.String("cache", "/tmp/store-photos.json", "where fetched references are kept, so a dry run and an apply share one fetch")
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

	cache := map[string]photo{}
	if raw, err := os.ReadFile(*cachePath); err == nil {
		_ = json.Unmarshal(raw, &cache)
	}

	rows, err := db.Query(ctx, `
SELECT s.id::text, s.name, x.external_id
FROM stores s
JOIN store_external_sources x ON x.store_id = s.id AND x.provider = 'google'
WHERE s.deleted_at IS NULL AND NOT (x.attribution ? 'photo_name')
ORDER BY s.name`)
	must(err)
	var pending []candidate
	for rows.Next() {
		var c candidate
		must(rows.Scan(&c.id, &c.name, &c.placeID))
		if known, ok := cache[c.placeID]; ok {
			c.photo = known
		}
		pending = append(pending, c)
	}
	rows.Close()
	must(rows.Err())
	fmt.Fprintf(os.Stderr, "stores showing no photograph: %d\n", len(pending))

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
			found := photo{Name: place.PhotoName, Attributions: place.PhotoAttributions}
			pending[i].photo = found
			cache[pending[i].placeID] = found
			if raw, err := json.MarshalIndent(cache, "", " "); err == nil {
				_ = os.WriteFile(*cachePath, raw, 0o600)
			}
			// Unhurried on purpose. This is a one-off backfill, not a hot path.
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "asked the provider about %d stores\n\n", asked)
	}

	var found []candidate
	for _, c := range pending {
		// A photo name that is not a photo name is not written anywhere near an href.
		if c.photo.Name == "" || !search.ValidPhotoName(c.photo.Name) {
			continue
		}
		found = append(found, c)
	}
	for _, c := range found {
		fmt.Printf("  %-46s %d credit(s)\n", trim(c.name, 46), len(c.photo.Attributions))
	}
	fmt.Printf("\nstores that gain a photograph: %d\nstores the provider has none for: %d\n", len(found), len(pending)-len(found))

	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)
	written := 0
	for _, c := range found {
		raw, err := json.Marshal(map[string]any{
			"photo_name":         c.photo.Name,
			"photo_attributions": c.photo.Attributions,
		})
		must(err)
		tag, err := tx.Exec(ctx, `
UPDATE store_external_sources SET attribution = attribution || $2::jsonb
WHERE store_id = $1::uuid AND provider = 'google' AND NOT (attribution ? 'photo_name')`, c.id, raw)
		must(err)
		written += int(tag.RowsAffected())
	}
	must(tx.Commit(ctx))
	fmt.Printf("\ngave %d stores their photograph.\n", written)
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
