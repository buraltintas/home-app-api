package search

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	highlightWindowDays     = 30
	highlightMinimumReviews = 5
)

// StoreHighlight is a store whose recent community activity is strong enough to
// recommend publicly. The threshold is deliberately fixed: a quiet store should
// not be presented as a trend on the strength of one or two reviews.
type StoreHighlight struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	City              string    `json:"city"`
	District          string    `json:"district,omitempty"`
	AverageRating     float64   `json:"average_rating"`
	ReviewCount       int       `json:"review_count"`
	RecentReviewCount int       `json:"recent_review_count"`
	RatingIncrease    float64   `json:"rating_increase,omitempty"`
}

type MonthlyStoreHighlights struct {
	RatingGainer *StoreHighlight `json:"rating_gainer,omitempty"`
	MostReviewed *StoreHighlight `json:"most_reviewed,omitempty"`
}

// MonthlyHighlights returns at most one store for each agreed monthly signal:
// the largest rating increase and the most reviews. A store needs at least five
// active reviews overall to be eligible. Missing signals remain nil so clients
// can omit the corresponding section instead of showing weak placeholder data.
func (s *Service) MonthlyHighlights(ctx context.Context) (MonthlyStoreHighlights, error) {
	windowStart := s.now().Add(-highlightWindowDays * 24 * time.Hour)
	base := `
WITH review_stats AS (
  SELECT s.id, s.name, s.city, coalesce(s.district, '') AS district,
         count(p.id) FILTER (WHERE p.deleted_at IS NULL) AS review_count,
         count(p.id) FILTER (WHERE p.deleted_at IS NULL AND p.created_at >= $1) AS recent_review_count,
         avg(p.rating::double precision) FILTER (WHERE p.deleted_at IS NULL) AS current_rating,
         avg(p.rating::double precision) FILTER (
           WHERE p.created_at < $1 AND (p.deleted_at IS NULL OR p.deleted_at >= $1)
         ) AS prior_rating
  FROM stores s
  LEFT JOIN posts p ON p.store_id = s.id
  WHERE s.deleted_at IS NULL
  GROUP BY s.id, s.name, s.city, s.district
)
SELECT id, name, city, district, current_rating, review_count, recent_review_count,
       coalesce(current_rating - prior_rating, 0) AS rating_increase
FROM review_stats
WHERE review_count >= $2 AND recent_review_count > 0 `

	out := MonthlyStoreHighlights{}
	ratingQuery := base + `
  AND prior_rating IS NOT NULL
  AND current_rating > prior_rating
ORDER BY rating_increase DESC, recent_review_count DESC, review_count DESC, id
LIMIT 1`
	rating, err := scanHighlight(s.db.QueryRow(ctx, ratingQuery, windowStart, highlightMinimumReviews))
	if err != nil {
		return out, err
	}
	out.RatingGainer = rating

	mostReviewedQuery := base + `
ORDER BY recent_review_count DESC, current_rating DESC, review_count DESC, id
LIMIT 1`
	mostReviewed, err := scanHighlight(s.db.QueryRow(ctx, mostReviewedQuery, windowStart, highlightMinimumReviews))
	if err != nil {
		return out, err
	}
	out.MostReviewed = mostReviewed
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHighlight(row rowScanner) (*StoreHighlight, error) {
	var item StoreHighlight
	if err := row.Scan(
		&item.ID,
		&item.Name,
		&item.City,
		&item.District,
		&item.AverageRating,
		&item.ReviewCount,
		&item.RecentReviewCount,
		&item.RatingIncrease,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}
