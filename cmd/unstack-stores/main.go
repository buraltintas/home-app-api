// Command unstack-stores takes the shops a publisher stood on one another's heads and puts
// each where its own address says.
//
// A coordinate a brand has given to a great many of its shops at once is not any of their
// addresses. Merinos gives 104 of its dealers a single point in Bursa and 45 more a single
// point in İstanbul -- its own, presumably, filled in wherever the dealer's was not known.
// Kept, it stands a hundred shops on one doorstep: they all answer "the nearest one to me"
// with the same distance, and whichever the sort puts first wins.
//
// The importer refuses such a point on the way in. This is the same rule applied to rows
// already in the catalogue, and it fetches nothing: the address was always in the row.
//
// A row moved here says so in its provenance, so the next person can tell a shop we placed
// from a shop its chain placed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/burakaltintas/home-app-api/internal/catalog"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
)

// The same threshold the importer uses. Two shops in one shopping centre can honestly share
// a point to the metre; five cannot.
const stackedLimit = 5

func main() {
	apply := flag.Bool("apply", false, "write the new points; without it nothing is written")
	sample := flag.Int("sample", 12, "how many examples to print")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
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
	resolver, e := catalog.NewResolver(ctx, db)
	if e != nil {
		log.Fatal(e)
	}

	// Counted per brand, because two chains standing at the same mall entrance is a
	// coincidence and one chain standing a hundred shops there is a placeholder.
	rows, e := db.Query(ctx, `
WITH stacked AS (
  SELECT brand_id, ST_AsText(location::geometry) AS at
    FROM stores
   WHERE deleted_at IS NULL AND brand_id IS NOT NULL AND location IS NOT NULL
     AND location_from NOT LIKE 'placed at%'
   GROUP BY 1,2 HAVING count(*) >= $1)
SELECT s.id::text, s.name, coalesce(s.city,''), coalesce(s.district,''), coalesce(s.address,''), b.name
  FROM stores s
  JOIN brands b ON b.id=s.brand_id
  JOIN stacked k ON k.brand_id=s.brand_id AND k.at=ST_AsText(s.location::geometry)
 WHERE s.deleted_at IS NULL
 ORDER BY b.name, s.name`, stackedLimit)
	if e != nil {
		log.Fatal(e)
	}
	type move struct {
		id, name, brand, where string
		lat, lon               float64
	}
	var moves []move
	var unplaceable int
	for rows.Next() {
		var m move
		var city, district, address string
		if e = rows.Scan(&m.id, &m.name, &city, &district, &address, &m.brand); e != nil {
			log.Fatal(e)
		}
		lat, lon, where, ok := resolver.FallbackPoint(city, district, address)
		if !ok {
			// Nowhere to put it: the row does not say which province it is in. Leaving it
			// where the publisher put it is wrong, but inventing a place is worse.
			unplaceable++
			continue
		}
		m.lat, m.lon, m.where = lat, lon, where
		moves = append(moves, m)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		log.Fatal(e)
	}

	byNeighbourhood := 0
	for _, m := range moves {
		if len(m.where) > 4 && m.where[:4] == "the " {
			byNeighbourhood++
		}
	}
	for i, m := range moves {
		if i >= *sample {
			break
		}
		fmt.Printf("  %-52s -> %s\n", trim(m.name), m.where)
	}
	fmt.Printf("%d shop(s) standing on a publisher's placeholder: %d placed by the neighbourhood in their address, %d by their district or province, %d left alone for want of a province\n",
		len(moves), byNeighbourhood, len(moves)-byNeighbourhood, unplaceable)

	if !*apply {
		fmt.Println("dry run; pass -apply to write")
		return
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, m := range moves {
		if _, e = tx.Exec(ctx, `
UPDATE stores
   SET location=ST_SetSRID(ST_MakePoint($3,$2),4326)::geography,
       location_from=$4,
       updated_at=now()
 WHERE id=$1`, m.id, m.lat, m.lon,
			"placed at the centre of "+m.where+"; the publisher gave this point to many of its shops at once"); e != nil {
			log.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	fmt.Println("written")
}

func trim(s string) string {
	r := []rune(s)
	if len(r) <= 50 {
		return s
	}
	return string(r[:49]) + "…"
}
