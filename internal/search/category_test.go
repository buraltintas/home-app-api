package search

import "testing"

func TestStoreCategoriesGenericHomeNames(t *testing.T) {
	cases := map[string]string{
		"English Home - Erasta AVM": "home_accessories",
		"Nilda Home":                "home_accessories",
		"Evim Home Goods":           "home_accessories",
		"Deco Home Ev Aksesuar":     "home_accessories",
		"Uçuç Home":                 "home_accessories",
	}
	for name, want := range cases {
		got := StoreCategories(name, nil)
		found := false
		for _, c := range got {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q -> %v, %q eksik", name, got, want)
		}
	}
}

// The whole point of the word boundary: these must stay uncategorised.
func TestStoreCategoriesIgnoresWordsInsideWords(t *testing.T) {
	for _, name := range []string{"Homeros Restaurant", "Evren Ekmek Fırını", "Deveci Otomotiv", "Isbirli Ekmek Taş Firin"} {
		if got := StoreCategories(name, nil); len(got) > 0 {
			t.Errorf("%q kategorisiz kalmalıydı, %v aldı", name, got)
		}
	}
}

// A chain has many branches with the same name and much the same score. Before this the
// order between them was arbitrary; the one a person can reach has to come first.
func TestNamedStoreResultsRunNearestFirst(t *testing.T) {
	near, mid, far := 800.0, 4000.0, 20000.0
	results := []Result{
		{Name: "İşbir Yatak Kepez", DistanceMeters: &far, nameHit: true, score: 90},
		{Name: "Yataş Bedding", DistanceMeters: &near, score: 95},
		{Name: "İşbir Yatak Konyaaltı", DistanceMeters: &near, nameHit: true, score: 80},
		{Name: "İşbir Yatak Muratpaşa", DistanceMeters: &mid, nameHit: true, score: 85},
	}
	rankResults(results, true, true)
	want := []string{"İşbir Yatak Konyaaltı", "İşbir Yatak Muratpaşa", "İşbir Yatak Kepez", "Yataş Bedding"}
	for i, name := range want {
		if results[i].Name != name {
			t.Fatalf("%d. sıra %q olmalıydı, %q geldi", i+1, name, results[i].Name)
		}
	}
}

// A review is worth leading with against stores you would actually weigh against each
// other. Ten kilometres away is not one of those.
func TestReviewLeadStopsAtAKilometre(t *testing.T) {
	near, far := 500.0, 10000.0
	reviewed := Platform{ReviewCount: 3, AverageRating: 5}
	results := []Result{
		{Name: "Uncalı, üç değerlendirme", DistanceMeters: &far, City: "Antalya", Platform: &reviewed, score: 400},
		{Name: "Beş yüz metre, değerlendirmesiz", DistanceMeters: &near, City: "Antalya", Platform: &Platform{}, score: 80},
	}
	rankResults(results, true, false)
	if results[0].Name != "Beş yüz metre, değerlendirmesiz" {
		t.Fatalf("yakındaki önce gelmeliydi, %q geldi", results[0].Name)
	}
}

// Inside the kilometre the review still leads, which is the whole point of collecting them.
func TestReviewStillLeadsInsideTheKilometre(t *testing.T) {
	a, b := 900.0, 300.0
	results := []Result{
		{Name: "Değerlendirmesiz, daha yakın", DistanceMeters: &b, City: "Antalya", Platform: &Platform{}, score: 80},
		{Name: "Değerlendirilmiş", DistanceMeters: &a, City: "Antalya", Platform: &Platform{ReviewCount: 2, AverageRating: 4}, score: 400},
	}
	rankResults(results, true, false)
	if results[0].Name != "Değerlendirilmiş" {
		t.Fatalf("aynı kilometrede değerlendirilmiş önce gelmeliydi, %q geldi", results[0].Name)
	}
}

// The bakery that matches "İşbir" by name and nothing else about it.
func TestUnclassifiedNameMatchGoesLast(t *testing.T) {
	near, far := 200.0, 6000.0
	results := []Result{
		{Name: "Isbirli Ekmek Taş Firin", DistanceMeters: &near, nameHit: true, Categories: []string{}, score: 90},
		{Name: "İşbir Yatak Kepez", DistanceMeters: &far, nameHit: true, Categories: []string{"bedding"}, score: 80},
	}
	rankResults(results, true, true)
	if results[0].Name != "İşbir Yatak Kepez" {
		t.Fatalf("sınıflandırılmış olan önce gelmeliydi, %q geldi", results[0].Name)
	}
}

// The catalogue filled up with businesses nobody would visit for a curtain, because
// anything a search turned up was kept.
func TestNonHomeBusinessesAreTurnedAwayAtTheDoor(t *testing.T) {
	away := map[string][]string{
		"bakery":          {"bakery", "food_store", "store"},
		"language school": {"school", "educational_institution", "point_of_interest"},
		"beauty salon":    {"beauty_salon", "point_of_interest", "service"},
		"clinic":          {"medical_clinic", "beauty_salon", "hair_care"},
		"warehouse":       {"storage", "service", "point_of_interest"},
		"contractor":      {"general_contractor", "point_of_interest", "service"},
		"apartments":      {"apartment_complex", "point_of_interest", "service"},
		"nursery":         {"child_care_agency", "point_of_interest", "service"},
	}
	for what, types := range away {
		if IsHomeLivingPlace(types) {
			t.Errorf("%s katalogda yeri yok, ama kabul edildi: %v", what, types)
		}
	}
}

// Silence is not evidence. Most shops carry nothing but "store", and a rule that needed
// proof of belonging would empty the catalogue.
func TestPlacesGoogleSaysNothingAboutAreKept(t *testing.T) {
	for _, types := range [][]string{
		{"store", "point_of_interest", "establishment"},
		{"point_of_interest", "establishment"},
		nil,
		{"furniture_store", "home_improvement_store", "home_goods_store"},
		// A department store with a cafe inside is still a department store.
		{"department_store", "cafe", "store"},
	} {
		if !IsHomeLivingPlace(types) {
			t.Errorf("kalması gerekirdi: %v", types)
		}
	}
}

// A renovation firm calls itself "tadilat" and puts "dekorasyon" in the name too. It is
// not a shop; nobody visits it to buy a lamp. Reported from the live site, twice.
func TestServiceBusinessesAreNotStores(t *testing.T) {
	services := []string{
		"Met Yapı / Antalya Tadilat - Dekorasyon Hizmetleri",
		"Antalia Dekorasyon & Tadilat",
		"Antalya İç Mimarlık | Decorative Studio",
		"Uyum Tadilat, Boya ve Dekorasyon",
	}
	for _, name := range services {
		if got := StoreCategories(name, nil); len(got) > 0 {
			t.Errorf("%q bir hizmet firması, kategorisiz kalmalıydı, %v aldı", name, got)
		}
		if IsHomeLivingStore(name, nil) {
			t.Errorf("%q katalogda yeri yok", name)
		}
	}
	// The name beats the provider. Google types this one as a home improvement store,
	// which is exactly how a contractor came back under a search for decoration.
	if got := StoreCategories("New Yapı Tadilat", []string{"home_improvement_store", "home_goods_store"}); len(got) > 0 {
		t.Errorf("sağlayıcı ne derse desin tadilat firması mağaza değil, %v aldı", got)
	}
}

// The rule has to be narrow or it takes real shops with it. These sell over a counter.
func TestRealShopsSurviveTheServiceRule(t *testing.T) {
	shops := map[string][]string{
		"VitrA - Artema - Güvercinler İnşaat Muratpaşa":   {"home_goods_store"},
		"Can Sıhhi Su Tesisat Malzeme Montaj Ve Tamiratı": {"hardware_store"},
		"YIKILMAZ MOBİLYA montaj mutfak Yüklük Vestiyer":  {"furniture_store"},
		"Horzum Spot Yapı Market":                         nil,
	}
	for name, types := range shops {
		if !IsHomeLivingStore(name, types) {
			t.Errorf("%q gerçek bir mağaza, elenmemeliydi", name)
		}
	}
}

// Turkish glues suffixes onto everything. A whole-word rule caught "Tadilat" and missed
// "Tadilatı", which left a renovation firm sitting in the decoration category after the
// rule meant to remove it had shipped.
func TestServiceRuleSurvivesTurkishSuffixes(t *testing.T) {
	for _, name := range []string{
		"ANTALYA DECOR HOME | Antalya Ev Dekorasyon ve Tadilatı",
		"Antalya Tadilatçı Dekorasyon",
		"Antalya İç Mimarlığı",
		"Kent Mimarlık Bürosu",
		"Boya Badana Hizmetlerimiz",
	} {
		if IsHomeLivingStore(name, nil) {
			t.Errorf("%q bir hizmet firması, elenmeliydi", name)
		}
	}
}

// A trade type outranks the sector labels Google hands the whole building trade. Every
// painter and roofer in the catalogue carried home_improvement_store, and reading that as
// evidence of a shop is what kept letting renovation firms in.
func TestTradeTypeBeatsSectorLabel(t *testing.T) {
	cases := map[string][]string{
		"painter":            {"painter", "service", "point_of_interest", "establishment"},
		"painter and roofer": {"painter", "roofing_contractor", "building_materials_store", "general_contractor", "home_goods_store"},
		"contractor":         {"general_contractor", "home_improvement_store", "home_goods_store", "store"},
		"electrician":        {"electrician", "home_improvement_store"},
		"interior designer":  {"interior_designer", "home_goods_store"},
	}
	for name, types := range cases {
		if IsHomeLivingPlace(types) {
			t.Errorf("%s: kept a business that sells labour: %v", name, types)
		}
	}
}

// What is sold still wins outright, so a real showroom typed into the building trade is
// not thrown out with the contractors.
func TestProductTypeBeatsTradeType(t *testing.T) {
	cases := map[string][]string{
		"bathroom showroom": {"bathroom_supply_store", "general_contractor", "home_goods_store"},
		"tile shop":         {"tile_store", "painter"},
		"department store":  {"department_store", "cafe"},
	}
	for name, types := range cases {
		if !IsHomeLivingPlace(types) {
			t.Errorf("%s: threw out a shop: %v", name, types)
		}
	}
}

// A sector label on its own is still worth something: a shop with nothing but
// home_goods_store is a shop, and silence remains no evidence at all.
func TestSectorLabelAloneIsKept(t *testing.T) {
	for _, types := range [][]string{
		{"home_goods_store", "store", "point_of_interest"},
		{"home_improvement_store"},
		{"building_materials_store"},
		nil,
		{"store", "point_of_interest", "establishment"},
	} {
		if !IsHomeLivingPlace(types) {
			t.Errorf("threw out a place with no evidence against it: %v", types)
		}
	}
}

// The bakery from the report. A search for the bed brand "İşbir" came back with "Isbirli
// Ekmek Taş Firin" because the provider matches names loosely; what put it in front of a
// reader, and then into the catalogue, was our own import writing a store the classifier
// had already declined to classify. These are the shapes that verdict has to keep giving,
// whatever the provider sends back.
func TestStoreCategoriesDeclinesWhatThisProductIsNotAbout(t *testing.T) {
	for _, name := range []string{
		"Isbirli Ekmek Taş Firin",
		"Aknur Teknik Servis",
		"Vintage Rent Motorbike",
		"İbrahim Aksin Spor Kompleksi",
	} {
		if got := StoreCategories(name, nil); len(got) != 0 {
			t.Errorf("StoreCategories(%q) = %v, want none", name, got)
		}
	}
	// And it still says yes to what the product is about, so the rule above cannot be
	// satisfied by simply refusing everything.
	if got := StoreCategories("İşbir Yatak Uncalı (Uyku Merkezi)", nil); len(got) == 0 {
		t.Error("StoreCategories declined a bedding store")
	}
}

// Half the kebab houses in Turkey are called "... Sofrası", and "sofra" is also a real
// word for tableware. Reading the sign before the provider's verdict turned every one of
// them into a tableware shop -- "Urfa Sofrası", typed by Google as a turkish_restaurant,
// was sitting in the catalogue under Sofra Takımı. A trade the provider names outright is
// exact, and no word on a sign overrules it.
func TestATradeNamedOutrightBeatsTheSign(t *testing.T) {
	restaurant := []string{"turkish_restaurant", "restaurant", "food", "point_of_interest"}
	for _, name := range []string{"Urfa Sofrası", "Sofra Lahmacun & Pide", "Ece Sofra Restaurant", "Sofraya Buyurun"} {
		if IsHomeLivingStore(name, restaurant) {
			t.Errorf("IsHomeLivingStore(%q, restaurant) = true, want false", name)
		}
		if got := StoreCategories(name, restaurant); len(got) != 0 {
			t.Errorf("StoreCategories(%q, restaurant) = %v, want none", name, got)
		}
	}

	// The sign must still win where the provider says only that this is a building. That
	// is the case the ordering was written for and it has to keep working.
	vague := []string{"point_of_interest", "establishment"}
	if !IsHomeLivingStore("Sofra Ev Tekstili", vague) {
		t.Error("a tableware shop was rejected when the provider said nothing useful")
	}
	if got := StoreCategories("Anadolu Sofra Takımları", vague); len(got) == 0 {
		t.Error("a tableware shop got no categories when the provider said nothing useful")
	}
	// And a sector label is still not a trade: a shop is kept on its own sign.
	if !IsHomeLivingStore("Yılmaz Halı", []string{"home_goods_store"}) {
		t.Error("a carpet shop was rejected")
	}
}

// A sign can name two trades at once. "Sofra" is what a dinner service is called and it is
// what half the country's eating houses are called, so it cannot decide on its own -- and
// two shops reported from the live site were filed under tableware on the strength of it.
func TestAFoodBusinessIsNotATablewareShop(t *testing.T) {
	cases := []struct {
		name  string
		types []string
	}{
		// No telling type at all, so the sign is everything -- and it says food twice.
		{"Bizim Sofra Günlük Ev Yemekleri Kahvaltı", []string{"manufacturer", "point_of_interest", "establishment"}},
		// The provider names the trade outright.
		{"SOFRA ŞARKÜTERİ&TEKEL", []string{"liquor_store", "store", "point_of_interest"}},
		{"Sofra Lokantası", nil},
		{"Anadolu Sofrası Kebap", nil},
		{"Köşe Pastanesi", nil},
	}
	for _, c := range cases {
		if got := StoreCategories(c.name, c.types); len(got) > 0 {
			t.Errorf("StoreCategories(%q) = %v, want nothing", c.name, got)
		}
	}

	// The word still works where it is the only trade named.
	shops := []struct {
		name  string
		types []string
	}{
		{"Sofra Takımı Dünyası", nil},
		{"Zücaciye Sofra", []string{"home_goods_store"}},
	}
	for _, c := range shops {
		if got := StoreCategories(c.name, c.types); len(got) == 0 {
			t.Errorf("StoreCategories(%q) returned nothing; a tableware shop is still a shop", c.name)
		}
	}
}

// A word inside another word is not that word, and Turkish adds to the end of words.
// "Tekeli Kilim" is a carpet shop; "Sofra Lokantası" is not a tableware shop.
func TestTheFoodVocabularyReadsWordsNotLetters(t *testing.T) {
	for _, name := range []string{"Tekeli Kilim", "Tekeli", "Yatak Odası Dünyası"} {
		if namesAFoodBusiness(normalizeText(name), foldLatin(normalizeText(name))) {
			t.Errorf("%q was read as a food business", name)
		}
	}
	for _, name := range []string{"Sofra Lokantası", "Hacı Baba Kebapçısı", "Köşe Pastanesi"} {
		if !namesAFoodBusiness(normalizeText(name), foldLatin(normalizeText(name))) {
			t.Errorf("%q was not read as a food business", name)
		}
	}
}

// An oven and a hob are kitchen appliances. The food vocabulary must not swallow them.
func TestKitchenAppliancesSurviveTheFoodVocabulary(t *testing.T) {
	for _, name := range []string{"Ankastre Fırın ve Ocak Merkezi", "Arçelik Ankastre"} {
		if namesAFoodBusiness(normalizeText(name), foldLatin(normalizeText(name))) {
			t.Errorf("%q was read as a food business", name)
		}
	}
}
