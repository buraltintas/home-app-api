// Runs the retention sweep once and exits. The API now performs the same sweep daily on
// its own, so this exists for a manual run or a one-off after changing a retention period.
// Both call the same code: two copies of a retention policy would eventually disagree, and
// the published pages can only match one of them.
package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/privacy"
	"github.com/burakaltintas/home-app-api/internal/reporting"
)

func main() {
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

	if e = privacy.Sweep(ctx, db, privacy.Config{
		SearchRetentionDays:         c.SearchRetentionDays,
		SearchLocationRetentionDays: c.SearchLocationRetentionDays,
		VisitorRetentionDays:        c.VisitorRetentionDays,
	}); e != nil {
		log.Fatal(e)
	}

	reportSvc, e := reporting.NewService(db, c.ReportingTimezone, c.SearchAttributionWindow)
	if e != nil {
		log.Fatal(e)
	}
	if e = reportSvc.RebuildSnapshot(ctx); e != nil {
		log.Fatal(e)
	}
	slog.Default().Info("privacy retention completed")
}
