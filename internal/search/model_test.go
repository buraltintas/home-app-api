package search

import "testing"

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
