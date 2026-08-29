package search

import "testing"

// Two people a street apart are asking the same question, and the answer they get should
// be bought once. The key rounds their positions together and ignores how they capitalised
// it -- but never runs two different questions, radii or languages together, because the
// answers to those genuinely differ.
func TestPlacesCacheKeyGroupsTheSameQuestion(t *testing.T) {
	at := func(v float64) *float64 { return &v }
	base := placesCacheKey("halı mağazası", at(36.8841), at(30.7056), 5000, "tr")

	same := []struct{ name, got string }{
		{"capitals, dotless I and all", placesCacheKey("HALI MAĞAZASI", at(36.8841), at(30.7056), 5000, "tr")},
		{"whitespace", placesCacheKey("  halı mağazası ", at(36.8841), at(30.7056), 5000, "tr")},
		{"a street away", placesCacheKey("halı mağazası", at(36.8843), at(30.7051), 5000, "tr")},
	}
	for _, c := range same {
		if c.got != base {
			t.Errorf("%s: got %q, want it to match %q", c.name, c.got, base)
		}
	}

	different := []struct{ name, got string }{
		{"another query", placesCacheKey("perde", at(36.8841), at(30.7056), 5000, "tr")},
		{"another town", placesCacheKey("halı mağazası", at(41.0082), at(28.9784), 5000, "tr")},
		{"another radius", placesCacheKey("halı mağazası", at(36.8841), at(30.7056), 20000, "tr")},
		{"another language", placesCacheKey("halı mağazası", at(36.8841), at(30.7056), 5000, "en")},
		{"no location", placesCacheKey("halı mağazası", nil, nil, 5000, "tr")},
	}
	for _, c := range different {
		if c.got == base {
			t.Errorf("%s: key collided with %q", c.name, base)
		}
	}
}
