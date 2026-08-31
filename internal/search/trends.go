package search

import "context"

const (
	popularCityWindowDays  = 30
	popularCityMinSearches = 3
	popularCityMaxLimit    = 10
)

// PopularCity is a coarse, privacy-safe search trend. Counts are aggregated by city;
// districts, coordinates, visitors and raw queries never leave the reporting store.
type PopularCity struct {
	Name        string `json:"name"`
	SearchCount int64  `json:"search_count"`
}

// PopularCities returns the cities where completed searches happened most often during
// the rolling month. A city must cross a small public threshold so one person's location
// never appears as a trend by itself.
func (s *Service) PopularCities(ctx context.Context, limit int) ([]PopularCity, error) {
	if limit < 1 {
		limit = 5
	}
	if limit > popularCityMaxLimit {
		limit = popularCityMaxLimit
	}
	rows, err := s.db.Query(ctx, `SELECT min(btrim(search_city)) AS city, count(*)
FROM searches
WHERE status='completed'
  AND created_at >= now()-($1::int * interval '1 day')
  AND nullif(btrim(search_city),'') IS NOT NULL
GROUP BY lower(btrim(search_city))
HAVING count(*) >= $2
ORDER BY count(*) DESC, lower(min(btrim(search_city)))
LIMIT $3`, popularCityWindowDays, popularCityMinSearches, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PopularCity{}
	for rows.Next() {
		var item PopularCity
		if err = rows.Scan(&item.Name, &item.SearchCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
