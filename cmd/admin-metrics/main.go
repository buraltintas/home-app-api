package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/reporting"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "rebuild" {
		log.Fatal("usage: admin-metrics rebuild")
	}
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
	svc, e := reporting.NewService(db, cfg.ReportingTimezone, cfg.SearchAttributionWindow)
	if e != nil {
		log.Fatal(e)
	}
	if e = svc.Rebuild(ctx); e != nil {
		log.Fatal(e)
	}
	log.Printf("admin metrics rebuilt using timezone %s", cfg.ReportingTimezone)
}
