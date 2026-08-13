package main

import (
	"context"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"log"
	"time"
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
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer tx.Rollback(ctx)
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM searches WHERE created_at < now()-($1::int*interval '1 day')`, []any{c.SearchRetentionDays}},
		{`DELETE FROM searches WHERE user_id IS NULL AND visitor_session_id IN (SELECT id FROM visitor_sessions WHERE expires_at < now() OR last_seen_at < now()-($1::int*interval '1 day'))`, []any{c.VisitorRetentionDays}},
		{`DELETE FROM visitor_sessions WHERE expires_at < now() OR last_seen_at < now()-($1::int*interval '1 day')`, []any{c.VisitorRetentionDays}},
		{`DELETE FROM email_verification_codes WHERE created_at < now()-interval '30 days'`, nil},
		{`DELETE FROM email_outbox WHERE created_at < now()-interval '90 days' AND status IN ('sent','failed')`, nil},
		{`UPDATE searches SET request_latitude=NULL,request_longitude=NULL WHERE created_at < now()-($1::int*interval '1 day')`, []any{c.SearchLocationRetentionDays}},
	}
	for _, statement := range statements {
		if _, e = tx.Exec(ctx, statement.query, statement.args...); e != nil {
			log.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	reportSvc, e := reporting.NewService(db, c.ReportingTimezone, c.SearchAttributionWindow)
	if e != nil {
		log.Fatal(e)
	}
	if e = reportSvc.RebuildSnapshot(ctx); e != nil {
		log.Fatal(e)
	}
	log.Print("privacy retention completed")
}
