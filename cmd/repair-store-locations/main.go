// Command repair-store-locations puts right the city and district of stores imported
// before the address parser existed.
//
// A Turkish address ends the way the post office writes it -- "... 45003 Yunusemre/Manisa,
// Türkiye" -- and the whole of that last component was being stored as the city. That is
// why a store page was titled with a postcode nobody asked for, and why the district
// column was empty for the entire catalogue. New imports have been correct since the
// parser landed; every store that arrived before it still carries the old value.
//
// It reads addresses we already hold. There is no provider call here and nothing is
// fetched: the information was always in the row, it was simply never separated.
//
// A store whose stored city already matches what the address says is left alone, and a
// district somebody filled in by hand is never overwritten -- only an empty one is filled.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

type repair struct {
	id, name               string
	city, district         string
	wantCity, wantDistrict string
}

func main() {
	apply := flag.Bool("apply", false, "write the corrected city and district; without it nothing is written")
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

	rows, err := db.Query(ctx, `SELECT id::text, name, coalesce(city,''), coalesce(district,''), coalesce(address,'')
 FROM stores WHERE deleted_at IS NULL AND coalesce(address,'') <> '' ORDER BY name`)
	must(err)
	var found []repair
	for rows.Next() {
		var r repair
		var address string
		must(rows.Scan(&r.id, &r.name, &r.city, &r.district, &address))
		city, district := search.CityAndDistrict(address)
		// An address that yields nothing usable tells us less than the row already holds.
		if city == "" || city == "Bilinmiyor" {
			continue
		}
		r.wantCity, r.wantDistrict = city, r.district
		if r.district == "" {
			r.wantDistrict = district
		}
		if r.wantCity == r.city && r.wantDistrict == r.district {
			continue
		}
		found = append(found, r)
	}
	rows.Close()

	for _, r := range found {
		fmt.Printf("%-46s %-34s -> %s / %s\n", trim(r.name, 46), trim(r.city+" / "+r.district, 34), r.wantCity, r.wantDistrict)
	}
	fmt.Printf("\n%d stores would have their city or district corrected.\n", len(found))
	if !*apply {
		fmt.Println("\ndry run -- nothing written. re-run with -apply to write.")
		return
	}

	tx, err := db.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)
	written := 0
	for _, r := range found {
		tag, err := tx.Exec(ctx, `UPDATE stores SET city=$2, district=$3 WHERE id=$1::uuid`, r.id, r.wantCity, r.wantDistrict)
		must(err)
		written += int(tag.RowsAffected())
	}
	must(tx.Commit(ctx))
	fmt.Printf("\ncorrected %d stores.\n", written)
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
