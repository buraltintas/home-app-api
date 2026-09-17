package search

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	highlightWindowDays     = 30
	highlightMinimumReviews = 5
	// Five reviews counted reviews, not people, and the two came apart the moment anybody
	// wrote more than one. A shop reached the home page on fourteen reviews that were all
	// by the same person: the arithmetic was right and the sentence underneath it -- "most
	// reviewed this month" -- was not, because it reads as a crowd agreeing.
	//
	// A standout is a claim about consensus, so it needs more than one voice. Three is the
	// smallest number that can disagree with itself.
	highlightMinimumReviewers = 3
	// How many recently reviewed shops the home page can list. Unlike a standout this makes
	// no claim beyond "somebody wrote about this one lately", so it needs no threshold --
	// and it is what gives the home page links out to store pages at all.
	recentHighlightLimit = 8
)

// StoreHighlight is a store whose recent community activity is strong enough to
// recommend publicly. The threshold is deliberately fixed: a quiet store should
// not be presented as a trend on the strength of one or two reviews.
type StoreHighlight struct {
	ID uuid.UUID `json:"id"`
	// The store's readable address. Without it a caller links by uuid and every one of
	// those links is answered with a redirect to the slug -- a hop that costs a reader
	// nothing and costs a crawler a whole fetch.
	Slug              string  `json:"slug"`
	Name              string  `json:"name"`
	City              string  `json:"city"`
	District          string  `json:"district,omitempty"`
	AverageRating     float64 `json:"average_rating"`
	ReviewCount       int     `json:"review_count"`
	RecentReviewCount int     `json:"recent_review_count"`
	// How many different people, as opposed to how many reviews. The two are the same
	// number only until somebody visits twice, and a reader deserves the one that says
	// how much agreement is behind a score.
	ReviewerCount  int     `json:"reviewer_count"`
	RatingIncrease float64 `json:"rating_increase,omitempty"`
	// The same photograph the result list shows, chosen the same way: a community
	// picture first, then the provider's. A store recommended without a face beside it
	// reads as a line of text rather than a place.
	Photo *Photo `json:"photo,omitempty"`
}

type MonthlyStoreHighlights struct {
	RatingGainer *StoreHighlight `json:"rating_gainer,omitempty"`
	MostReviewed *StoreHighlight `json:"most_reviewed,omitempty"`
	// The shops written about most recently, newest first. Not a ranking and not a
	// recommendation: the home page needs something true to point at on a day when no
	// store has crossed a threshold, and "somebody was here last week" is always true of
	// somewhere.
	Recent []StoreHighlight `json:"recent,omitempty"`
}

// MonthlyHighlights answers what the home page can honestly say about the community this
// month. Three things, and they are not the same kind of claim.
//
// Two are standouts -- the largest rating increase and the most reviews -- and a standout
// says a crowd agreed. Those require five reviews, from at least three different people,
// with activity inside the window. Missing signals stay nil so a client omits the section
// rather than filling it with something weak.
//
// The third is the recently reviewed list, which claims nothing beyond "somebody wrote
// about this one lately" and therefore carries no threshold. It exists because a home page
// with no standout had nothing to point at, and a home page that points at no store page
// is a home page that passes its standing to nothing.
func (s *Service) MonthlyHighlights(ctx context.Context) (MonthlyStoreHighlights, error) {
	windowStart := s.now().Add(-highlightWindowDays * 24 * time.Hour)
	base := `
WITH review_stats AS (
  SELECT s.id, s.slug, s.name, s.city, coalesce(s.district, '') AS district,
         coalesce((SELECT b.slug FROM brands b WHERE b.id=s.brand_id), '') AS brand_slug,
         coalesce((SELECT m.id::text FROM post_media pm
                   JOIN posts p2 ON p2.id=pm.post_id JOIN media m ON m.id=pm.media_id
                   WHERE p2.store_id=s.id AND p2.deleted_at IS NULL AND m.status='ready'
                   ORDER BY p2.created_at DESC, pm.position LIMIT 1), '') AS own_media,
         count(p.id) FILTER (WHERE p.deleted_at IS NULL) AS review_count,
         count(DISTINCT p.user_id) FILTER (WHERE p.deleted_at IS NULL) AS reviewer_count,
         max(p.created_at) FILTER (WHERE p.deleted_at IS NULL) AS last_review_at,
         count(p.id) FILTER (WHERE p.deleted_at IS NULL AND p.created_at >= $1) AS recent_review_count,
         avg(p.rating::double precision) FILTER (WHERE p.deleted_at IS NULL) AS current_rating,
         avg(p.rating::double precision) FILTER (
           WHERE p.created_at < $1 AND (p.deleted_at IS NULL OR p.deleted_at >= $1)
         ) AS prior_rating
  FROM stores s
  LEFT JOIN posts p ON p.store_id = s.id
  WHERE s.deleted_at IS NULL
  GROUP BY s.id, s.slug, s.name, s.city, s.district, brand_slug, own_media
)
SELECT id, slug, name, city, district, brand_slug, own_media, current_rating, review_count, recent_review_count,
       reviewer_count, coalesce(current_rating - prior_rating, 0) AS rating_increase
FROM review_stats
`

	out := MonthlyStoreHighlights{}
	// The two standouts carry the thresholds; the recent list deliberately does not.
	standing := `WHERE review_count >= $2 AND recent_review_count > 0 AND reviewer_count >= $3 `
	ratingQuery := base + standing + `
  AND prior_rating IS NOT NULL
  AND current_rating > prior_rating
ORDER BY rating_increase DESC, recent_review_count DESC, review_count DESC, id
LIMIT 1`
	rating, err := scanHighlight(s.db.QueryRow(ctx, ratingQuery, windowStart, highlightMinimumReviews, highlightMinimumReviewers))
	if err != nil {
		return out, err
	}
	out.RatingGainer = rating

	mostReviewedQuery := base + standing + `
ORDER BY recent_review_count DESC, current_rating DESC, review_count DESC, id
LIMIT 1`
	mostReviewed, err := scanHighlight(s.db.QueryRow(ctx, mostReviewedQuery, windowStart, highlightMinimumReviews, highlightMinimumReviewers))
	if err != nil {
		return out, err
	}
	out.MostReviewed = mostReviewed

	recentRows, err := s.db.Query(ctx, base+`WHERE review_count > 0
ORDER BY last_review_at DESC NULLS LAST, id
LIMIT `+strconv.Itoa(recentHighlightLimit), windowStart)
	if err != nil {
		return out, err
	}
	defer recentRows.Close()
	for recentRows.Next() {
		item, err := scanHighlight(recentRows)
		if err != nil {
			return out, err
		}
		if item != nil {
			out.Recent = append(out.Recent, *item)
		}
	}
	return out, recentRows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHighlight(row rowScanner) (*StoreHighlight, error) {
	var item StoreHighlight
	var brandSlug, ownMedia string
	if err := row.Scan(
		&item.ID,
		&item.Slug,
		&item.Name,
		&item.City,
		&item.District,
		&brandSlug,
		&ownMedia,
		&item.AverageRating,
		&item.ReviewCount,
		&item.RecentReviewCount,
		&item.ReviewerCount,
		&item.RatingIncrease,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	// Same order the result list uses: somebody who went there and took a picture beats
	// the chain's mark, which beats nothing.
	if ownMedia != "" {
		item.Photo = &Photo{Source: "community", MediaID: ownMedia}
	} else if brandSlug != "" {
		item.Photo = &Photo{Source: "brand", BrandSlug: brandSlug}
	}
	return &item, nil
}
