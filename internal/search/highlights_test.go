package search

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type failingRow struct{ err error }

func (r failingRow) Scan(...any) error { return r.err }

func TestHighlightsRequireAgreedReviewThreshold(t *testing.T) {
	if highlightMinimumReviews != 5 {
		t.Fatalf("monthly highlights require exactly five reviews, got %d", highlightMinimumReviews)
	}
}

func TestHighlightsUseThirtyDayWindow(t *testing.T) {
	if highlightWindowDays != 30 {
		t.Fatalf("monthly highlights require a 30-day window, got %d", highlightWindowDays)
	}
}

func TestScanHighlightTreatsNoRowsAsAnAbsentSignal(t *testing.T) {
	item, err := scanHighlight(failingRow{err: pgx.ErrNoRows})
	if err != nil || item != nil {
		t.Fatalf("expected an absent signal, got item=%v err=%v", item, err)
	}
}

func TestScanHighlightPreservesDatabaseErrors(t *testing.T) {
	want := errors.New("database unavailable")
	if _, err := scanHighlight(failingRow{err: want}); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

// A standout says a crowd agreed, so counting reviews is not enough: fourteen reviews by
// one person put a shop on the home page under the words "most reviewed this month".
func TestStandoutsRequireMoreThanOneVoice(t *testing.T) {
	if highlightMinimumReviewers < 3 {
		t.Fatalf("a standout needs at least three different people, got %d", highlightMinimumReviewers)
	}
	if highlightMinimumReviewers > highlightMinimumReviews {
		t.Fatalf("more reviewers than reviews is unsatisfiable: %d > %d", highlightMinimumReviewers, highlightMinimumReviews)
	}
}

// The recently reviewed list is what gives the home page links out to store pages, so an
// empty or single-entry list would leave the page pointing nowhere again.
func TestRecentListIsWorthRendering(t *testing.T) {
	if recentHighlightLimit < 5 {
		t.Fatalf("too few to be a list, got %d", recentHighlightLimit)
	}
}
