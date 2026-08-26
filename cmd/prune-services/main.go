// Command prune-services removes categories from stores that are service businesses.
//
// A renovation firm calls itself "tadilat" and puts "dekorasyon" in the name too, so it was
// classified as a decoration store and came back under searches for one. The classifier no
// longer does that, but a store's categories are worked out once, so the ones already here
// keep theirs.
//
// It deletes category links and nothing else. The store itself is untouched: it keeps its
// photo, rating and reviews, and simply stops claiming to sell things it does not sell. An
// uncategorised store already sorts below classified ones, so the effect is that it sinks
// rather than disappears.
//
// The set is decided by the same rule the classifier uses, not by a list anybody typed out.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	apply := flag.Bool("apply", false, "delete the links; without it nothing is written")
	flag.Parse()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	must(err)
	defer db.Close()

	rows, err := db.Query(ctx, `
SELECT s.id::text, s.name, coalesce(string_agg(c.slug, ',' ORDER BY c.slug), '')
FROM stores s
JOIN store_category_links l ON l.store_id = s.id
JOIN store_categories c ON c.id = l.category_id
WHERE s.deleted_at IS NULL
GROUP BY s.id, s.name
ORDER BY s.name`)
	must(err)

	type victim struct{ id, name, cats string }
	var found []victim
	for rows.Next() {
		var v victim
		must(rows.Scan(&v.id, &v.name, &v.cats))
		// The rule, not a list. A store is a service business if its own name says so.
		if !search.IsHomeLivingStore(v.name, nil) {
			found = append(found, v)
		}
	}
	rows.Close()

	for _, v := range found {
		fmt.Printf("%-52s %s\n", trim(v.name, 52), v.cats)
	}
	fmt.Printf("\n%d stores would lose their categories.\n", len(found))
	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to delete.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)
	deleted := 0
	for _, v := range found {
		tag, err := tx.Exec(ctx, `DELETE FROM store_category_links WHERE store_id = $1::uuid`, v.id)
		must(err)
		deleted += int(tag.RowsAffected())
	}
	must(tx.Commit(ctx))
	fmt.Printf("\ndeleted %d category links from %d stores.\n", deleted, len(found))
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
