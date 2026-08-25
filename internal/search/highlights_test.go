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
