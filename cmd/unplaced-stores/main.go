// Command unplaced-stores settles the stores the catalogue cannot put anywhere.
//
// A store with no city answers no search for a city, appears on no city page, and is
// counted as coverage it does not provide. The importer has refused such rows since
// `placePoint` learned to: a row with nothing naming a place and a coordinate that no
// Turkish town is near is marked Outside and, if it is already in the catalogue, retired.
//
// That rule only ever ran against rows arriving from a publisher. Rows imported before it
// existed are still here, and nothing re-reads them until their brand is imported again --
// which for a brand whose next run would put hundreds of rows in front of a person is not
// a thing to do for the sake of a handful of stores.
//
// So the same question is asked of what is already stored. Each store with no city is
// offered to the resolver with everything the row holds -- its name, its address and its
// own coordinate. If that produces a city the store is placed and kept; if it does not,
// the store cannot be placed in Turkey at all and is retired the way the importer would
// retire it. Soft-deleted, so anything anybody wrote about it survives the decision.
//
//	go run ./cmd/unplaced-stores           what it would do, and why
//	go run ./cmd/unplaced-stores -apply    write it
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/burakaltintas/home-app-api/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	id, name, address, district string
	latitude, longitude         *float64
	city, placedDistrict        string
}

func main() {
	apply := flag.Bool("apply", false, "write the placements and retirements; without it nothing is written")
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

	resolver, err := catalog.NewResolver(ctx, db)
	must(err)

	rows, err := db.Query(ctx, `SELECT id::text, name, coalesce(address,''), coalesce(district,''),
 ST_Y(location::geometry), ST_X(location::geometry)
 FROM stores WHERE deleted_at IS NULL AND btrim(coalesce(city,'')) = '' ORDER BY name`)
	must(err)
	var placeable, unplaceable []store
	for rows.Next() {
		var s store
		must(rows.Scan(&s.id, &s.name, &s.address, &s.district, &s.latitude, &s.longitude))
		place := resolver.ResolveAt(s.city, s.district, s.address, s.name, s.latitude, s.longitude)
		if place.City != "" {
			s.city, s.placedDistrict = place.City, place.District
			placeable = append(placeable, s)
			continue
		}
		unplaceable = append(unplaceable, s)
	}
	rows.Close()
	must(rows.Err())

	for _, s := range placeable {
		fmt.Printf("place   %-46s -> %s / %s\n", trim(s.name, 46), s.city, s.placedDistrict)
	}
	for _, s := range unplaceable {
		where := "no coordinate either"
		if s.latitude != nil && s.longitude != nil {
			where = fmt.Sprintf("published at %.5f, %.5f -- no Turkish town near it", *s.latitude, *s.longitude)
		}
		fmt.Printf("retire  %-46s    %s\n", trim(s.name, 46), where)
	}
	fmt.Printf("\n%d store(s) can be placed, %d cannot and would be retired.\n", len(placeable), len(unplaceable))
	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)
	for _, s := range placeable {
		_, err = tx.Exec(ctx, `UPDATE stores SET city=$2, district=coalesce(nullif($3,''),district), updated_at=now() WHERE id=$1::uuid`,
			s.id, s.city, s.placedDistrict)
		must(err)
	}
	for _, s := range unplaceable {
		_, err = tx.Exec(ctx, `UPDATE stores SET deleted_at=now(), updated_at=now() WHERE id=$1::uuid AND deleted_at IS NULL`, s.id)
		must(err)
	}
	must(tx.Commit(ctx))
	fmt.Printf("\nplaced %d, retired %d.\n", len(placeable), len(unplaceable))
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
