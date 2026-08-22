package search

import (
	"context"
	"math"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

// A suggestion is something other people near here have actually looked for. It is shown
// as the query they typed, so it has to survive being read by a stranger.
type Suggestion struct {
	Query       string `json:"query"`
	SearchCount int64  `json:"search_count"`
}

// Two rules keep this safe to publish.
//
// A query is only offered once several different people have made it. One person's search
// can carry a name, a street or something they would not choose to say out loud; a phrase
// that many unconnected people arrived at independently is a description of a product, not
// of a person. That threshold is the whole privacy argument for this endpoint, so it lives
// here as a constant rather than as a caller-supplied parameter.
const distinctSearchers = 3

// And only queries that found something are offered. Suggesting a phrase that returns an
// empty page is worse than suggesting nothing at all.
//
// Ninety days is long enough for a quiet neighbourhood to accumulate a list and short
// enough that last winter's searches are not offered in July.
const suggestionWindowDays = 90

// Roughly a city. Beyond this the answer stops being "near you" and turns into a national
// average, which is exactly what the seasonal list already covers.
const suggestionRadiusKm = 30.0

// Bounding box rather than a true radius: it uses the plain btree index on the coordinate
// columns, and a suggestion list does not need the corners trimmed.
func boundingBox(latitude, longitude float64) (minLat, maxLat, minLon, maxLon float64) {
	latSpan := suggestionRadiusKm / 111.0
	cos := math.Cos(latitude * math.Pi / 180)
	if cos < 0.01 {
		cos = 0.01
	}
	lonSpan := suggestionRadiusKm / (111.0 * cos)
	return latitude - latSpan, latitude + latSpan, longitude - lonSpan, longitude + lonSpan
}

// NearbySuggestions returns what people around this point have been searching for, most
// searched first. It answers with an empty list rather than an error: the caller shows a
// seasonal list when there is nothing local yet, and an outage here must not take the
// search page down with it.
func (s *Service) NearbySuggestions(ctx context.Context, latitude, longitude float64, limit int) ([]Suggestion, error) {
	if limit <= 0 || limit > 100 {
		limit = 60
	}
	minLat, maxLat, minLon, maxLon := boundingBox(latitude, longitude)
	rows, e := s.db.Query(ctx, `
SELECT (array_agg(raw_query ORDER BY created_at DESC))[1] AS shown, count(*) AS searches
FROM searches
WHERE created_at >= now() - make_interval(days => $1)
  AND request_latitude BETWEEN $2 AND $3
  AND request_longitude BETWEEN $4 AND $5
  AND total_result_count > 0
  AND char_length(normalized_query) BETWEEN 3 AND 60
  AND coalesce(query_language::text,'tr') = $6
GROUP BY normalized_query
HAVING count(DISTINCT coalesce(user_id::text, visitor_session_id::text)) >= $7
ORDER BY searches DESC, shown
LIMIT $8`,
		suggestionWindowDays, minLat, maxLat, minLon, maxLon, string(i18n.FromContext(ctx)), distinctSearchers, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Suggestion{}
	for rows.Next() {
		var x Suggestion
		if e = rows.Scan(&x.Query, &x.SearchCount); e != nil {
			return nil, e
		}
		x.Query = strings.TrimSpace(x.Query)
		if x.Query != "" {
			out = append(out, x)
		}
	}
	return out, rows.Err()
}
