// Package location answers "where should I search?" from a table this product owns.
//
// It replaces a paid autocomplete. Two things follow from that and are worth stating,
// because both were properties of the thing it replaces and both had to be kept:
//
//   - A prediction still carries no coordinates the client can choose. The browser sends
//     back an id, and Resolve reads the point out of this table. Nothing a person types
//     becomes the point a search runs against.
//   - The list is Turkish administrative places only. The product searches Turkish home
//     and living stores; a picker that could return a village in Belgium was a defect of
//     the provider, not a feature.
package location

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Result is the wire shape the location picker has always received. Provider names this
// product rather than a third party; PlaceID carries our own row id, which is stable
// because it is derived from the published registry ids rather than from a row number.
type Result struct {
	Provider     string   `json:"provider"`
	PlaceID      string   `json:"place_id"`
	Name         string   `json:"name"`
	Address      string   `json:"address"`
	Latitude     float64  `json:"latitude"`
	Longitude    float64  `json:"longitude"`
	Types        []string `json:"types"`
	Attributions []string `json:"attributions"`
}

const provider = "bosagezme"

// Kinds are published as the `types` array so a client can tell a province from a
// neighbourhood without parsing the label.
var kindTypes = map[string][]string{
	"il":      {"administrative_area_level_1", "locality"},
	"ilce":    {"administrative_area_level_2", "locality"},
	"mahalle": {"neighborhood", "sublocality"},
}

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

// Search returns places whose name begins with, or contains, what has been typed.
//
// The ordering is the whole design. A person typing four letters wants the largest,
// nearest, most obvious answer first, and there are 70,000 rows here of which several
// hundred are called some form of "Yeni" or "Merkez". So: names that start with the
// fragment beat names that merely contain it; a province beats a district beats a
// neighbourhood; and inside a tie, population decides. Coordinates, when the caller
// offers them, only break ties further -- where somebody is standing is a hint about
// which "Merkez" they mean, never a filter on what they may choose.
func (s *Service) Search(ctx context.Context, query string, limit int, lat, lon *float64) ([]Result, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 120 || limit < 1 || limit > 10 {
		return nil, httpapi.ErrInvalidInput
	}
	key := textnorm.Key(query)
	if key == "" {
		return nil, httpapi.ErrInvalidInput
	}
	// Two letters match several thousand rows and say almost nothing, so they are answered
	// from the front of names only: "an" should offer Ankara and Antalya, not every place
	// in Turkey with those letters somewhere inside it. From three letters the trigram
	// index can serve a mid-name fragment, which is what makes "unca" find "Uncalı".
	prefixOnly := utf8.RuneCountInString(key) < 3
	rows, e := s.db.Query(ctx, `
SELECT id,kind,name,province_name,coalesce(district_name,''),ST_Y(location::geometry),ST_X(location::geometry)
FROM tr_locations
WHERE search_key LIKE $1 || '%' OR (NOT $5 AND search_key LIKE '%' || $1 || '%')
ORDER BY
  CASE WHEN search_key = $1 THEN 0 WHEN search_key LIKE $1 || '%' THEN 1 ELSE 2 END,
  CASE kind WHEN 'il' THEN 0 WHEN 'ilce' THEN 1 ELSE 2 END,
  CASE WHEN $2::float8 IS NULL OR $3::float8 IS NULL THEN 0
       ELSE ST_Distance(location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END,
  population DESC,
  name
LIMIT $4`, key, lat, lon, limit, prefixOnly)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	results := make([]Result, 0, limit)
	for rows.Next() {
		var id, kind, name, province, district string
		var latitude, longitude float64
		if e = rows.Scan(&id, &kind, &name, &province, &district, &latitude, &longitude); e != nil {
			return nil, e
		}
		results = append(results, build(id, kind, name, province, district, latitude, longitude))
	}
	return results, rows.Err()
}

// Resolve turns a chosen prediction into the point a search is run against. The id is the
// only thing the client sends; everything else is read here.
func (s *Service) Resolve(ctx context.Context, id string) (Result, error) {
	id = strings.TrimSpace(id)
	if id == "" || utf8.RuneCountInString(id) > 300 {
		return Result{}, httpapi.ErrInvalidInput
	}
	var kind, name, province, district string
	var latitude, longitude float64
	e := s.db.QueryRow(ctx, `
SELECT kind,name,province_name,coalesce(district_name,''),ST_Y(location::geometry),ST_X(location::geometry)
FROM tr_locations WHERE id=$1`, id).Scan(&kind, &name, &province, &district, &latitude, &longitude)
	if e != nil {
		// A prediction that cannot be read back is not a server fault: the id came from
		// the client, and an unknown one means the client chose something this table does
		// not have. The picker already knows how to say so.
		if errors.Is(e, pgx.ErrNoRows) {
			return Result{}, httpapi.E(422, "INVALID_LOCATION", "The selected location could not be verified")
		}
		return Result{}, e
	}
	return build(id, kind, name, province, district, latitude, longitude), nil
}

// build assembles the two strings a person reads. The name is the place's own; the
// address is everything above it, largest last -- "Meltem" then "Muratpaşa, Antalya" --
// which is how a Turkish address is spoken and how the picker has always rendered it.
func build(id, kind, name, province, district string, lat, lon float64) Result {
	var parents []string
	switch kind {
	case "mahalle":
		parents = []string{district, province}
	case "ilce":
		parents = []string{province}
	}
	types := kindTypes[kind]
	return Result{
		Provider:     provider,
		PlaceID:      id,
		Name:         name,
		Address:      strings.Join(parents, ", "),
		Latitude:     lat,
		Longitude:    lon,
		Types:        append([]string{}, types...),
		Attributions: []string{},
	}
}
