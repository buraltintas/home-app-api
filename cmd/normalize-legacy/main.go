// normalize-legacy prepares the stores already in the catalogue to be matched against.
//
// Every row imported from a provider carries that provider's spelling: shouted names,
// city and district as they appeared in a formatted address, and no folded form at all.
// "ENGLISH HOME KADIKÖY AVM" and "English Home Kadıköy" are the same shop and nothing in
// the database says so. Until this has run, the importer's matcher is comparing against
// text it cannot read, and it will cheerfully create a second copy of every chain store we
// already hold.
//
// So it runs once before the first import, and again whenever rows arrive from somewhere
// that does not fold names itself. Dry run by default, like every tool here.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/catalog"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
)

func main() {
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	limit := flag.Int("sample", 12, "how many examples to print")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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

	rows, e := db.Query(ctx, `SELECT id::text,name,coalesce(city,''),coalesce(district,''),coalesce(address,''),coalesce(compact_name,''),ST_Y(location::geometry),ST_X(location::geometry),coalesce(location_from,'') FROM stores WHERE deleted_at IS NULL ORDER BY created_at`)
	if e != nil {
		log.Fatal(e)
	}
	type change struct {
		id                            string
		name                          string
		compact                       string
		city, district                string
		oldCity, oldDistrict, oldName string
	}
	var changes []change
	var total int
	for rows.Next() {
		var c change
		var address string
		var latitude, longitude *float64
		var pointFrom string
		if e = rows.Scan(&c.id, &c.oldName, &c.oldCity, &c.oldDistrict, &address, &c.compact, &latitude, &longitude, &pointFrom); e != nil {
			log.Fatal(e)
		}
		// A point we put there ourselves cannot say where the shop is: it was derived from
		// the place in the first place, and reading the place back off it is a circle. It
		// put Vivense's Kepez shop in Muratpaşa, because that is what is nearest the middle
		// of Antalya, and then the next import could not recognise its own row.
		derived := catalog.Derived(pointFrom)
		if derived {
			latitude, longitude = nil, nil
		}
		total++
		c.name = catalog.TidyName(c.oldName)
		c.compact = catalog.CompactName(c.name)
		place := resolver.ResolveAt(c.oldCity, c.oldDistrict, address, c.oldName, latitude, longitude)
		c.city, c.district = place.City, place.District
		// A place the administrative table cannot confirm is left exactly as it was. A
		// wrong district is worse than an untidy one: the catalogue is grouped by it.
		if c.city == "" {
			c.city, c.district = c.oldCity, c.oldDistrict
		} else if c.district == "" {
			c.district = c.oldDistrict
		}
		// A row standing on a coordinate we invented has a district that may have been read
		// off that same coordinate -- which is how Vivense's Kepez shop came to be filed in
		// Muratpaşa, and the stored value now looks exactly like a published one. Its own
		// name is the way back: it says Kepez, and a district named in the row's own text
		// beats one derived from a point we placed.
		if derived && c.city != "" {
			if named := resolver.DistrictInText(c.city, c.oldName+" "+address); named != "" {
				c.district = named
			}
		}
		changes = append(changes, c)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		log.Fatal(e)
	}

	var renamed, replaced int
	shown := 0
	for _, c := range changes {
		nameChanged := c.name != c.oldName
		placeChanged := c.city != c.oldCity || c.district != c.oldDistrict
		if nameChanged {
			renamed++
		}
		if placeChanged {
			replaced++
		}
		if (nameChanged || placeChanged) && shown < *limit {
			shown++
			fmt.Printf("  %-46s -> %-46s  %s/%s -> %s/%s\n", trim(c.oldName), trim(c.name), c.oldCity, c.oldDistrict, c.city, c.district)
		}
	}
	// Broken out, because "places canonicalised" covers two very different things: a
	// district spelled the way the administrative table spells it, and a district this row
	// did not have at all. The second is the one worth watching, and a district that was
	// already readable and comes out different is the one worth stopping for.
	var gainedCity, gainedDistrict, movedDistrict int
	var moved []change
	for _, c := range changes {
		if c.oldCity == "" && c.city != "" {
			gainedCity++
		}
		if c.oldDistrict == "" && c.district != "" {
			gainedDistrict++
		}
		if c.oldDistrict != "" && c.district != "" && c.district != c.oldDistrict &&
			!strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(c.oldDistrict, c.oldCity)), c.district) {
			movedDistrict++
			moved = append(moved, c)
		}
	}
	fmt.Printf("%d stores: %d names retitled, %d places canonicalised, %d compact names written\n", total, renamed, replaced, len(changes))
	fmt.Printf("  of those: %d given a city they did not have, %d given a district they did not have, %d moved to a different district\n",
		gainedCity, gainedDistrict, movedDistrict)
	// These are printed in full however few they are: a row that is being taken out of the
	// district it was filed under is the one change here a person should look at.
	for _, c := range moved {
		fmt.Printf("  moved: %-44s %s/%s -> %s/%s\n", trim(c.oldName), c.oldCity, c.oldDistrict, c.city, c.district)
	}

	if !*apply {
		fmt.Println("dry run; pass -apply to write")
		return
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, c := range changes {
		if _, e = tx.Exec(ctx, `UPDATE stores SET name=$2,compact_name=$3,city=$4,district=nullif($5,''),updated_at=now() WHERE id=$1`,
			c.id, c.name, c.compact, c.city, c.district); e != nil {
			log.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	fmt.Println("written")
}

func trim(value string) string {
	runes := []rune(value)
	if len(runes) <= 44 {
		return value
	}
	return string(runes[:43]) + "…"
}
