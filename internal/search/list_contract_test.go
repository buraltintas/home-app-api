package search

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchListExternalCannotExposeDetailTierFields(t *testing.T) {
	p := Place{
		PlaceID: "place-1", Rating: 4.9, RatingCount: 912, Phone: "0212 000 00 00",
		Website: "https://example.test", Hours: &OpeningHours{},
		PhotoName: "places/store/photos/photo", BusinessStatus: "OPERATIONAL",
	}
	raw, err := json.Marshal(listExternal(p))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, forbidden := range []string{"rating", "rating_count", "phone", "website", "opening_hours"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("list DTO exposed %q in %s", forbidden, got)
		}
	}
	for _, required := range []string{"place-1", "photo_name", "business_status"} {
		if !strings.Contains(got, required) {
			t.Errorf("list DTO lost %q in %s", required, got)
		}
	}
}
