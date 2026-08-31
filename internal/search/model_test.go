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
	if Validate(Intent{Scope: ScopeOutOfScope, StoreName: "Unrelated Shop"}) == nil {
		t.Fatal("out-of-scope store name accepted")
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

func TestCatalogueMarkerIsCopiedWithoutChangingScore(t *testing.T) {
	id := uuid.New()
	base := storepkg.Item{ID: id, Name: "Işık Ev", Platform: storepkg.Stats{AverageRating: 4.7, ReviewCount: 82, FavoriteCount: 240, PostCount: 82}}
	standard := fromStore(base, 3)
	base.IsCatalogStore = true
	catalogue := fromStore(base, 3)

	if !catalogue.CatalogStore {
		t.Fatal("catalogue marker was not copied to the search contract")
	}
	if catalogue.score != standard.score {
		t.Fatalf("catalogue marker changed ranking score: standard=%v catalogue=%v", standard.score, catalogue.score)
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

func TestDeterministicUnderstandsWhiteGoodsAsHomeRetail(t *testing.T) {
	intent := Deterministic("beyaz eşya")
	if intent.Scope != ScopeHomeLiving || intent.StoreName != "" || !has(intent.Categories, "household") || !has(intent.ProductTerms, "home_appliance") {
		t.Fatalf("white goods parsed as %+v", intent)
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

func TestDeterministicDoesNotSpecialCaseStoreBrands(t *testing.T) {
	// Store names are discovered through our catalogue and Google's provider data. A
	// hand-maintained brand list made partial names behave differently before and after
	// another query happened to import branches into the catalogue.
	for _, query := range []string{"IKEA", "Madame Coco", "English Home", "Koçtaş"} {
		intent := Deterministic(query)
		if intent.Scope != ScopeUnclear || intent.StoreName != "" {
			t.Fatalf("brand %q was special-cased: %+v", query, intent)
		}
	}
	enriched := merge(Deterministic("özel ev mağazası"), Intent{Scope: ScopeHomeLiving, StoreName: "Özel Ev"})
	if enriched.Scope != ScopeHomeLiving || enriched.StoreName != "Özel Ev" {
		t.Fatalf("AI store name was not merged: %+v", enriched)
	}
}

func TestTireProviderTypesAreOutOfScope(t *testing.T) {
	for _, providerType := range []string{"tire_shop", "auto_parts_store"} {
		if IsHomeLivingStore("Örnek İşletme", []string{providerType}) {
			t.Fatalf("provider type %q classified as home and living", providerType)
		}
	}
}

func TestElectricalLightingRetailAndStoreNameAreDistinguishedFromServices(t *testing.T) {
	store := Deterministic("Yeğenler Elektrik Antalya")
	if store.Scope != ScopeHomeLiving || store.StoreName != "Yeğenler Elektrik" || store.LocationText != "Antalya" || !has(store.Categories, "lighting") {
		t.Fatalf("electrical retailer parsed as %+v", store)
	}
	retail := Deterministic("Antalya elektrik malzemeleri mağazası")
	if retail.Scope != ScopeHomeLiving || retail.StoreName != "" || !has(retail.Categories, "lighting") {
		t.Fatalf("electrical supplies parsed as %+v", retail)
	}
	if service := Deterministic("Yakınımda elektrikçi lazım"); service.Scope != ScopeOutOfScope {
		t.Fatalf("electrician service scope=%q", service.Scope)
	}
}

func TestStoreNameDropsOnlyAnEdgeLocation(t *testing.T) {
	for _, test := range []struct{ name, location, want string }{
		{"Yeğenler Elektrik Antalya", "Antalya", "Yeğenler Elektrik"},
		{"Antalya Yeğenler Elektrik", "Antalya", "Yeğenler Elektrik"},
		{"Madame Coco Kadıköy", "Kadikoy", "Madame Coco"},
		{"Güney Antalya Halı", "Antalya", "Güney Antalya Halı"},
	} {
		if got := stripEdgeLocation(test.name, test.location); got != test.want {
			t.Fatalf("stripEdgeLocation(%q,%q)=%q want %q", test.name, test.location, got, test.want)
		}
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

// The provider gets the person's words and their location, and nothing from our own
// taxonomy. This test used to assert the opposite -- that parsed terms and category slugs
// were appended -- on the theory that more terms match better. They do not: the slugs are
// English words that Turkish businesses put on their signs, so a search for "yatak" near
// Bostanlı came back as six branches of one chain out of six results, because we had added
// "bedding" and half that chain's name is Bedding. The rule it was written for was real
// until it was measured.
func TestPlacesQuerySendsOnlyWhatThePersonSaid(t *testing.T) {
	got := placesQuery(Intent{ProductTerms: []string{"bedding_set"}, SemanticTerms: []string{"home dowry shopping"}, Categories: []string{"bedding"}}, "Çeyiz almak istiyorum")
	if got != "Çeyiz almak istiyorum" {
		t.Fatalf("query should be the person's own words, got %q", got)
	}
	for _, leaked := range []string{"bedding", "home dowry shopping"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("internal key %q reached the provider: %q", leaked, got)
		}
	}
	// Where a place was named, it still travels -- that is the person's own word too.
	if got := placesQuery(Intent{LocationText: "Bostanlı", Categories: []string{"bedding"}}, "yatak"); got != "yatak Bostanlı" {
		t.Fatalf("location should travel with the query, got %q", got)
	}
	if long := placesQuery(Intent{ProductTerms: []string{"furniture"}}, strings.Repeat("ö", 500)); len([]rune(long)) > 500 {
		t.Fatalf("places query too long: %d", len([]rune(long)))
	}
	if got := placesQuery(Intent{StoreName: "Yeğenler Elektrik", LocationText: "Antalya", Categories: []string{"lighting"}}, "Yeğenler Elektrik Antalya"); got != "Yeğenler Elektrik Antalya" {
		t.Fatalf("named places query=%q", got)
	}
}

func TestNameMatchesIgnoresCaseAndTurkishDiacritics(t *testing.T) {
	cases := []struct {
		result, storeName string
		want              bool
	}{
		{"IKEA Bornova", "ikea", true},
		{"Koçtaş Ankara", "koctas", true},
		{"Madame Coco Kadıköy", "madame coco", true},
		{"Perde Dünyası", "ikea", false},
		{"IKEA Bornova", "", false},
	}
	for _, c := range cases {
		if got := nameMatches(c.result, c.storeName); got != c.want {
			t.Fatalf("nameMatches(%q,%q)=%v want %v", c.result, c.storeName, got, c.want)
		}
	}
}

func TestValidPhotoNameRejectsUnsafeInput(t *testing.T) {
	if !ValidPhotoName("places/ChIJabc-123_x/photos/AelY_Cs9-xY") {
		t.Fatal("expected a well formed photo name to be accepted")
	}
	// Google issues photo references well past four hundred characters, so a bound
	// that only fits a short reference silently drops every real photo.
	if !ValidPhotoName("places/ChIJmwFY8_OPwxQRb39DXDLxwcY/photos/" + strings.Repeat("A", 452)) {
		t.Fatal("expected a real length photo reference to be accepted")
	}
	for _, bad := range []string{
		"",
		"places/../photos/x",
		"https://evil.example/places/a/photos/b",
		"places/a/photos/b?key=leak",
		"places/a/photos/b/media",
		"places/a/photos/" + strings.Repeat("x", 1001),
	} {
		if ValidPhotoName(bad) {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

// A store we already hold has its photograph on file. Before this, only stores that the
// live Google response happened to include showed a picture, so our own catalogue -- and
// every promoted store, which never goes through Google -- rendered as blank tiles.
func TestFromStoreCarriesTheStoredPhoto(t *testing.T) {
	name := "places/ChIJmwFY8_OPwxQRb39DXDLxwcY/photos/AelY_Cs9xY"
	r := fromStore(storepkg.Item{ID: uuid.New(), Name: "Salihler Halı Perde", Photo: &storepkg.Photo{Source: "google", Name: name, Attributions: []string{"Bir Kullanıcı"}}}, 0)
	if r.Photo == nil || r.Photo.Name != name {
		t.Fatalf("expected the stored photo to reach the result, got %+v", r.Photo)
	}
	if len(r.Photo.Attributions) != 1 {
		t.Fatal("the provider terms require the credit to travel with the photograph")
	}
	// A malformed reference would render as a broken image, so it is dropped here rather
	// than handed to the client.
	bad := fromStore(storepkg.Item{ID: uuid.New(), Name: "Taç", Photo: &storepkg.Photo{Source: "google", Name: "https://evil.example/x"}}, 0)
	if bad.Photo != nil {
		t.Fatal("expected an invalid photo reference to be dropped")
	}
}

// A Turkish address ends the way the post office writes it, and the last component before
// the country carries three things at once: a postcode, a district and a province. Storing
// it whole is how a store's page came to be titled with a postcode nobody asked for, and
// how the district column stayed empty for every store in the catalogue.
func TestCityAndDistrictFromTurkishAddress(t *testing.T) {
	cases := []struct{ address, city, district string }{
		{"Meltem, 3808. Sk. No:5, 07070 Konyaaltı/Antalya, Türkiye", "Antalya", "Konyaaltı"},
		{"AKPIYAR MAH. GAP BLV, 63320 Karaköprü/Şanlıurfa, Türkiye", "Şanlıurfa", "Karaköprü"},
		{"Bağdat Cd. No:1, 34710 Kadıköy/İstanbul, Türkiye", "İstanbul", "Kadıköy"},
		// No district, which is the ordinary shape for a province that is its own centre.
		{"Bir Sokak No:2, Antalya, Türkiye", "Antalya", ""},
		// No postcode at all.
		{"Bir Cadde, Muratpaşa/Antalya, Türkiye", "Antalya", "Muratpaşa"},
		// A village inside a district inside a province. Only the level next to the
		// province is the district; the rest is street detail.
		{"Bir Yol, 07220 Bahtılı Köyü/Kepez/Antalya, Türkiye", "Antalya", "Kepez"},
		// Nothing usable.
		{"Türkiye", "Bilinmiyor", ""},
	}
	for _, c := range cases {
		city, district := CityAndDistrict(c.address)
		if city != c.city || district != c.district {
			t.Errorf("cityAndDistrict(%q) = (%q, %q), want (%q, %q)", c.address, city, district, c.city, c.district)
		}
	}
}

// A product is not a store. The intent parser hands back "Yastık" as a store name, and a
// name-led search drops the radius filter and lifts every shop whose sign carries the word
// above every shop that is nearer -- which is how a search from Antalya answered with a
// shop 44 km away in second place, above a dozen at eight kilometres.
func TestAProductWordIsNotAStoreName(t *testing.T) {
	generic := []string{"Yastık", "yastik", "perde", "PERDE MAĞAZASI", "halı", "mobilya mağazası", "nevresim", "curtain", "möbel", "ковер", "ev tekstili ürünleri"}
	for _, name := range generic {
		if !genericStoreName(name) {
			t.Errorf("genericStoreName(%q) = false, want it treated as a product rather than a name", name)
		}
	}
	// A real sign carries a word of its own beside the trade, and that word is the name.
	named := []string{"Yataş", "Bambi Yatak", "İşbir Yatak", "Taç Antalya Fabrika Satış Mağazası", "Sipahioğlu Home", "Koçtaş", "Vintage Perde Döşemealtı"}
	for _, name := range named {
		if genericStoreName(name) {
			t.Errorf("genericStoreName(%q) = true, want it kept as a name", name)
		}
	}
	if genericStoreName("") {
		t.Error("an empty name is nothing to strip")
	}
}
