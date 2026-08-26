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
