package main

import (
	"context"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
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
	_, e = tx.Exec(ctx, `DELETE FROM searches WHERE created_at<now()-($1::int*interval '1 day');DELETE FROM searches WHERE user_id IS NULL AND visitor_session_id IN(SELECT id FROM visitor_sessions WHERE expires_at<now());DELETE FROM visitor_sessions WHERE expires_at<now();DELETE FROM email_verification_codes WHERE created_at<now()-interval '30 days';DELETE FROM email_outbox WHERE created_at<now()-interval '90 days' AND status IN('sent','failed');UPDATE searches SET request_latitude=NULL,request_longitude=NULL WHERE created_at<now()-interval '30 days'`, c.SearchRetentionDays)
	if e != nil {
		log.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	log.Print("privacy retention completed")
}
