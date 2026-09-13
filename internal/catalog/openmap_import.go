package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Importing shops from the public map.
//
// Deliberately its own path rather than a brand wearing a disguise. The brand importer
// carries a chain's authority: it renames a shop into the chain's own form, writes the
// chain's website onto it, marks it verified and files it under the chain's categories.
// None of that is true of a shop on the public map, and bending the brand importer into
// pretending otherwise would have put that authority behind rows that do not have it.
//
// What is shared is the part worth sharing: the resolver that turns a point into a town, and
// the same three-way decision the matcher makes -- this is a shop we hold, this might be,
// this is new -- with the same thresholds, so a person reading the review queue sees one kind
// of question rather than two.
type OpenMapImporter struct {
	db       *pgxpool.Pool
	resolver *Resolver
}

func NewOpenMapImporter(db *pgxpool.Pool, resolver *Resolver) *OpenMapImporter {
	return &OpenMapImporter{db: db, resolver: resolver}
}

// How near, and how alike, before two rows are the same shop. The same numbers the brand
// matcher uses, for the same reason: a person settling the middle band should not have to
// learn a second set.
const (
	openMapSameMeters     = 150
	openMapSameSimilarity = 0.55
	openMapReviewMinimum  = 0.40
)

func (i *OpenMapImporter) Run(ctx context.Context, source *OpenMapSource, apply bool) (Report, error) {
	report := Report{Brand: "OpenStreetMap · " + source.Province(), Applied: apply}

	rows, e := source.Fetch(ctx)
	if e != nil {
		return report, e
	}
	report.Fetched = len(rows)

	tx, e := i.db.Begin(ctx)
	if e != nil {
		return report, e
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One province at a time, like one brand at a time, and for the same reason: two runs of
	// the same province both see an empty catalogue for each shop and both insert.
	var mine bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('openmap-import:'||$1))`, source.Province()).Scan(&mine); e != nil {
		return report, e
	}
	if !mine {
		return report, fmt.Errorf("%s is already being imported by another run", source.Province())
	}

	var runID string
	if e = tx.QueryRow(ctx, `INSERT INTO store_import_runs(brand_id,applied,fetched) VALUES(NULL,$1,$2) RETURNING id::text`,
		apply, len(rows)).Scan(&runID); e != nil {
		return report, e
	}
	report.RunID = runID

	seen := map[string]bool{}
	claimed := map[string]bool{}
	for _, raw := range rows {
		row := i.resolver.Normalize(raw)
		// The shop's own sign, cased the way a person writes it. No chain name in front and
		// no town appended: this shop is not a branch of anything, and its name is the whole
		// of what it is called.
		row.Name = TidyName(row.Name)
		row.Categories = raw.Categories
		if row.Name == "" || row.ExternalID == "" {
			continue
		}
		if seen[row.ExternalID] {
			continue
		}
		seen[row.ExternalID] = true

		decision, e := i.decide(ctx, tx, row, claimed)
		if e != nil {
			return report, e
		}
		if apply {
			if decision, e = i.write(ctx, tx, decision); e != nil {
				return report, e
			}
		}
		switch decision.Action {
		case ActionInserted:
			report.Inserted++
		case ActionUpdated:
			report.Updated++
		case ActionReview:
			report.Review++
		default:
			report.Skipped++
		}
		report.Decisions = append(report.Decisions, decision)
		if e = record(ctx, tx, runID, row, decision); e != nil {
			return report, e
		}
	}

	if _, e = tx.Exec(ctx, `UPDATE store_import_runs SET finished_at=now(),inserted=$2,updated=$3,review=$4,skipped=$5 WHERE id=$1`,
		runID, report.Inserted, report.Updated, report.Review, report.Skipped); e != nil {
		return report, e
	}
	if e = tx.Commit(ctx); e != nil {
		return report, e
	}
	return report, nil
}

// decide answers the only question that matters here: do we already hold this shop.
func (i *OpenMapImporter) decide(ctx context.Context, tx pgx.Tx, row RawStore, claimed map[string]bool) (Decision, error) {
	d := Decision{Raw: row}

	// The map's own identifier, which outlives edits to the shop's name and tags. A row we
	// have seen before is the same row however it has been retagged since.
	var known string
	e := tx.QueryRow(ctx, `SELECT store_id::text FROM store_external_sources WHERE provider=$1 AND external_id=$2`, OpenMapProvider, row.ExternalID).Scan(&known)
	if e == nil {
		d.Action, d.StoreID, d.Reason = ActionUpdated, known, "same shop, matched on the map's own identifier"
		claimed[known] = true
		return d, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return d, e
	}

	if row.Latitude == nil || row.Longitude == nil {
		d.Action, d.Reason = ActionSkipped, "no usable point"
		return d, nil
	}

	var id, name, compact string
	var sim, metres float64
	var branded bool
	e = tx.QueryRow(ctx, `
SELECT id::text, name, compact_name, similarity(compact_name,$1), ST_Distance(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography), brand_id IS NOT NULL
  FROM stores
 WHERE deleted_at IS NULL AND compact_name <> ''
   AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography, $4)
 ORDER BY greatest(similarity(compact_name,$1),
                   CASE WHEN length($1)>=6 AND length(compact_name)>=6
                         AND (compact_name LIKE '%'||$1||'%' OR $1 LIKE '%'||compact_name||'%')
                        THEN 1 ELSE 0 END) DESC,
          similarity(compact_name,$1) DESC
 LIMIT 1`, CompactName(row.Name), *row.Latitude, *row.Longitude, openMapSameMeters).Scan(&id, &name, &compact, &sim, &metres, &branded)
	if errors.Is(e, pgx.ErrNoRows) {
		d.Action, d.Reason = ActionInserted, "no comparable shop within 150 m"
		return d, nil
	}
	if e != nil {
		return d, e
	}
	d.Similarity, d.Distance = sim, metres

	// A dealer's row is mostly the dealer's own name: "Bellona - İstanbul Eyüpsultan Balcı
	// Mobilya" against the map's plain "Bellona" shares so little of its length that trigram
	// similarity reads 0.17 and calls them different shops. They are one shop, one metre
	// apart, and the sign over the door says so. The brand importer has always treated one
	// name holding the other whole as identity; this path did not, and 130 duplicates are
	// what that cost. Same rule, same place, one definition.
	held := containment(CompactName(row.Name), compact)

	switch {
	case (sim >= openMapSameSimilarity || held) && !claimed[id]:
		// Already in the catalogue, from a chain's own list or from an operator. The map is
		// not the authority on it, so nothing about the shop is overwritten -- the only
		// thing recorded is that this map object is that shop, so the next run recognises it
		// without asking again.
		d.Action, d.StoreID = ActionUpdated, id
		why := fmt.Sprintf("%.2f", sim)
		if held {
			why = "one name holds the other whole"
		}
		d.Reason = fmt.Sprintf("already in the catalogue as %q (%s, %.0f m); recorded as the same shop", name, why, metres)
		claimed[id] = true
	case sim >= openMapReviewMinimum:
		d.Action = ActionReview
		d.Reason = fmt.Sprintf("resembles %q (%.2f, %.0f m) but not closely enough to call it the same shop", name, sim, metres)
	default:
		d.Action = ActionInserted
		what := "an independent shop"
		if branded {
			what = "a branch of a chain"
		}
		d.Reason = fmt.Sprintf("nearest shop within 150 m is %s under another name (%q, %.2f)", what, name, sim)
	}
	return d, nil
}

func (i *OpenMapImporter) write(ctx context.Context, tx pgx.Tx, d Decision) (Decision, error) {
	switch d.Action {
	case ActionInserted:
		id := uuid.New()
		if _, e := tx.Exec(ctx, `
INSERT INTO stores(id,name,slug,address,city,district,location,phone,website,compact_name,source_kind,data_verified_at,is_catalog_store,location_from)
VALUES($1,$2,$3,$4,$5,$6,ST_SetSRID(ST_MakePoint($8,$7),4326)::geography,nullif($9,''),nullif($10,''),$11,'osm',NULL,false,$12)`,
			id, d.Raw.Name, storeSlug(d.Raw.Name, id), d.Raw.Address, d.Raw.City, d.Raw.District,
			*d.Raw.Latitude, *d.Raw.Longitude, d.Raw.Phone, d.Raw.Website, CompactName(d.Raw.Name), d.Raw.PointFrom); e != nil {
			return d, e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO store_stats(store_id) VALUES($1)`, id); e != nil {
			return d, e
		}
		if e := linkCategories(ctx, tx, id.String(), d.Raw.Categories); e != nil {
			return d, e
		}
		d.StoreID = id.String()
		return d, i.linkSource(ctx, tx, d.StoreID, d.Raw)

	case ActionUpdated:
		// Nothing about the shop is rewritten. Whoever put it here -- a chain's own list, an
		// operator, or an earlier read of the map -- knows more about it than a map object
		// does, and a row that flips between two spellings on every import is worse than one
		// that is slightly stale.
		return d, i.linkSource(ctx, tx, d.StoreID, d.Raw)
	}
	return d, nil
}

func (i *OpenMapImporter) linkSource(ctx context.Context, tx pgx.Tx, storeID string, row RawStore) error {
	attribution, _ := json.Marshal(map[string]any{
		"licence": "ODbL",
		"source":  "OpenStreetMap contributors",
		"read_at": time.Now().UTC().Format(time.RFC3339),
	})
	_, e := tx.Exec(ctx, `
INSERT INTO store_external_sources(store_id,provider,external_id,attribution,refreshed_at)
VALUES($1,$2,$3,$4,now())
ON CONFLICT (provider,external_id) DO UPDATE SET store_id=excluded.store_id,attribution=excluded.attribution,refreshed_at=now()`,
		storeID, OpenMapProvider, row.ExternalID, attribution)
	return e
}
