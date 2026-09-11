package textnorm

import "testing"

func TestKeyFoldsTurkishNamesToWhatPeopleType(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// The dotted and dotless I are the whole reason this package exists: a person
		// types "kadikoy" and the registry prints "KADIKÖY", and both have to become the
		// same key or the picker answers nothing.
		{"Kadıköy", "kadikoy"},
		{"KADIKÖY", "kadikoy"},
		{"İstanbul", "istanbul"},
		{"ISTANBUL", "istanbul"},
		{"Iğdır", "igdir"},
		{"Şişli", "sisli"},
		{"Çankaya", "cankaya"},
		{"Üsküdar", "uskudar"},
		{"Balıkesir", "balikesir"},
		// Punctuation in a printed place name is decoration; nobody types it.
		{"Kadıköy/İstanbul", "kadikoy istanbul"},
		{"  Akören  Mah.  ", "akoren mah"},
		{"19 Mayıs", "19 mayis"},
		{"", ""},
		{"...", ""},
	} {
		if got := Key(c.in); got != c.want {
			t.Errorf("Key(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestFoldLeavesLettersAndCaseAlone(t *testing.T) {
	// Fold strips marks; it is not a lowercaser. Normalize is what lowercases, and the two
	// are kept separate because search compares normalized text in one place and folded
	// text in another.
	if got := Fold("Ağrı"); got != "Agri" {
		t.Errorf("Fold(\"Ağrı\")=%q want %q", got, "Agri")
	}
	// Normalize lowercases by the Unicode default, not by Turkish rules, so "I" becomes
	// a dotted "i" rather than "ı". That is deliberate and safe here only because Fold
	// runs after it and maps both to "i" -- which is why nothing should compare the
	// output of Normalize to a Turkish word without folding it first.
	if got := Normalize("  AĞRI  "); got != "ağri" {
		t.Errorf("Normalize=%q want %q", got, "ağri")
	}
	if Key("AĞRI") != Key("Ağrı") {
		t.Errorf("Key disagrees on the two spellings of Ağrı: %q vs %q", Key("AĞRI"), Key("Ağrı"))
	}
}
