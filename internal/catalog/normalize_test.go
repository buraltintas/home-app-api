package catalog

import (
	"strings"
	"testing"
)

// A shop's name has to carry the chain and the town, each exactly once. These are the four
// shapes locators actually publish.
func TestDisplayNameCarriesTheChainAndTheTownExactlyOnce(t *testing.T) {
	for _, c := range []struct{ brand, branch, province, district, want string }{
		// Neither part published: both are added. Nine shops called "Forum AVM" become
		// nine distinguishable shops.
		{"English Home", "Forum AVM", "Mersin", "Yenişehir", "English Home - Mersin Yenişehir Forum AVM"},
		{"English Home", "Forum AVM", "Kayseri", "Kocasinan", "English Home - Kayseri Kocasinan Forum AVM"},
		// The chain already names itself, so it is not repeated.
		{"Doğtaş", "Doğtaş Exclusive - Çukurova", "Adana", "Çukurova", "Adana Doğtaş Exclusive - Çukurova"},
		// The branch is already named for its town, in both halves.
		{"Madame Coco", "Antalya Kepez Kültür Pop-Up Cadde", "Antalya", "Kepez", "Madame Coco - Antalya Kepez Kültür Pop-Up Cadde"},
		// A dealership trading under its own sign.
		{"İstikbal", "Ahsen Mobilya", "Antalya", "Muratpaşa", "İstikbal - Antalya Muratpaşa Ahsen Mobilya"},
		// Nothing published to add.
		{"Bellona", "Bellona Merkez", "", "", "Bellona Merkez"},
	} {
		if got := DisplayName(c.brand, c.branch, c.province, c.district); got != c.want {
			t.Errorf("DisplayName(%q,%q,%q,%q)\n got %q\nwant %q", c.brand, c.branch, c.province, c.district, got, c.want)
		}
	}
}

// The chain's own filing shorthand comes off; words the whole trade uses stay on.
func TestStripHouseWordsLeavesWhatNamesTheShop(t *testing.T) {
	house := map[string]bool{"yb": true, "prk": true, "shw": true}
	for _, c := range []struct{ in, want string }{
		{"ORAN YB PRK SHW CAD", "ORAN CAD"},
		{"KENTPARK YB PRK SHW AVM", "KENTPARK AVM"},
		// Nothing to strip.
		{"Akdeniz Bulvarı", "Akdeniz Bulvarı"},
		// A name that is nothing but shorthand keeps it: shorthand beats an empty name.
		{"YB PRK SHW", "YB PRK SHW"},
	} {
		if got := StripHouseWords(c.in, house); got != c.want {
			t.Errorf("StripHouseWords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// With nothing measured, nothing is removed.
	if got := StripHouseWords("ORAN YB PRK SHW CAD", nil); got != "ORAN YB PRK SHW CAD" {
		t.Errorf("stripped without evidence: %q", got)
	}
}

// A shop whose own sign names the chain, a few metres from where the chain says its shop
// is, is that shop -- and it is the case trigram similarity is worst at.
func TestNamedByTheChainIsEvidenceSimilarityMisses(t *testing.T) {
	brand := CompactName("Bellona")
	legacy := CompactName("Çelik Mağazacılık Konyaaltı Bellona")
	ours := CompactName("Bellona - Antalya Çelık Centroom Konyaaltı")
	if !strings.Contains(legacy, brand) {
		t.Fatalf("the chain's name is not found in %q", legacy)
	}
	// The pair this rule exists for: neither containment nor similarity would have merged
	// them, and they are seven metres apart on the ground.
	if containment(ours, legacy) {
		t.Errorf("containment already covers this pair; the rule would be redundant")
	}
}

// A chain tells us which of its capital I's is an İ by writing the dot on another row.
func TestSpellingsLearnFromThePublishersOwnRows(t *testing.T) {
	names := []string{
		"ÇELİK CENTROOM ALTINTAŞ",
		"ÇELIK CENTROOM KONYAALTI",
		"ÖZLEM MOBİLYA",
		"NIZAMLAR MOBILYA ALEMDAĞ",
		// Nobody writes these with a dot, so nothing may invent one.
		"KIRŞEHİR ŞUBE",
		"TÜRKYILMAZLAR",
	}
	spellings := Spellings(names)
	for word, want := range map[string]string{"ÇELIK": "ÇELİK", "MOBILYA": "MOBİLYA"} {
		if spellings[word] != want {
			t.Errorf("%q -> %q, want %q", word, spellings[word], want)
		}
	}
	for _, word := range []string{"KIRŞEHİR", "TÜRKYILMAZLAR", "ALTINTAŞ", "KONYAALTI"} {
		if fixed, ok := spellings[word]; ok {
			t.Errorf("invented a dot: %q -> %q", word, fixed)
		}
	}
	if got := RepairSpelling("ÇELIK CENTROOM KONYAALTI", spellings); got != "ÇELİK CENTROOM KONYAALTI" {
		t.Errorf("RepairSpelling = %q", got)
	}
	// And the name a person finally sees.
	if got := TidyName(RepairSpelling("ÇELIK CENTROOM KONYAALTI", spellings)); got != "Çelik Centroom Konyaaltı" {
		t.Errorf("TidyName = %q", got)
	}
}

// An identifier we made up carries none of the authority a chain's own does.
func TestDerivedIdentifiersAreNotTheChainSpeaking(t *testing.T) {
	if published(DerivedID(RawStore{Name: "Polo Mobilya"})) {
		t.Error("a derived identifier was read as the chain's own")
	}
	if !published("3771") || !published("BEL-867") {
		t.Error("a published identifier was read as ours")
	}
	if published("") {
		t.Error("no identifier at all was read as the chain's own")
	}
}

// A point with no readable place beside it is still a shop somewhere, and the place it is
// in is a fact about the point. The nearest neighbourhood answers it; a point far from
// every neighbourhood in the country answers nothing rather than guessing.
func TestAPointNamesItsOwnPlaceOrSaysNothing(t *testing.T) {
	r := &Resolver{neighbourhoods: []placed{
		{point{40.9923, 29.0275}, Place{City: "İstanbul", District: "Kadıköy"}},
		{point{40.9780, 29.0900}, Place{City: "İstanbul", District: "Maltepe"}},
		{point{39.9200, 32.8540}, Place{City: "Ankara", District: "Çankaya"}},
	}}
	if got := r.placeFromPoint(40.9910, 29.0290, ""); got.District != "Kadıköy" {
		t.Fatalf("a point in Kadıköy was placed in %q", got.District)
	}
	// Restricted to a province, the neighbourhoods of every other one are not candidates.
	if got := r.placeFromPoint(39.9210, 32.8550, "İstanbul"); got.District != "" {
		t.Fatalf("a point in Ankara was given the İstanbul district %q", got.District)
	}
	// The Black Sea, 100 km off the coast: no neighbourhood is near it and none is claimed.
	if got := r.placeFromPoint(42.4000, 31.0000, ""); got.City != "" {
		t.Fatalf("a point at sea was placed in %q", got.City)
	}
}
