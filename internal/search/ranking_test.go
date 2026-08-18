package search

import "testing"

func meters(v float64) *float64 { return &v }

func names(results []Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Name
	}
	return out
}

// The searcher stood in Antalya and Denizli came back first, because a five star store
// 169 km away only paid 16.9 points for the distance while its rating and review count
// were worth far more. Distance now bands the list, so nothing in another province can
// climb over something down the road.
func TestNearbyStoresOutrankFarBetterRatedOnes(t *testing.T) {
	results := []Result{
		{Name: "Cotton Box", Address: "Hacıeyüplü, 20050 Denizli Merkezefendi/Denizli, Türkiye", DistanceMeters: meters(169400), score: googleScore(Place{Rating: 5, RatingCount: 49}, 2)},
		{Name: "Denizli Tekstil Dünyası", Address: "Saraylar, 20100 Denizli Merkezefendi/Denizli, Türkiye", DistanceMeters: meters(163000), score: googleScore(Place{Rating: 5, RatingCount: 28}, 3)},
		{Name: "Yataş Bedding Aspendos", Address: "Kızıltoprak, 07230 Muratpaşa/Antalya, Türkiye", DistanceMeters: meters(13900), Platform: &Platform{}, score: mergedScore(Platform{}, Place{Rating: 4, RatingCount: 141}, 5)},
		{Name: "Moda Yorgan House", Address: "Fevzi Çakmak, 07210 Kepez/Antalya, Türkiye", DistanceMeters: meters(9000), score: googleScore(Place{Rating: 4, RatingCount: 4}, 0)},
	}
	rankResults(results, true)
	for _, far := range []string{"Cotton Box", "Denizli Tekstil Dünyası"} {
		for _, near := range []string{"Moda Yorgan House", "Yataş Bedding Aspendos"} {
			if indexOf(results, far) < indexOf(results, near) {
				t.Fatalf("%q from another province outranked %q: %v", far, near, names(results))
			}
		}
	}
}

// A store our own community has reviewed, in the searcher's own city, is the whole point
// of the product and has to lead the list even when Google likes something else more.
func TestReviewedStoresInTheSearchersCityComeFirst(t *testing.T) {
	results := []Result{
		{Name: "Closest Unknown", Address: "Kepez/Antalya, Türkiye", DistanceMeters: meters(400), score: googleScore(Place{Rating: 5, RatingCount: 900}, 0)},
		{Name: "Reviewed Antalya", Address: "Muratpaşa/Antalya, Türkiye", DistanceMeters: meters(11000), Platform: &Platform{ReviewCount: 6, AverageRating: 4.2}, score: platformScore(Platform{ReviewCount: 6, AverageRating: 4.2}, 4)},
		{Name: "Reviewed Denizli", Address: "Merkezefendi/Denizli, Türkiye", DistanceMeters: meters(160000), Platform: &Platform{ReviewCount: 40, AverageRating: 5}, score: platformScore(Platform{ReviewCount: 40, AverageRating: 5}, 1)},
	}
	rankResults(results, true)
	if results[0].Name != "Reviewed Antalya" {
		t.Fatalf("expected the reviewed Antalya store first, got %v", names(results))
	}
	if indexOf(results, "Reviewed Denizli") < indexOf(results, "Closest Unknown") {
		t.Fatalf("a reviewed store in another city must not jump the queue: %v", names(results))
	}
}

// Knowing a store must never cost it position against the same store seen only through
// Google, which is what the old flat 80 point floor did.
func TestKnownStoreWithoutReviewsKeepsItsGoogleStanding(t *testing.T) {
	place := Place{Rating: 4, RatingCount: 141}
	if mergedScore(Platform{}, place, 5) != googleScore(place, 5) {
		t.Fatal("a mapped store without community reviews lost its Google standing")
	}
	reviewed := Platform{ReviewCount: 3, AverageRating: 4.5}
	if mergedScore(reviewed, place, 5) != platformScore(reviewed, 5) {
		t.Fatal("community reviews must win over Google once they exist")
	}
}

func TestCityKeyReadsTurkishAddresses(t *testing.T) {
	for address, want := range map[string]string{
		"Kızıltoprak, Aspendos Blv. No:41, 07230 Muratpaşa/Antalya, Türkiye":       "antalya",
		"Hacıeyüplü, 3125. Sk. No:8, 20050 Denizli Merkezefendi/Denizli, Türkiye":  "denizli",
		"Güneşli, Dumlupınar Sk. No:5A, 34212 Bağcılar/İstanbul, Türkiye":          "istanbul",
		"Cumhuriyet Mah. 677 Sok. No:30, 07070 Antalya/Muratpaşa/Antalya, Türkiye": "antalya",
	} {
		if got := cityKey("", address); got != want {
			t.Fatalf("cityKey(%q)=%q want %q", address, got, want)
		}
	}
}

// Without coordinates there is no near or far, so the list must fall back to relevance
// instead of silently treating every result as equally distant.
func TestRankingWithoutALocationFallsBackToRelevance(t *testing.T) {
	results := []Result{{Name: "low", score: 10}, {Name: "high", score: 90}}
	rankResults(results, false)
	if results[0].Name != "high" {
		t.Fatalf("expected relevance order, got %v", names(results))
	}
}

func indexOf(results []Result, name string) int {
	for i, r := range results {
		if r.Name == name {
			return i
		}
	}
	return -1
}
