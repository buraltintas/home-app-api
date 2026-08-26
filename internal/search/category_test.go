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
