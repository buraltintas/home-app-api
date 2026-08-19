// Assigns categories to stores that have none.
//
// Stores imported from Google were never given categories: the ones shown beside a result
// came from the search that happened to find the store. So a shop plainly named "... HALI"
// carried no carpet category and could not be matched by anything filtering on one, which
// is what stopped promoted stores appearing in the searches they were promoted for.
//
// Imports now assign categories at the point of import. This is the same logic applied to
// what is already in the database.
//
// Run with --apply to write. Without it, it only reports what it would do, because a
// backfill that cannot be previewed is a backfill nobody should run.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/google/uuid"
)

func main() {
	apply := flag.Bool("apply", false, "write the categories; without this the run only reports")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	db, e := database.Open(ctx, c.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()

	// Only stores with no categories at all. One that already has them was categorised by
	// something that knew more than a name, and this must not overwrite that.
	rows, e := db.Query(ctx, `SELECT s.id,s.name FROM stores s
 WHERE s.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM store_category_links l WHERE l.store_id=s.id)
 ORDER BY s.created_at`)
	if e != nil {
		log.Fatal(e)
	}
	type candidate struct {
		id         uuid.UUID
		name       string
		categories []string
	}
	var found []candidate
	var untouched int
	for rows.Next() {
		var x candidate
		if e = rows.Scan(&x.id, &x.name); e != nil {
			log.Fatal(e)
		}
		// Types are not stored, so only the name is available here. Imports use both.
		x.categories = search.StoreCategories(x.name, nil)
		if len(x.categories) == 0 {
			untouched++
			continue
		}
		found = append(found, x)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		log.Fatal(e)
	}

	for _, x := range found {
		log.Printf("%-52s -> %v", trim(x.name), x.categories)
	}
	log.Printf("%d stores would be categorised, %d left alone because their name says nothing", len(found), untouched)

	if !*apply {
		log.Print("dry run; pass --apply to write")
		return
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer tx.Rollback(ctx)
	for _, x := range found {
		if _, e = tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=ANY($2) AND active ON CONFLICT DO NOTHING`, x.id, x.categories); e != nil {
			log.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	log.Printf("categorised %d stores", len(found))
}

func trim(s string) string {
	if len([]rune(s)) > 50 {
		return string([]rune(s)[:50])
	}
	return s
}
