package location

import (
	"compress/gzip"
	"context"
	"embed"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The dataset travels inside the binary. It is about a megabyte compressed, it changes
// perhaps once a year when a district is created, and shipping it this way means a fresh
// environment has a working location picker the moment migrations have run -- with no
// download, no key and no provider to be unavailable.
//
// Regenerate with `go run ./cmd/build-locations`; provenance and licence are in
// data/README.md next to it.
//
//go:embed data/tr_locations.csv.gz
var dataset embed.FS

// Seed replaces the contents of tr_locations with the shipped dataset.
//
// Replace, not merge: this table has no local edits to protect -- every row comes from the
// file, and a district that has been dissolved should disappear rather than linger because
// an upsert had nothing to say about it. The whole thing runs in one transaction, so a
// failed load leaves the previous data in place rather than an empty picker.
func Seed(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	file, e := dataset.Open("data/tr_locations.csv.gz")
	if e != nil {
		return 0, e
	}
	defer file.Close()
	zip, e := gzip.NewReader(file)
	if e != nil {
		return 0, e
	}
	defer zip.Close()
	reader := csv.NewReader(zip)
	header, e := reader.Read()
	if e != nil {
		return 0, e
	}
	if len(header) != 10 || header[0] != "id" {
		return 0, fmt.Errorf("unexpected dataset header %v", header)
	}

	tx, e := db.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Children reference parents, so the constraint has to stand down for the length of
	// the load; the file is written parents-first but a single statement cannot promise
	// that to the planner.
	if _, e = tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM tr_locations`); e != nil {
		return 0, e
	}

	var failed error
	source := pgx.CopyFromFunc(func() ([]any, error) {
		line, e := reader.Read()
		if e == io.EOF {
			return nil, nil
		}
		if e != nil {
			failed = e
			return nil, e
		}
		latitude, e := strconv.ParseFloat(line[7], 64)
		if e != nil {
			failed = fmt.Errorf("row %s: latitude %q: %w", line[0], line[7], e)
			return nil, failed
		}
		longitude, e := strconv.ParseFloat(line[8], 64)
		if e != nil {
			failed = fmt.Errorf("row %s: longitude %q: %w", line[0], line[8], e)
			return nil, failed
		}
		population, e := strconv.Atoi(line[9])
		if e != nil {
			failed = fmt.Errorf("row %s: population %q: %w", line[0], line[9], e)
			return nil, failed
		}
		var parent, district any
		if line[4] != "" {
			parent = line[4]
		}
		if line[6] != "" {
			district = line[6]
		}
		// COPY cannot call ST_MakePoint, so the point is handed over in the text form
		// PostGIS parses on input.
		point := fmt.Sprintf("SRID=4326;POINT(%.6f %.6f)", longitude, latitude)
		return []any{line[0], line[1], line[2], line[3], parent, line[5], district, point, population}, nil
	})
	copied, e := tx.CopyFrom(ctx, pgx.Identifier{"tr_locations"},
		[]string{"id", "kind", "name", "search_key", "parent_id", "province_name", "district_name", "location", "population"}, source)
	if e != nil {
		return 0, e
	}
	if failed != nil {
		return 0, failed
	}
	if copied == 0 {
		return 0, fmt.Errorf("dataset is empty")
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return copied, nil
}
