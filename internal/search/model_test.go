package search

import (
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
