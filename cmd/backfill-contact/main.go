// Command backfill-contact fills in the phone number and the website of stores that were
// imported before either was read.
//
// Both are asked for in the field mask the import already uses, and a store that turns up
// in a search again has them filled in on the spot. That is enough for anything found
// since; it is no help at all to a store nobody has searched for since. Those stores sit
// in the catalogue with an empty website while Google Maps shows one, which is exactly the
// discrepancy that was reported.
//
// Everything here is deliberately narrow, because it runs against the production database:
//
//   - Only stores with an empty value are considered. A store that already holds a number
//     or an address is never read, never updated, never touched: a store that told us its
//     own number knows it better than the directory does.
//   - The only writes are UPDATE stores SET phone/website, each guarded a second time in
//     the statement itself, so a row that gained a value between the read and the write is
//     still left alone.
//   - Nothing is inserted and nothing is deleted.
//   - It prints what it would do and stops. Applying requires -apply, and even then the
//     whole thing is one transaction.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contact struct {
	Website string `json:"website"`
	Phone   string `json:"phone"`
}

type candidate struct {
	id, name, placeID string
	hasWebsite        bool
	hasPhone          bool
	website, phone    string
}

func main() {
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	fetch := flag.Bool("fetch", false, "ask Google for the contact details of candidates that have none cached")
	cachePath := flag.String("cache", "/tmp/store-contact.json", "where fetched details are kept, so a dry run and an apply share one fetch")
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

	// Fetched details are cached on disk on purpose. A dry run and the apply that follows
	// it must not each cost a round of provider calls, and re-running after a mistake must
	// not either. Delete the file to force a refetch.
	cache := map[string]contact{}
	if raw, err := os.ReadFile(*cachePath); err == nil {
		_ = json.Unmarshal(raw, &cache)
	}

	// Only stores that are missing something and that we can ask about. A store with no
	// Google record cannot be answered by Google, so it is not a candidate at all.
	rows, err := db.Query(ctx, `
SELECT s.id::text, s.name,
       x.external_id,
       coalesce(s.website,'') <> '',
       coalesce(s.phone,'') <> ''
FROM stores s
JOIN store_external_sources x ON x.store_id = s.id AND x.provider = 'google'
WHERE s.deleted_at IS NULL
  AND (coalesce(s.website,'') = '' OR coalesce(s.phone,'') = '')
ORDER BY s.name`)
	must(err)
	var pending []candidate
	for rows.Next() {
		var c candidate
		must(rows.Scan(&c.id, &c.name, &c.placeID, &c.hasWebsite, &c.hasPhone))
		if known, ok := cache[c.placeID]; ok {
			c.website, c.phone = known.Website, known.Phone
		}
		pending = append(pending, c)
	}
	rows.Close()
	must(rows.Err())
	missingWebsite, missingPhone := 0, 0
	for _, c := range pending {
		if !c.hasWebsite {
			missingWebsite++
		}
		if !c.hasPhone {
			missingPhone++
		}
	}
	fmt.Fprintf(os.Stderr, "stores missing something: %d (%d without a website, %d without a number)\n", len(pending), missingWebsite, missingPhone)

	// Ask the provider only for the ones we have nothing cached for, and only when asked
	// to. Every call costs money and the answers do not change from one minute to the
	// next, so they are written to the cache as they arrive -- an interrupted run resumes
	// rather than starting the bill again.
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
			pending[i].website, pending[i].phone = place.Website, place.Phone
			cache[pending[i].placeID] = contact{Website: place.Website, Phone: place.Phone}
			if raw, err := json.MarshalIndent(cache, "", " "); err == nil {
				_ = os.WriteFile(*cachePath, raw, 0o600)
			}
			// Unhurried on purpose. This is a one-off backfill, not a hot path, and there
			// is nothing to gain from arriving at a rate limit.
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "asked the provider about %d stores\n\n", asked)
	}

	var found []candidate
	websites, phones, unanswered := 0, 0, 0
	for _, c := range pending {
		if c.hasWebsite || !validWebsite(c.website) {
			c.website = ""
		}
		if c.hasPhone {
			c.phone = ""
		}
		c.phone = strings.TrimSpace(c.phone)
		if c.website == "" && c.phone == "" {
			unanswered++
			continue
		}
		if c.website != "" {
			websites++
		}
		if c.phone != "" {
			phones++
		}
		found = append(found, c)
	}

	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	for _, c := range found {
		fmt.Printf("%-42s %-34s %s\n", trim(c.name, 42), trim(c.website, 34), c.phone)
	}
	fmt.Printf("\nstores to update: %d (%d websites, %d numbers)\nstores the provider had nothing for: %d\n", len(found), websites, phones, unanswered)

	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)

	updatedWebsites, updatedPhones := 0, 0
	for _, c := range found {
		// Guarded again in the statement. Between the read above and this write a store
		// may have gained its own value -- from a search that happened to find it, or from
		// somebody correcting it -- and that value wins over the directory's.
		if c.website != "" {
			tag, err := tx.Exec(ctx, `UPDATE stores SET website=$2 WHERE id=$1::uuid AND coalesce(website,'')=''`, c.id, c.website)
			must(err)
			updatedWebsites += int(tag.RowsAffected())
		}
		if c.phone != "" {
			tag, err := tx.Exec(ctx, `UPDATE stores SET phone=$2 WHERE id=$1::uuid AND coalesce(phone,'')=''`, c.id, c.phone)
			must(err)
			updatedPhones += int(tag.RowsAffected())
		}
	}
	must(tx.Commit(ctx))
	fmt.Printf("\nfilled in %d websites and %d numbers.\n", updatedWebsites, updatedPhones)
}

// validWebsite keeps out anything that is not a web address a person can open. The
// provider is trusted for the value itself -- whatever Google Maps shows beside the place
// is what the store chose to publish -- but the shape is checked here, because the value
// ends up in an href.
func validWebsite(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
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
