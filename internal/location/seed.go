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
//
// The rows land in a staging table of plain numbers first. COPY speaks the binary protocol,
// and a geography column expects EWKB there -- handing it the text "SRID=4326;POINT(...)"
// is read as binary and fails with an endian error. Building the point in SQL, from two
// float8 columns, is both correct and the form PostGIS is happiest parsing.
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

	if _, e = tx.Exec(ctx, `
CREATE TEMP TABLE tr_locations_load(
  id text, kind text, name text, search_key text, parent_id text,
  province_name text, district_name text,
  latitude double precision, longitude double precision, population integer
) ON COMMIT DROP`); e != nil {
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
		return []any{line[0], line[1], line[2], line[3], parent, line[5], district, latitude, longitude, population}, nil
	})
	copied, e := tx.CopyFrom(ctx, pgx.Identifier{"tr_locations_load"},
		[]string{"id", "kind", "name", "search_key", "parent_id", "province_name", "district_name", "latitude", "longitude", "population"}, source)
	if e != nil {
		return 0, e
	}
	if failed != nil {
		return 0, failed
	}
	if copied == 0 {
		return 0, fmt.Errorf("dataset is empty")
	}
	if _, e = tx.Exec(ctx, `DELETE FROM tr_locations`); e != nil {
		return 0, e
	}
	// Parents before children, so the foreign key holds row by row without having to be
	// made deferrable for a load that happens twice a year.
	if _, e = tx.Exec(ctx, `
INSERT INTO tr_locations(id,kind,name,search_key,parent_id,province_name,district_name,location,population)
SELECT id,kind,name,search_key,parent_id,province_name,nullif(district_name,''),
       ST_SetSRID(ST_MakePoint(longitude,latitude),4326)::geography,population
FROM tr_locations_load
ORDER BY CASE kind WHEN 'il' THEN 0 WHEN 'ilce' THEN 1 ELSE 2 END`); e != nil {
		return 0, e
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return copied, nil
}
