package search

import (
	"strings"
	"testing"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
)

func TestDeterministicTurkishIntent(t *testing.T) {
	i := Deterministic("Antalya'da uygun fiyatlı ve büyük perde mağazaları")
	if i.Scope != ScopeHomeLiving || i.PriceIntent != "budget" || i.LocationText != "Antalya" || !has(i.Categories, "curtain") || !has(i.Attributes, "large_selection") {
		t.Fatalf("unexpected intent: %+v", i)
	}
}

func TestEquivalentMultilingualQueriesProduceCanonicalIntent(t *testing.T) {
	tests := []struct {
		query, language string
	}{
		{"Antalya'da uygun fiyatlı perde mağazası", "tr"},
		{"affordable curtain stores in Antalya", "en"},
		{"günstige Gardinengeschäfte in Antalya", "de"},
		{"недорогие магазины штор в Анталии", "ru"},
	}
	for _, test := range tests {
		intent := Deterministic(test.query)
		if string(intent.QueryLanguage) != test.language || intent.PriceIntent != "budget" || intent.LocationText != "Antalya" || !has(intent.Categories, "curtain") || !has(intent.Categories, "home_textile") || !has(intent.ProductTerms, "curtain") {
			t.Fatalf("%q produced %+v", test.query, intent)
		}
	}
}

func TestUnicodeNormalizationPreservesCyrillicAndMatchesTurkishASCII(t *testing.T) {
	if got := Deterministic("магазины штор").NormalizedQuery; got != "магазины штор" {
		t.Fatalf("Cyrillic changed: %q", got)
	}
	for _, query := range []string{"Kadıköy mobilya", "kadikoy furniture"} {
		if Deterministic(query).LocationText != "Kadikoy" {
			t.Fatalf("location not normalized for %q", query)
		}
	}
}
func TestIntentRejectsUnknownCategory(t *testing.T) {
	if Validate(Intent{Scope: ScopeHomeLiving, Categories: []string{"restaurant"}}) == nil {
		t.Fatal("unknown category accepted")
	}
}
func TestIntentRejectsOversizedOrMalformedValues(t *testing.T) {
	if Validate(Intent{Scope: ScopeHomeLiving, ProductTerms: []string{strings.Repeat("x", 81)}}) == nil {
		t.Fatal("oversized product accepted")
	}
	if Validate(Intent{Scope: ScopeHomeLiving, SemanticTerms: []string{"safe\nunsafe"}}) == nil {
		t.Fatal("control character accepted")
	}
}

func TestIntentCountsUnicodeCharactersNotBytes(t *testing.T) {
	if err := Validate(Intent{Scope: ScopeUnclear, NormalizedQuery: strings.Repeat("Ж", 500)}); err != nil {
		t.Fatalf("500 Unicode characters rejected: %v", err)
	}
	if err := Validate(Intent{Scope: ScopeUnclear, NormalizedQuery: strings.Repeat("Ж", 501)}); err == nil {
		t.Fatal("501 Unicode characters accepted")
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
	r := fromStore(storepkg.Item{ID: id, Name: "Işık Ev", Platform: storepkg.Stats{AverageRating: 4.7, ReviewCount: 82, FavoriteCount: 240, PostCount: 82}}, 0)
	if r.Source != "internal" || r.Platform == nil || r.Platform.ReviewCount != 82 || r.Platform.PostCount != 82 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestDeterministicUnderstandsIndirectHomeNeeds(t *testing.T) {
	for _, test := range []struct {
		query      string
		categories []string
	}{
		{"Çeyiz almak istiyorum", []string{"home_textile", "bedding", "kitchenware", "tableware"}},
		{"Nevresim takımı lazım", []string{"home_textile", "bedding"}},
	} {
		intent := Deterministic(test.query)
		if intent.Scope != ScopeHomeLiving {
			t.Fatalf("%q scope=%q", test.query, intent.Scope)
		}
		for _, category := range test.categories {
			if !has(intent.Categories, category) {
				t.Fatalf("%q missing %q: %+v", test.query, category, intent)
			}
		}
	}
}

func TestDeterministicRejectsUnrelatedAndMarksChitchatUnclear(t *testing.T) {
	if got := Deterministic("Yakınımda lastikçi lazım").Scope; got != ScopeOutOfScope {
		t.Fatalf("lastikçi scope=%q", got)
	}
	if got := Deterministic("naber merhaba").Scope; got != ScopeUnclear {
		t.Fatalf("chitchat scope=%q", got)
	}
}

func TestGuidanceIsLocalizedAndRotatesExamples(t *testing.T) {
	first := guidanceFor("tr", ScopeUnclear)
	second := guidanceFor("tr", ScopeUnclear)
	if first.Code != "HOME_LIVING_ONLY" || first.Message == "" || len(first.Examples) != 2 || len(second.Examples) != 2 {
		t.Fatalf("invalid guidance: %+v %+v", first, second)
	}
	if first.Examples[0] == second.Examples[0] {
		t.Fatalf("examples did not rotate: %+v %+v", first.Examples, second.Examples)
	}
}

func TestPhysicallyReviewedPlatformStoreRanksBeforeGoogleOnly(t *testing.T) {
	community := platformScore(Platform{AverageRating: 1, ReviewCount: 1}, 19)
	google := googleScore(Place{Rating: 5, RatingCount: 100000}, 0)
	if community <= google {
		t.Fatalf("community score=%f google score=%f", community, google)
	}
}

func TestPlacesQueryUsesRawAndParsedTerms(t *testing.T) {
	got := placesQuery(Intent{ProductTerms: []string{"bedding_set"}, SemanticTerms: []string{"home dowry shopping"}, Categories: []string{"bedding"}}, "Çeyiz almak istiyorum")
	for _, want := range []string{"Çeyiz almak istiyorum", "bedding set", "home dowry shopping", "bedding"} {
		if !strings.Contains(got, want) {
			t.Fatalf("query %q missing %q", got, want)
		}
	}
	if long := placesQuery(Intent{ProductTerms: []string{"furniture"}}, strings.Repeat("ö", 500)); len([]rune(long)) > 500 {
		t.Fatalf("places query too long: %d", len([]rune(long)))
	}
}
