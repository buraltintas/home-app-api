package search

import "testing"

func TestPopularCitiesUseRollingMonth(t *testing.T) {
	if popularCityWindowDays != 30 {
		t.Fatalf("popular cities must use a 30-day window, got %d", popularCityWindowDays)
	}
}

func TestPopularCitiesRequireThreeSearches(t *testing.T) {
	if popularCityMinSearches != 3 {
		t.Fatalf("public city trends must require three searches, got %d", popularCityMinSearches)
	}
}

func TestPopularCitiesHaveBoundedLimit(t *testing.T) {
	if popularCityMaxLimit != 10 {
		t.Fatalf("popular city limit changed unexpectedly: %d", popularCityMaxLimit)
	}
}
