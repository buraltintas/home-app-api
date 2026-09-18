package store

import "testing"

func TestValidCoordinates(t *testing.T) {
	for _, x := range [][2]float64{{41.0082, 28.9784}, {-90, -180}, {90, 180}} {
		if !ValidCoordinates(x[0], x[1]) {
			t.Fatalf("rejected %v", x)
		}
	}
	for _, x := range [][2]float64{{91, 0}, {0, 181}, {-91, 0}} {
		if ValidCoordinates(x[0], x[1]) {
			t.Fatalf("accepted %v", x)
		}
	}
}

func TestAnyWordStoreFallbackIgnoresLocationAndGenericStoreWords(t *testing.T) {
	if got := anyWordQuery("yeğenler elektrik antalya"); got != "yeğenler | elektrik" {
		t.Fatalf("fallback query=%q", got)
	}
	if got := anyWordQuery("Antalya mağaza"); got != "" {
		t.Fatalf("weak-only fallback query=%q", got)
	}
}

func TestNearbyLimitFallsBackRatherThanFailing(t *testing.T) {
	for _, requested := range []int{0, -1, 25, 1000} {
		if got := nearbyLimit(requested); got != 6 {
			t.Fatalf("nearbyLimit(%d)=%d, want the default 6", requested, got)
		}
	}
	for _, requested := range []int{1, 6, 24} {
		if got := nearbyLimit(requested); got != requested {
			t.Fatalf("nearbyLimit(%d)=%d, want it honoured", requested, got)
		}
	}
}

// The neighbours block shows the same picture the rest of the product shows, and shows
// nothing at all rather than a broken frame when a shop has no brand behind it.
func TestNearbyPhotoFollowsTheSameRuleAsEverywhereElse(t *testing.T) {
	var uploaded Item
	assignPhoto(&uploaded, "3f6c1f0e-0000-4000-8000-000000000001")
	if uploaded.Photo == nil || uploaded.Photo.Source != "admin" {
		t.Fatalf("an administrator's upload should win, got %+v", uploaded.Photo)
	}
	branded := Item{BrandSlug: "english-home"}
	assignPhoto(&branded, "")
	if branded.Photo == nil || branded.Photo.Source != "brand" || branded.Photo.BrandSlug != "english-home" {
		t.Fatalf("a chain's mark should stand in, got %+v", branded.Photo)
	}
	var bare Item
	assignPhoto(&bare, "")
	if bare.Photo != nil {
		t.Fatalf("an independent shop has no picture, got %+v", bare.Photo)
	}
}

// A page has to be worth opening before it is worth publishing. Below ten shops a
// city-and-category page is a list of two things dressed up as a guide, which is thin
// content to a search engine and a wasted tap to a reader.
func TestCityCategoryPagesNeedEnoughShopsToBeWorthPublishing(t *testing.T) {
	if cityCategoryMinimum < 10 {
		t.Fatalf("threshold too low to keep a page honest, got %d", cityCategoryMinimum)
	}
}
