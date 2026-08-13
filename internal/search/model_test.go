package search

import (
	"strings"
	"testing"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
)

func TestDeterministicTurkishIntent(t *testing.T) {
	i := Deterministic("Antalya'da uygun fiyatlı ve büyük perde mağazaları")
	if i.PriceIntent != "budget" || i.LocationText != "antalya" || !has(i.Categories, "curtain") || !has(i.Attributes, "large_selection") {
		t.Fatalf("unexpected intent: %+v", i)
	}
}
func TestIntentRejectsUnknownCategory(t *testing.T) {
	if Validate(Intent{Categories: []string{"restaurant"}}) == nil {
		t.Fatal("unknown category accepted")
	}
}
func TestIntentRejectsOversizedOrMalformedValues(t *testing.T) {
	if Validate(Intent{ProductTerms: []string{strings.Repeat("x", 81)}}) == nil {
		t.Fatal("oversized product accepted")
	}
	if Validate(Intent{SemanticTerms: []string{"safe\nunsafe"}}) == nil {
		t.Fatal("control character accepted")
	}
}
func TestInternalQueryUsesParsedDemandTerms(t *testing.T) {
	got := internalQuery(Intent{NormalizedQuery: "uzun doğal cümle", ProductTerms: []string{"avize", "lamba"}})
	if got != "avize OR lamba" {
		t.Fatalf("query=%q", got)
	}
}
func has(v []string, w string) bool {
	for _, x := range v {
		if x == w {
			return true
		}
	}
	return false
}
func TestInternalResultClassificationAndSnapshot(t *testing.T) {
	id := uuid.New()
	r := fromStore(storepkg.Item{ID: id, Name: "Işık Ev", Platform: storepkg.Stats{AverageRating: 4.7, ReviewCount: 82, FavoriteCount: 240, PostCount: 82}})
	if r.Source != "internal" || r.Platform == nil || r.Platform.ReviewCount != 82 || r.Platform.PostCount != 82 {
		t.Fatalf("unexpected result: %+v", r)
	}
}
