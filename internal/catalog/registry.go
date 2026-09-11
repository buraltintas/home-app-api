package catalog

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

//go:embed data/brands.yaml
var registryFile embed.FS

// registryEntry is one brand as the file describes it.
type registryEntry struct {
	Slug       string   `yaml:"slug"`
	Name       string   `yaml:"name"`
	Website    string   `yaml:"website"`
	Tier       int      `yaml:"tier"`
	Categories []string `yaml:"categories"`
	Estimate   int      `yaml:"store_count_estimate"`
	// Inactive is how the registry records a brand we have decided not to carry, with the
	// reason beside it. Deleting the entry would lose that reason and invite the next
	// person to add it back; the registry is meant to list what we decided as well as what
	// we import. Absent means active, so every existing entry keeps its meaning.
	Inactive *bool `yaml:"active"`
	Locator  struct {
		Kind string `yaml:"kind"`
		// Everything else in the block is the locator's own configuration, kept as
		// written so that adding a field to JSONLocatorConfig needs no change here.
		Rest map[string]any `yaml:",inline"`
	} `yaml:"locator"`
}

// LoadRegistry reads the shipped brand list and writes it into the brands table, so the
// file stays the source of truth and the table stays the thing everything else reads.
//
// It updates rather than replaces: a brand row carries things the file does not know --
// its logo, when it was last imported, whether somebody switched it off -- and those must
// survive a change of category or a new locator URL.
func LoadRegistry(ctx context.Context, db *pgxpool.Pool) (int, error) {
	body, e := registryFile.ReadFile("data/brands.yaml")
	if e != nil {
		return 0, e
	}
	var document struct {
		Brands []registryEntry `yaml:"brands"`
	}
	if e = yaml.Unmarshal(body, &document); e != nil {
		return 0, e
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, entry := range document.Brands {
		if entry.Slug == "" || entry.Name == "" {
			return 0, fmt.Errorf("brand registry entry without a slug or name: %+v", entry)
		}
		kind := entry.Locator.Kind
		if kind == "" {
			kind = "none"
		}
		config, e := json.Marshal(entry.Locator.Rest)
		if e != nil {
			return 0, e
		}
		tier := entry.Tier
		if tier < 1 || tier > 3 {
			tier = 3
		}
		var estimate any
		if entry.Estimate > 0 {
			estimate = entry.Estimate
		}
		if _, e = tx.Exec(ctx, `
INSERT INTO brands(slug,name,website,tier,locator_kind,locator_config,category_profile,store_count_estimate,active)
VALUES($1,$2,nullif($3,''),$4,$5,$6,$7,$8,$9)
ON CONFLICT(slug) DO UPDATE SET
  name=EXCLUDED.name,
  website=coalesce(EXCLUDED.website,brands.website),
  tier=EXCLUDED.tier,
  locator_kind=EXCLUDED.locator_kind,
  locator_config=EXCLUDED.locator_config,
  category_profile=EXCLUDED.category_profile,
  store_count_estimate=coalesce(EXCLUDED.store_count_estimate,brands.store_count_estimate),
  active=EXCLUDED.active,
  updated_at=now()`,
			entry.Slug, entry.Name, entry.Website, tier, kind, config, entry.Categories, estimate, entry.Inactive == nil || *entry.Inactive); e != nil {
			return 0, fmt.Errorf("brand %s: %w", entry.Slug, e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return len(document.Brands), nil
}

// Brands reads the registry back out of the database, tier first, which is the order an
// import must run in.
func Brands(ctx context.Context, db *pgxpool.Pool, slug string) ([]BrandSpec, error) {
	rows, e := db.Query(ctx, `
SELECT id::text,slug,name,coalesce(website,''),coalesce(category_profile,'{}'),locator_kind,locator_config,tier
FROM brands
WHERE active AND ($1='' OR slug=$1)
ORDER BY tier, slug`, slug)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []BrandSpec
	for rows.Next() {
		var spec BrandSpec
		var config []byte
		if e = rows.Scan(&spec.ID, &spec.Slug, &spec.Name, &spec.Website, &spec.CategoryProfile, &spec.LocatorKind, &config, &spec.Tier); e != nil {
			return nil, e
		}
		spec.LocatorConfig = config
		out = append(out, spec)
	}
	return out, rows.Err()
}

// SourceFor builds the adapter a brand's registry entry describes. A brand whose locator
// has not been mapped yet answers with no source rather than an error: the registry lists
// what exists, and the work still to do is part of what it lists.
func SourceFor(spec BrandSpec, fetcher *Fetcher) (Source, bool, error) {
	switch spec.LocatorKind {
	case "html":
		locator, e := NewHTMLLocator(spec, fetcher)
		if e != nil {
			return nil, false, e
		}
		return locator, true, nil
	case "json":
		source, e := NewJSONLocator(spec, fetcher)
		if e != nil {
			return nil, false, e
		}
		return source, true, nil
	case "", "none":
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("%s: locator kind %q is not implemented", spec.Slug, spec.LocatorKind)
}
