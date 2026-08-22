package search

import (
	"math"
	"testing"
)

// The box has to actually contain the radius it claims, or a suggestion list quietly
// stops including the far side of a city.
func TestBoundingBoxCoversRadius(t *testing.T) {
	for _, latitude := range []float64{0, 36.9, 41.0, 60.0, -33.9} {
		minLat, maxLat, minLon, maxLon := boundingBox(latitude, 30.7)
		if maxLat-latitude < suggestionRadiusKm/111.5 {
			t.Fatalf("latitude span too small at %v", latitude)
		}
		if latitude-minLat < suggestionRadiusKm/111.5 {
			t.Fatalf("latitude span asymmetric at %v", latitude)
		}
		widthKm := (maxLon - minLon) / 2 * 111.0 * math.Cos(latitude*math.Pi/180)
		if widthKm < suggestionRadiusKm*0.99 {
			t.Fatalf("longitude span %v km covers less than the radius at %v", widthKm, latitude)
		}
	}
}

// Near the poles the cosine approaches zero and an unclamped division produces an
// infinite box, which would offer someone in Antalya what was searched in Alaska.
func TestBoundingBoxStaysFiniteAtThePole(t *testing.T) {
	_, _, minLon, maxLon := boundingBox(89.999, 0)
	if math.IsInf(maxLon-minLon, 0) || math.IsNaN(maxLon-minLon) {
		t.Fatal("longitude span is not finite at the pole")
	}
}

// The privacy argument for publishing other people's searches rests entirely on this
// number. A change to it is a change to what the endpoint discloses, so it is asserted.
func TestSuggestionsRequireSeveralSearchers(t *testing.T) {
	if distinctSearchers < 3 {
		t.Fatalf("a suggestion may not be shown to the public below three distinct searchers, got %d", distinctSearchers)
	}
}
