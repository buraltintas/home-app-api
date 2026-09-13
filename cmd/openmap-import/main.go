// Command openmap-import adds the independent home and living shops that no chain publishes.
//
// The catalogue was built from brands' own store lists, which is why nine tenths of it is
// branches of chains. The shops that are nobody's branch -- the carpet dealer on the corner,
// the furniture showroom known only in its own district -- publish no list for us to read.
// Measured before this was written: İstanbul held 70 of them, Ankara 3, Bursa 4.
//
// OpenStreetMap holds around two thousand in İstanbul alone. It is not a chain speaking about
// itself, so what comes from it arrives unverified and search ranks it behind what a chain has
// confirmed. Its licence is ODbL: attribution is required wherever this data is shown, and a
// derived database carries share-alike obligations -- see docs/LEGAL_REVIEW_REQUIRED.md.
//
// Dry run by default, like every tool here.
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
	provinces := flag.String("provinces", "İstanbul,Ankara,İzmir,Bursa,Antalya", "provinces to read, comma separated")
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	verbose := flag.Int("show", 12, "how many per-row decisions to print")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
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
	fetcher := catalog.NewFetcherWithTimeout(4 * time.Minute)
	importer := catalog.NewOpenMapImporter(db, resolver)

	var totals catalog.Report
	for _, province := range strings.Split(*provinces, ",") {
		province = strings.TrimSpace(province)
		if province == "" {
			continue
		}
		report, e := importer.Run(ctx, catalog.NewOpenMapSource(province, fetcher), *apply)
		if e != nil {
			// One province's failure is not the sweep's. Overpass rate-limits, and a
			// province that comes back empty today comes back tomorrow.
			log.Printf("%s: %v", province, e)
			continue
		}
		fmt.Println(report)
		printDecisions(report, *verbose)
		totals.Fetched += report.Fetched
		totals.Inserted += report.Inserted
		totals.Updated += report.Updated
		totals.Review += report.Review
		totals.Skipped += report.Skipped
	}
	fmt.Printf("toplam: %d okundu, %d yeni, %d zaten var, %d incelenecek, %d atlandı\n",
		totals.Fetched, totals.Inserted, totals.Updated, totals.Review, totals.Skipped)
	if !*apply {
		fmt.Println("dry run; pass -apply to write")
	}
}

func printDecisions(report catalog.Report, limit int) {
	if limit <= 0 {
		return
	}
	shown := 0
	for _, d := range report.Decisions {
		if d.Action == catalog.ActionUpdated || d.Action == catalog.ActionSkipped {
			continue
		}
		if shown >= limit {
			break
		}
		shown++
		fmt.Printf("  %-12s %-42s %s\n", d.Action, trim(d.Raw.Name), d.Reason)
	}
}

func trim(s string) string {
	r := []rune(s)
	if len(r) <= 40 {
		return s
	}
	return string(r[:39]) + "…"
}
