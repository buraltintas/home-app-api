package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Report is what one run did, in the form a person reads before deciding to apply it.
type Report struct {
	RunID     string
	Brand     string
	Applied   bool
	Fetched   int
	Inserted  int
	Updated   int
	Unchanged int
	Review    int
	Skipped   int
	Decisions []Decision
}

func (r Report) String() string {
	mode := "dry run"
	if r.Applied {
		mode = "applied"
	}
	return fmt.Sprintf("%s (%s): %d fetched, %d new, %d updated, %d unchanged, %d to review, %d skipped",
		r.Brand, mode, r.Fetched, r.Inserted, r.Updated, r.Unchanged, r.Review, r.Skipped)
}

// Importer runs one brand's list end to end: fetch, normalise, match, and -- only when
// asked -- write.
//
// Dry run is the default everywhere in this repository's tools, and here it is more than a
// convention: the matcher's verdicts are the thing worth reading, and a run that has
// already written them is a run nobody looked at.
type Importer struct {
	db       *pgxpool.Pool
	resolver *Resolver
}

func NewImporter(db *pgxpool.Pool, resolver *Resolver) *Importer {
	return &Importer{db: db, resolver: resolver}
}

func (i *Importer) Run(ctx context.Context, source Source, apply bool) (Report, error) {
	brand := source.Brand()
	report := Report{Brand: brand.Name, Applied: apply}

	published, e := source.Fetch(ctx)
	if e != nil {
		return report, fmt.Errorf("fetch %s: %w", brand.Slug, e)
	}
	report.Fetched = len(published)

	tx, e := i.db.Begin(ctx)
	if e != nil {
		return report, e
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One import of a brand at a time, across every process that might start one -- the
	// command line, and the panel's "import now" button once it exists. Two runs of the
	// same brand overlapping both see an empty catalogue for each shop, both decide to
	// insert, and the chain lands twice; it happened here, to Kelebek Mobilya, with the two
	// runs fifty-three seconds apart. The lock is held for the transaction and released
	// with it, so a crashed run does not block the next one.
	var mine bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('catalog-import:'||$1))`, brand.Slug).Scan(&mine); e != nil {
		return report, e
	}
	if !mine {
		return report, fmt.Errorf("%s is already being imported by another run", brand.Slug)
	}

	var runID string
	if e = tx.QueryRow(ctx, `INSERT INTO store_import_runs(brand_id,applied,fetched) VALUES($1,$2,$3) RETURNING id::text`,
		brand.ID, apply, len(published)).Scan(&runID); e != nil {
		return report, e
	}
	report.RunID = runID

	// The chain's own shorthand, measured from this run's names against the rest of the
	// catalogue, so branch names carry what names a shop and not what the chain files it
	// under.
	published0 := make([]string, 0, len(published))
	for _, raw := range published {
		published0 = append(published0, raw.Name)
	}
	house, e := HouseWords(ctx, tx, published0, brand.ID)
	if e != nil {
		return report, e
	}
	// Which of this chain's capital I's it means as İ, learned from the rows where it wrote
	// the dot itself.
	spellings := Spellings(published0)

	// How much of this brand already shares a name, measured before anything is written.
	twinsBefore, e := twinNames(ctx, tx, brand.ID)
	if e != nil {
		return report, e
	}

	matcher := NewMatcher(tx)
	seen := map[string]bool{}
	// A row thrown out before matching is recorded like any other decision. It used to be
	// counted and nothing more, so a dry run that rejected every row said only "515
	// skipped" and left the reason to be guessed at -- which is the opposite of what a dry
	// run is for.
	reject := func(row RawStore, reason string) error {
		report.Skipped++
		decision := Decision{Raw: row, Action: ActionSkipped, Reason: reason}
		report.Decisions = append(report.Decisions, decision)
		return record(ctx, tx, runID, row, decision)
	}
	for _, raw := range published {
		raw.Name = RepairSpelling(raw.Name, spellings)
		// Derived from what the publisher wrote, before any of our naming touches it. It
		// used to be derived from the finished display name, which is ours -- so every time
		// the naming rules improved, the identifier of every shop of every chain that
		// publishes none changed with them, and the next import inserted the lot again.
		// Ninety-nine shops stood duplicated at zero metres because of it.
		derived := DerivedID(raw)
		row := i.resolver.Normalize(raw)
		// The whole of naming, here rather than in the adapter, because only here are both
		// facts known: which chain this is, and which real town the row resolved to. The
		// order matters -- the city code comes off while the name is still shouted, casing
		// follows, then the chain's own filing words, then what is missing.
		row.Name = DisplayName(brand.Name, StripHouseWords(TidyName(StripPlaceCode(row.Name, row.City)), house), row.City, row.District)
		// Not every chain publishes an id for every shop. Where one is missing, it is
		// derived from what the row itself says, so the same shop derives the same id on
		// every run and a re-import updates it instead of adding a second copy. Without
		// this a locator like Bellona's -- which publishes an id for a handful of its
		// branches and nothing for the other five hundred -- loses almost all of them.
		if row.ExternalID == "" {
			row.ExternalID = derived
		}
		// A list that repeats a shop is the publisher's problem, not ours; the first one
		// wins and the rest are recorded as skipped rather than fought over.
		if seen[row.ExternalID] {
			if e = reject(row, "repeated in the published list"); e != nil {
				return report, e
			}
			continue
		}
		seen[row.ExternalID] = true

		// A chain's own list is not a promise that everything on it belongs here.
		if row.Outside {
			if e = reject(row, "published at a point outside Turkey"); e != nil {
				return report, e
			}
			continue
		}
		if NamesAnApparelDepartment(row.Name) {
			if e = reject(row, "the branch name says this department sells clothing, not homeware"); e != nil {
				return report, e
			}
			continue
		}

		decision, e := matcher.Match(ctx, brand, row)
		if e != nil {
			return report, e
		}
		if strings.HasPrefix(row.PointFrom, "placed at the centre") {
			decision.Reason = decision.Reason + "; " + row.PointFrom
		}
		if apply {
			if decision, e = i.apply(ctx, tx, brand, decision); e != nil {
				return report, e
			}
		}
		switch decision.Action {
		case ActionInserted:
			report.Inserted++
		case ActionUpdated:
			report.Updated++
		case ActionUnchanged:
			report.Unchanged++
		case ActionReview:
			report.Review++
		case ActionSkipped:
			report.Skipped++
		}
		report.Decisions = append(report.Decisions, decision)
		if e = record(ctx, tx, runID, row, decision); e != nil {
			return report, e
		}
	}

	// An import that makes duplication worse takes itself back.
	//
	// A net under a mistake already made: the identifier derived for a chain that publishes
	// none used to be hashed from the name we build, so improving the naming rules changed
	// every such shop's identity and the next run inserted the lot again -- 448 shops, in
	// one pass, with nobody watching.
	//
	// It compares before with after rather than demanding none, which is the version that
	// first shipped and was wrong in a way worth recording: a brand whose list genuinely
	// repeats a dealer already had twins, so the guard refused every run of it -- including
	// the run carrying the fix that would have told those dealers apart. A rule that blocks
	// its own remedy is not a safety net.
	twinsAfter := twinsBefore
	if apply {
		if twinsAfter, e = twinNames(ctx, tx, brand.ID); e != nil {
			return report, e
		}
	}
	if apply && twinsAfter > twinsBefore {
		return report, fmt.Errorf("%s: this run would leave %d name(s) on more than one shop, up from %d; rolled back",
			brand.Slug, twinsAfter, twinsBefore)
	}

	if _, e = tx.Exec(ctx, `UPDATE store_import_runs SET finished_at=now(),inserted=$2,updated=$3,review=$4,skipped=$5 WHERE id=$1`,
		runID, report.Inserted, report.Updated, report.Review, report.Skipped); e != nil {
		return report, e
	}
	if apply {
		if _, e = tx.Exec(ctx, `UPDATE brands SET last_imported_at=now(),updated_at=now() WHERE id=$1`, brand.ID); e != nil {
			return report, e
		}
	}
	// The run itself is kept either way. A dry run is evidence too: it is how a bad
	// adapter is noticed before it writes anything.
	if e = tx.Commit(ctx); e != nil {
		return report, e
	}
	return report, nil
}

func record(ctx context.Context, tx pgx.Tx, runID string, row RawStore, d Decision) error {
	raw := row.Raw
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var store any
	if d.StoreID != "" {
		store = d.StoreID
	}
	_, e := tx.Exec(ctx, `INSERT INTO store_import_records(run_id,external_id,name,raw,matched_store_id,action,reason,similarity,distance_meters)
VALUES($1,$2,$3,$4,$5,$6,$7,nullif($8,0),nullif($9,0))`,
		runID, row.ExternalID, row.Name, raw, store, string(d.Action), d.Reason, d.Similarity, d.Distance)
	return e
}

// apply writes one decision. Inserting and updating are deliberately different: an update
// never overwrites what the community or an administrator put there, and never moves a
// store the brand did not give a point for.
func (i *Importer) apply(ctx context.Context, tx pgx.Tx, brand BrandSpec, d Decision) (Decision, error) {
	switch d.Action {
	case ActionInserted:
		id := uuid.New()
		if d.Raw.Latitude == nil || d.Raw.Longitude == nil {
			// Without a point a store cannot be found by a search that orders by distance,
			// which is the only kind this product runs. Better absent than unfindable.
			d.Action, d.Reason = ActionSkipped, "published without coordinates and no existing store to attach to"
			return d, nil
		}
		if _, e := tx.Exec(ctx, `
INSERT INTO stores(id,name,slug,brand_name,brand_id,address,city,district,location,phone,website,compact_name,source_kind,data_verified_at,is_catalog_store,location_from)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,ST_SetSRID(ST_MakePoint($10,$9),4326)::geography,nullif($11,''),nullif($12,''),$13,'brand_locator',now(),true,$14)`,
			id, d.Raw.Name, storeSlug(d.Raw.Name, id), brand.Name, brand.ID, d.Raw.Address, d.Raw.City, d.Raw.District,
			*d.Raw.Latitude, *d.Raw.Longitude, d.Raw.Phone, firstNonEmpty(d.Raw.Website, brand.Website), CompactName(d.Raw.Name), d.Raw.PointFrom); e != nil {
			return d, e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO store_stats(store_id) VALUES($1)`, id); e != nil {
			return d, e
		}
		if e := linkCategories(ctx, tx, id.String(), brand.CategoryProfile); e != nil {
			return d, e
		}
		if e := linkSource(ctx, tx, id.String(), brand, d.Raw); e != nil {
			return d, e
		}
		d.StoreID = id.String()
		return d, nil

	case ActionUpdated:
		// A shop that turned out to hold two franchises is recorded as carrying this brand
		// and otherwise left exactly as it is. The chain being imported is not the authority
		// on a dealer whose sign is somebody else's: overwriting the name and address here
		// would make the shop flip between two chains' spellings on every import.
		if d.Carried {
			if _, e := tx.Exec(ctx, `INSERT INTO store_carried_brands(store_id,brand_id,source) VALUES($1,$2,'import') ON CONFLICT DO NOTHING`, d.StoreID, brand.ID); e != nil {
				return d, e
			}
			if e := linkSource(ctx, tx, d.StoreID, brand, d.Raw); e != nil {
				return d, e
			}
			return d, nil
		}
		// The brand is the authority on its own shop's address, telephone and position, so
		// those are replaced. The name is only replaced when the row we hold is unverified:
		// an administrator who corrected a sign should not be overruled by a locator.
		if _, e := tx.Exec(ctx, `
UPDATE stores SET
  name=CASE WHEN data_verified_at IS NULL OR source_kind='brand_locator' THEN $2 ELSE name END,
  compact_name=CASE WHEN data_verified_at IS NULL OR source_kind='brand_locator' THEN $3 ELSE compact_name END,
  brand_name=coalesce(nullif(brand_name,''),$4),
  brand_id=coalesce(brand_id,$5),
  address=coalesce(nullif($6,''),address),
  city=coalesce(nullif($7,''),city),
  district=coalesce(nullif($8,''),district),
  location=CASE WHEN $9::float8 IS NULL THEN location ELSE ST_SetSRID(ST_MakePoint($10,$9),4326)::geography END,
  location_from=CASE WHEN $9::float8 IS NULL THEN location_from ELSE $13 END,
  phone=coalesce(nullif($11,''),phone),
  website=coalesce(nullif($12,''),website),
  source_kind='brand_locator',
  data_verified_at=now(),
  is_catalog_store=true,
  updated_at=now()
WHERE id=$1`,
			d.StoreID, d.Raw.Name, CompactName(d.Raw.Name), brand.Name, brand.ID, d.Raw.Address, d.Raw.City, d.Raw.District,
			d.Raw.Latitude, d.Raw.Longitude, d.Raw.Phone, firstNonEmpty(d.Raw.Website, brand.Website), d.Raw.PointFrom); e != nil {
			return d, e
		}
		if e := linkCategories(ctx, tx, d.StoreID, brand.CategoryProfile); e != nil {
			return d, e
		}
		if e := linkSource(ctx, tx, d.StoreID, brand, d.Raw); e != nil {
			return d, e
		}
		return d, nil
	}
	// needs_review and skipped write nothing but their record.
	return d, nil
}

func linkCategories(ctx context.Context, tx pgx.Tx, storeID string, profile []string) error {
	if len(profile) == 0 {
		return nil
	}
	_, e := tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=ANY($2) AND active ON CONFLICT DO NOTHING`, storeID, profile)
	return e
}

func linkSource(ctx context.Context, tx pgx.Tx, storeID string, brand BrandSpec, row RawStore) error {
	raw := row.Raw
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	_, e := tx.Exec(ctx, `
INSERT INTO store_external_sources(store_id,provider,external_id,attribution,refreshed_at)
VALUES($1,$2,$3,$4,now())
ON CONFLICT(provider,external_id) DO UPDATE SET store_id=EXCLUDED.store_id,attribution=EXCLUDED.attribution,refreshed_at=now()`,
		storeID, brand.Provider(), row.ExternalID, raw)
	return e
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// storeSlug mirrors what the search importer produced, so a store's URL keeps the shape it
// has always had: a readable name with the row's own id on the end, which is what makes it
// unique without a lookup.
func storeSlug(name string, id uuid.UUID) string {
	base := strings.Trim(slugUnsafe.ReplaceAllString(textnorm.Key(name), "-"), "-")
	if base == "" {
		base = "magaza"
	}
	if len(base) > 60 {
		base = strings.Trim(base[:60], "-")
	}
	return base + "-" + id.String()[:8]
}

// twinNames counts how many of a brand's names sit on more than one shop.
func twinNames(ctx context.Context, tx pgx.Tx, brandID string) (int, error) {
	var count int
	e := tx.QueryRow(ctx, `
SELECT count(*) FROM (
  SELECT 1 FROM stores WHERE deleted_at IS NULL AND brand_id=$1
   GROUP BY compact_name HAVING count(*)>1) x`, brandID).Scan(&count)
	return count, e
}
