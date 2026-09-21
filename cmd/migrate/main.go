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

	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// "status" reads and changes nothing. It exists because "up" applies every migration the
	// database has not seen, not the one you have in mind -- and against a production
	// database the difference between those two matters enough to be able to look first.
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down" && os.Args[1] != "status") {
		log.Fatal("usage: migrate up|down|status")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Read straight from the environment rather than through the application's config.
	// Loading that config makes this tool demand the token signing keys, the OTP secret and
	// the BFF secrets before it will open a connection -- none of which a schema change has
	// any business holding. Running a migration should need exactly one thing: the database.
	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, e := database.Open(ctx, url)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	// Not for "status": that mode is a read, and a read should not be able to create
	// anything, not even a table that is certainly already there.
	if os.Args[1] != "status" {
		if _, e = db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
			log.Fatal(e)
		}
	}
	direction := os.Args[1]
	if direction == "status" {
		direction = "up"
	}
	files, e := filepath.Glob("migrations/*." + direction + ".sql")
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
		if os.Args[1] == "status" {
			state := "PENDING"
			if exists {
				state = "applied"
			}
			fmt.Printf("%-10s %s\n", state, filepath.Base(file))
			continue
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
