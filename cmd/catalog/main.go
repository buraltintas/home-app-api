// catalog imports a brand's own published store list into this product's catalogue.
//
//	go run ./cmd/catalog -registry          reload the shipped brand registry
//	go run ./cmd/catalog -list              what is registered, and what has a locator
//	go run ./cmd/catalog -source english-home          dry run: what it would do, and why
//	go run ./cmd/catalog -source english-home -apply   write it
//	go run ./cmd/catalog -tier 1 -apply                every mapped tier-1 brand, in order
//
// Dry run is the default. The matcher's verdict for every published row is printed and
// stored, because "why is this shop listed twice" has to be answerable a month later.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"github.com/burakaltintas/home-app-api/internal/catalog"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
)

func main() {
	slug := flag.String("source", "", "brand slug to import; empty means every mapped brand")
	tier := flag.Int("tier", 0, "only brands in this tier (1, 2 or 3)")
	apply := flag.Bool("apply", false, "write the changes; without it nothing is written")
	reload := flag.Bool("registry", false, "reload the shipped brand registry into the database and exit")
	list := flag.Bool("list", false, "list registered brands and exit")
	verbose := flag.Int("show", 15, "how many per-row decisions to print")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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

	if *reload {
		count, e := catalog.LoadRegistry(ctx, db)
		if e != nil {
			log.Fatal(e)
		}
		fmt.Printf("registry loaded: %d brands\n", count)
		return
	}

	brands, e := catalog.Brands(ctx, db, *slug)
	if e != nil {
		log.Fatal(e)
	}
	if len(brands) == 0 {
		log.Fatal("no brands registered; run with -registry first")
	}

	if *list {
		writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(writer, "SLUG\tNAME\tLOCATOR\tCATEGORIES")
		for _, brand := range brands {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%v\n", brand.Slug, brand.Name, brand.LocatorKind, brand.CategoryProfile)
		}
		_ = writer.Flush()
		return
	}

	resolver, e := catalog.NewResolver(ctx, db)
	if e != nil {
		log.Fatal(e)
	}
	fetcher := catalog.NewFetcher()
	importer := catalog.NewImporter(db, resolver)

	var ran int
	for _, brand := range brands {
		if *tier > 0 && brand.Tier != *tier {
			continue
		}
		source, ok, e := catalog.SourceFor(brand, fetcher)
		if e != nil {
			log.Printf("%s: %v", brand.Slug, e)
			continue
		}
		if !ok {
			continue
		}
		report, e := importer.Run(ctx, source, *apply)
		if e != nil {
			log.Printf("%s: %v", brand.Slug, e)
			continue
		}
		ran++
		fmt.Println(report)
		printDecisions(report, *verbose)
	}
	if ran == 0 {
		fmt.Println("nothing to do: no registered brand has a mapped store locator yet")
	}
}

// printDecisions shows the rows that changed something and every row a person has to look
// at. Unchanged rows are counted, not listed: a re-import of a settled chain is hundreds of
// lines saying nothing happened.
func printDecisions(report catalog.Report, limit int) {
	shown := 0
	for _, d := range report.Decisions {
		if d.Action == catalog.ActionUnchanged {
			continue
		}
		if d.Action != catalog.ActionReview && shown >= limit {
			continue
		}
		shown++
		fmt.Printf("  %-12s %-42s %s\n", d.Action, trim(d.Raw.Name), d.Reason)
	}
	if report.Review > 0 {
		fmt.Printf("  %d row(s) need a person to decide; they are in store_import_records for run %s\n", report.Review, report.RunID)
	}
}

func trim(value string) string {
	runes := []rune(value)
	if len(runes) <= 40 {
		return value
	}
	return string(runes[:39]) + "…"
}
