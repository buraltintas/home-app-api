// seed-locations loads Turkey's administrative places into tr_locations.
//
// Run it once after migrations, and again whenever the shipped dataset changes. It is
// idempotent: the table is replaced, so running it twice leaves exactly the same rows.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/location"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	started := time.Now()
	rows, e := location.Seed(ctx, db)
	if e != nil {
		log.Fatal(e)
	}
	fmt.Printf("loaded %d locations in %s\n", rows, time.Since(started).Round(time.Millisecond))
}
