// Command merge-stores joins two rows that are one shop, from the command line.
//
// The panel is where this normally happens, with the two rows shown side by side -- that is
// the part that stops the wrong one being pressed. This exists for the cases the panel is
// awkward for: a cleanup pass over rows found by a query, and the first exercise of a piece
// of code that deletes things, which ought to happen somewhere a person can read the result
// rather than the first time somebody presses a button in production.
//
// It runs the same service the panel's button does, so what is tested here is what ships.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/burakaltintas/home-app-api/internal/admin"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/google/uuid"
)

func main() {
	keep := flag.String("keep", "", "id of the store that survives; everything is moved onto it")
	drop := flag.String("merge", "", "id of the store merged into it")
	who := flag.String("as", "", "email of the administrator this merge is recorded against")
	apply := flag.Bool("apply", false, "perform the merge; without it the two rows are only printed")
	flag.Parse()

	keepID, e := uuid.Parse(*keep)
	if e != nil {
		log.Fatal("-keep must be a store id")
	}
	dropID, e := uuid.Parse(*drop)
	if e != nil {
		log.Fatal("-merge must be a store id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()

	show := func(label string, id uuid.UUID) {
		var name, address, city, kind string
		var posts, favorites, sources int
		if e := db.QueryRow(ctx, `
SELECT s.name, coalesce(s.address,''), s.city, s.source_kind,
       (SELECT count(*) FROM posts WHERE store_id=s.id AND deleted_at IS NULL),
       (SELECT count(*) FROM favorites WHERE store_id=s.id),
       (SELECT count(*) FROM store_external_sources WHERE store_id=s.id)
  FROM stores s WHERE s.id=$1 AND s.deleted_at IS NULL`, id).
			Scan(&name, &address, &city, &kind, &posts, &favorites, &sources); e != nil {
			log.Fatalf("%s: %v", label, e)
		}
		fmt.Printf("%-6s %s\n       %s, %s · %s · %d yorum, %d favori, %d kaynak\n", label, name, address, city, kind, posts, favorites, sources)
	}
	show("kalan", keepID)
	show("giden", dropID)

	if !*apply {
		fmt.Println("dry run; pass -apply to merge")
		return
	}
	// A merge is recorded against the person who decided it, the same as one made from the
	// panel. There is no house account to hide behind: an unattributable entry in the audit
	// log is worse than no tool, because the next person reading it cannot ask anybody what
	// they were looking at.
	var actorID uuid.UUID
	if e := db.QueryRow(ctx, `SELECT id FROM users WHERE primary_email=$1 AND deleted_at IS NULL`, *who).Scan(&actorID); e != nil {
		log.Fatalf("-as must be the email of an existing administrator: %v", e)
	}
	if e := admin.NewService(db).MergeStores(ctx, actorID, *who, keepID, dropID); e != nil {
		log.Fatal(e)
	}
	fmt.Println("merged")
}
