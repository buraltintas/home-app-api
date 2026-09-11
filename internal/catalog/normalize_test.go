package catalog

import "testing"

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
