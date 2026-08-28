package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"

	"github.com/google/uuid"
)

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
	rankResults(results, true, false)
	for _, far := range []string{"Cotton Box", "Denizli Tekstil Dünyası"} {
		for _, near := range []string{"Moda Yorgan House", "Yataş Bedding Aspendos"} {
			if indexOf(results, far) < indexOf(results, near) {
				t.Fatalf("%q from another province outranked %q: %v", far, near, names(results))
			}
		}
	}
}

// A store our own community has reviewed is the whole point of the product and leads even
// when Google likes something else more -- but only against stores a person would weigh
// against each other, which the product owner set at a kilometre. It used to lead the whole
// of the searcher's city, and in Antalya that meant a review eleven kilometres away beat a
// shop four hundred metres away. Both stores here are now inside the same kilometre.
func TestReviewedStoresNearbyComeFirst(t *testing.T) {
	results := []Result{
		{Name: "Closest Unknown", Address: "Kepez/Antalya, Türkiye", DistanceMeters: meters(900), score: googleScore(Place{Rating: 5, RatingCount: 900}, 0)},
		{Name: "Reviewed Antalya", Address: "Muratpaşa/Antalya, Türkiye", DistanceMeters: meters(400), Platform: &Platform{ReviewCount: 6, AverageRating: 4.2}, score: platformScore(Platform{ReviewCount: 6, AverageRating: 4.2}, 4)},
		{Name: "Reviewed Denizli", Address: "Merkezefendi/Denizli, Türkiye", DistanceMeters: meters(160000), Platform: &Platform{ReviewCount: 40, AverageRating: 5}, score: platformScore(Platform{ReviewCount: 40, AverageRating: 5}, 1)},
	}
	rankResults(results, true, false)
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
	rankResults(results, false, false)
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

// A store's own name has to survive into its URL. Dropping every non-ASCII letter turned
// "GÜMÜŞHAN PERDE" into "g-m-han-perde", which is neither readable nor searchable.
func TestStoreSlugFoldsTurkishLettersInsteadOfDroppingThem(t *testing.T) {
	id := uuid.MustParse("a6b49c3a-a9a3-4e36-81ea-978ea1239d40")
	for name, want := range map[string]string{
		"GÜMÜŞHAN PERDE":    "gumushan-perde-a6b49c3a",
		"Yataş Bedding":     "yatas-bedding-a6b49c3a",
		"Çiğdem Ev Tekstil": "cigdem-ev-tekstil-a6b49c3a",
		"İpek Halı":         "ipek-hali-a6b49c3a",
	} {
		if got := storeSlug(name, id); got != want {
			t.Fatalf("storeSlug(%q)=%q want %q", name, got, want)
		}
	}
}

// The real list from an Antalya search for bed linen. Every Antalya store fell into one
// coarse 7-15 km band, so relevance sorted the whole city and the list opened at 11.4 km,
// went to 13.9, then back to 7.4. Inside a city the order has to read nearest first.
func TestCityResultsReadNearestFirst(t *testing.T) {
	antalya := "Muratpaşa/Antalya, Türkiye"
	results := []Result{
		{Name: "Evdek", Address: antalya, DistanceMeters: meters(11400), score: googleScore(Place{Rating: 4.8, RatingCount: 50}, 0)},
		{Name: "Yataş", Address: antalya, DistanceMeters: meters(13900), score: googleScore(Place{Rating: 4, RatingCount: 141}, 1)},
		{Name: "El-DE", Address: antalya, DistanceMeters: meters(8400), score: googleScore(Place{Rating: 4.2, RatingCount: 25}, 2)},
		{Name: "Kültür", Address: antalya, DistanceMeters: meters(7400), score: googleScore(Place{Rating: 4.5, RatingCount: 10}, 3)},
		{Name: "Bebemsi", Address: antalya, DistanceMeters: meters(8300), score: googleScore(Place{Rating: 4.6, RatingCount: 102}, 4)},
	}
	rankResults(results, true, false)
	want := []string{"Kültür", "Bebemsi", "El-DE", "Evdek", "Yataş"}
	for i, name := range want {
		if results[i].Name != name {
			t.Fatalf("expected nearest first %v, got %v", want, names(results))
		}
	}
}

// Nobody drives 169 km for a duvet cover because a list offered it. Google is queried with
// locationBias, which it treats as a hint, so far cities have to be dropped while the
// searcher's own city still has results.
func TestFarCitiesAreDroppedWhileNearbyResultsRemain(t *testing.T) {
	results := []Result{
		{Name: "Antalya 1", DistanceMeters: meters(7400)}, {Name: "Antalya 2", DistanceMeters: meters(8300)},
		{Name: "Antalya 3", DistanceMeters: meters(8400)}, {Name: "Antalya 4", DistanceMeters: meters(9000)},
		{Name: "Antalya 5", DistanceMeters: meters(11400)},
		{Name: "Denizli", DistanceMeters: meters(169400)}, {Name: "İstanbul", DistanceMeters: meters(479400)},
	}
	kept := withinLocalHorizon(results)
	if len(kept) != 5 {
		t.Fatalf("expected the five nearby stores, got %v", names(kept))
	}
	for _, r := range kept {
		if *r.DistanceMeters > localHorizonMeters {
			t.Fatalf("a far city survived: %v", names(kept))
		}
	}
}

// A genuinely empty area must not produce an empty page. When there is nothing nearby,
// the far results are all there is and they are kept.
func TestFarResultsSurviveWhenNothingIsNearby(t *testing.T) {
	results := []Result{{Name: "Far 1", DistanceMeters: meters(169400)}, {Name: "Far 2", DistanceMeters: meters(479400)}}
	if len(withinLocalHorizon(results)) != 2 {
		t.Fatal("a sparse area must keep its distant results rather than show nothing")
	}
}

// One label for three different incidents meant a production failure could not be told
// apart without log access: a missing key, a slow provider and a model answering
// off-schema need different fixes.
func TestAIFallbackReasonsAreDistinguishable(t *testing.T) {
	if got := aiFallbackReason(errors.New("schema"), true); got != "ai_invalid_response" {
		t.Fatalf("invalid response = %q", got)
	}
	if got := aiFallbackReason(context.DeadlineExceeded, false); got != "ai_timeout" {
		t.Fatalf("timeout = %q", got)
	}
	for _, text := range []string{"status 401", "Unauthorized", "invalid_api_key", "status 429", "insufficient_quota"} {
		if got := aiFallbackReason(errors.New(text), false); got != "ai_unauthorized" {
			t.Fatalf("aiFallbackReason(%q) = %q, want ai_unauthorized", text, got)
		}
	}
	if got := aiFallbackReason(errors.New("connection reset"), false); got != "ai_unavailable" {
		t.Fatalf("network error = %q", got)
	}
}

// "güney antalya home" must reach GÜNEY ANTALYA HALI ve YATAK SATIŞ MAĞAZASI. Ranking
// alone could not do it: "ANTALYA DECOR HOME" also matches two of the three words, so a
// scattered match scored the same as a consecutive run and then won on distance.
func TestPhrasePrefixesPreferLongerConsecutiveRuns(t *testing.T) {
	got := storepkg.PhrasePrefixes("güney antalya home")
	want := []string{"güney antalya home", "güney antalya", "antalya home"}
	if len(got) != len(want) {
		t.Fatalf("phrasePrefixes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("phrasePrefixes = %v, want %v", got, want)
		}
	}
	if single := storepkg.PhrasePrefixes("debu"); len(single) != 0 {
		t.Fatalf("a single word is not a phrase: %v", single)
	}
}

// Paid placement reaches the top of the searcher's own city. The badge in the UI is what
// makes this lawful; the ordering here is what makes it worth paying for.
func TestPremiumStoresLeadTheSearchersOwnCity(t *testing.T) {
	antalya := "Muratpaşa/Antalya, Türkiye"
	results := []Result{
		{Name: "Reviewed", Address: antalya, DistanceMeters: meters(200), Platform: &Platform{ReviewCount: 9, AverageRating: 4.8}, score: platformScore(Platform{ReviewCount: 9, AverageRating: 4.8}, 0)},
		{Name: "Closest", Address: antalya, DistanceMeters: meters(300), score: googleScore(Place{Rating: 5, RatingCount: 400}, 0)},
		{Name: "Premium", Address: antalya, DistanceMeters: meters(12000), Premium: true, score: googleScore(Place{Rating: 3.9, RatingCount: 12}, 6)},
	}
	rankResults(results, true, false)
	if results[0].Name != "Premium" {
		t.Fatalf("expected the promoted store first, got %v", names(results))
	}
	if indexOf(results, "Reviewed") > indexOf(results, "Closest") {
		t.Fatalf("community reviews still outrank an unreviewed store: %v", names(results))
	}
}

func TestNearbyPremiumDoesNotDependOnFormattedCityLabel(t *testing.T) {
	results := []Result{
		{Name: "Organic", City: "Antalya", Address: "Muratpaşa/Antalya, Türkiye", DistanceMeters: meters(400), score: 500},
		{Name: "Premium", City: "Muratpaşa", Address: "Antalya, Türkiye", DistanceMeters: meters(12000), Premium: true, score: 80},
	}
	rankResults(results, true, false)
	if results[0].Name != "Premium" {
		t.Fatalf("nearby premium store lost placement because city labels differed: %v", names(results))
	}
}

// Premium is not a way to buy your way into another province's results.
func TestPremiumDoesNotTravelToAnotherCity(t *testing.T) {
	results := []Result{
		{Name: "Local", Address: "Kepez/Antalya, Türkiye", DistanceMeters: meters(4000), score: googleScore(Place{Rating: 4, RatingCount: 20}, 1)},
		{Name: "Premium Denizli", Address: "Merkezefendi/Denizli, Türkiye", DistanceMeters: meters(165000), Premium: true, score: googleScore(Place{Rating: 5, RatingCount: 300}, 0)},
	}
	rankResults(results, true, false)
	if results[0].Name != "Local" {
		t.Fatalf("a promoted store in another city jumped the queue: %v", names(results))
	}
}

// A premium store inside the horizon must survive the far-city filter, or paid placement
// would silently vanish from exactly the searches it was bought for.
func TestPremiumSurvivesTheLocalHorizon(t *testing.T) {
	results := []Result{
		{Name: "Premium nearby", DistanceMeters: meters(9000), Premium: true},
		{Name: "A", DistanceMeters: meters(1000)}, {Name: "B", DistanceMeters: meters(2000)},
		{Name: "C", DistanceMeters: meters(3000)}, {Name: "D", DistanceMeters: meters(4000)},
		{Name: "Far", DistanceMeters: meters(400000)},
	}
	kept := withinLocalHorizon(results)
	if indexOf(kept, "Premium nearby") < 0 {
		t.Fatalf("the promoted store was dropped: %v", names(kept))
	}
}

// Promotion that only reorders is not promotion: the candidate list comes from Google, so
// a paid-for store Google did not return could never be lifted into it. These pin the two
// halves of the contract the injection has to keep.
func TestPromotedStoreLeadsEvenWhenFurtherAway(t *testing.T) {
	antalya := "Muratpaşa/Antalya, Türkiye"
	results := []Result{
		{Name: "Organic 5km", Address: antalya, DistanceMeters: meters(5900), score: googleScore(Place{Rating: 5, RatingCount: 104}, 0)},
		{Name: "Promoted 15km", Address: antalya, DistanceMeters: meters(15500), Premium: true, score: platformScore(Platform{}, 9)},
	}
	rankResults(results, true, false)
	if results[0].Name != "Promoted 15km" {
		t.Fatalf("promoted store did not lead: %v", names(results))
	}
}

// A wrapped deadline is still a deadline. Reporting one as an unreachable network sends
// whoever is debugging production to the wrong setting.
func TestWrappedTimeoutIsNotReportedAsUnreachable(t *testing.T) {
	for _, text := range []string{
		`Post "https://api.openai.com/v1/responses": context deadline exceeded`,
		`Post "https://api.openai.com/v1/responses": net/http: request canceled (Client.Timeout exceeded)`,
		`dial tcp 1.2.3.4:443: i/o timeout`,
	} {
		if got := aiFallbackReason(errors.New(text), false); got != "ai_timeout" {
			t.Fatalf("aiFallbackReason(%q) = %q, want ai_timeout", text, got)
		}
	}
	// A genuine connection failure must stay distinguishable from a slow one.
	if got := aiFallbackReason(errors.New("dial tcp: lookup api.openai.com: no such host"), false); got != "ai_unavailable" {
		t.Fatalf("dns failure = %q, want ai_unavailable", got)
	}
}

// Imported stores used to carry no categories at all, so a shop plainly named "... HALI"
// could not be found by anything that filtered on one -- which is what limited promotion.
func TestStoreCategoriesComeFromTheStoreItself(t *testing.T) {
	got := StoreCategories("GÜNEY ANTALYA HALI ve YATAK SATIŞ MAĞAZASI", nil)
	has := func(slug string) bool {
		for _, c := range got {
			if c == slug {
				return true
			}
		}
		return false
	}
	if !has("carpet") {
		t.Fatalf("a carpet shop got %v, expected carpet from its own name", got)
	}
	if !has("bedding") {
		t.Fatalf("expected bedding from \"YATAK\" in the name, got %v", got)
	}
	// Google's types are the other source, and are used even when the name says nothing.
	if got := StoreCategories("Salihler", []string{"furniture_store"}); len(got) == 0 || got[0] != "furniture" {
		t.Fatalf("google types ignored: %v", got)
	}
	// Neither source saying anything must not invent a category.
	if got := StoreCategories("Ada", []string{"restaurant"}); len(got) != 0 {
		t.Fatalf("expected no categories, got %v", got)
	}
	got = StoreCategories("FAZİLET MEFRUŞAT (PERDE&ÇEYİZ)", nil)
	if !slices.Contains(got, "curtain") || !slices.Contains(got, "home_textile") {
		t.Fatalf("explicit curtain category missing: %v", got)
	}
	for _, broad := range []string{"bedding", "kitchenware", "tableware"} {
		if slices.Contains(got, broad) {
			t.Fatalf("store name inferred %q from generic çeyiz wording: %v", broad, got)
		}
	}
}

func TestDeterministicMirror(t *testing.T) {
	for _, query := range []string{"Salon için büyük bir ayna", "mirror", "Spiegel", "зеркало"} {
		got := Deterministic(query)
		if got.Scope != ScopeHomeLiving || !slices.Contains(got.Categories, "decoration") {
			t.Fatalf("mirror query %q parsed as %+v", query, got)
		}
	}
}

func TestSearchResultAlwaysSerializesCategoryArray(t *testing.T) {
	result := fromStore(storepkg.Item{ID: uuid.New()}, 0)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"categories":[]`)) {
		t.Fatalf("empty categories must be an array: %s", encoded)
	}
}
