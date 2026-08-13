package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		log.Fatal("usage: migrate up|down")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	if _, e = db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		log.Fatal(e)
	}
	files, e := filepath.Glob("migrations/*." + os.Args[1] + ".sql")
	if e != nil {
		log.Fatal(e)
	}
	sort.Strings(files)
	if os.Args[1] == "down" {
		sort.Sort(sort.Reverse(sort.StringSlice(files)))
	}
	for _, file := range files {
		version := strings.Split(filepath.Base(file), "_")[0]
		var exists bool
		if e = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); e != nil {
			log.Fatal(e)
		}
		if os.Args[1] == "up" && !exists {
			apply(ctx, db, file, version, true)
			fmt.Println("applied", file)
		} else if os.Args[1] == "down" && exists {
			apply(ctx, db, file, version, false)
			fmt.Println("reverted", file)
			break
		}
	}
}
func apply(ctx context.Context, db *pgxpool.Pool, file, version string, up bool) {
	body, e := os.ReadFile(file)
	if e != nil {
		log.Fatal(e)
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(body)); e != nil {
		log.Fatalf("%s: %v", file, e)
	}
	if up {
		_, e = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version)
	} else {
		_, e = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, version)
	}
	if e != nil {
		log.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
}
