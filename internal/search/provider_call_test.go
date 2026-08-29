package search

import (
	"context"
	"errors"
	"testing"
)

type recordingPlaces struct {
	calls  int
	query  string
	radius int
	places []Place
	err    error
}

func (p *recordingPlaces) TextSearch(_ context.Context, query string, _, _ *float64, radius int) ([]Place, error) {
	p.calls++
	p.query, p.radius = query, radius
	return p.places, p.err
}

func (p *recordingPlaces) PlaceDetails(context.Context, string) (Place, error) {
	return Place{}, errors.New("unexpected place details call")
}

// The gate changed when the provider is asked, not what happens once it is. This pins the
// second half: the same query, the same radius rule for a named store, and the same
// home-and-living filter applied at the door rather than to the results later -- because
// anything that gets past the door is imported into the catalogue and into the sitemap.
func TestTheProviderCallItselfIsUnchanged(t *testing.T) {
	places := &recordingPlaces{places: []Place{
		{PlaceID: "shop", Name: "Modalife Mobilya", Types: []string{"furniture_store"}},
		{PlaceID: "bakery", Name: "Modalife Pastanesi", Types: []string{"bakery"}},
	}}
	s := &Service{places: places}

	got, reason := s.providerSearch(t.Context(), Intent{StoreName: "Modalife"}, Request{Query: "Modalife", RadiusMeters: 5000}, "tr")
	if reason != "" {
		t.Fatalf("reason=%q, want none", reason)
	}
	if len(got) != 1 || got[0].PlaceID != "shop" {
		t.Fatalf("places=%+v, want the bakery dropped at the door", got)
	}
	if places.query != "Modalife" {
		t.Errorf("query=%q, want the parsed store name", places.query)
	}
	// A named store is worth finding wherever it is, so it keeps the wide radius.
	if places.radius != localHorizonMeters {
		t.Errorf("radius=%d, want %d", places.radius, localHorizonMeters)
	}
}

// A provider that is down degrades the search; it does not fail it, and it says so.
func TestAnUnavailableProviderIsReportedRatherThanFatal(t *testing.T) {
	s := &Service{places: &recordingPlaces{err: errors.New("503")}}
	got, reason := s.providerSearch(t.Context(), Intent{}, Request{Query: "halı", RadiusMeters: 5000}, "tr")
	if got != nil || reason != "places_unavailable" {
		t.Fatalf("places=%+v reason=%q", got, reason)
	}
}

// With no provider configured there is nothing to call and nothing to report.
func TestNoProviderConfiguredIsSilent(t *testing.T) {
	s := &Service{}
	if got, reason := s.providerSearch(t.Context(), Intent{}, Request{Query: "halı", RadiusMeters: 5000}, "tr"); got != nil || reason != "" {
		t.Fatalf("places=%+v reason=%q", got, reason)
	}
}
