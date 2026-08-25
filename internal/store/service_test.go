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
