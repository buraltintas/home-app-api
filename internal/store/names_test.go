package store

import (
	"context"
	"testing"
)

func TestCollapseNamesKeepsTheShortNameAndDropsWhatOnlyLengthensIt(t *testing.T) {
	found := []NameSuggestion{
		{Name: "Yataş Bedding", key: "yatas bedding"},
		{Name: "Yatsan", key: "yatsan"},
		{Name: "Yatak Baza Başlık", key: "yatak baza baslik"},
		{Name: "Yatsan Urla", key: "yatsan urla"},
		{Name: "Yatsanlar Mobilya", key: "yatsanlar mobilya"},
	}
	got := collapseNames(found, 6)
	want := []string{"Yataş Bedding", "Yatsan", "Yatak Baza Başlık", "Yatsanlar Mobilya"}
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d: %+v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("name %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestCollapseNamesNeverDropsAChain(t *testing.T) {
	found := []NameSuggestion{
		{Name: "Yataş", key: "yatas"},
		{Name: "Yataş Bedding", BrandSlug: "yatas-bedding", key: "yatas bedding"},
		{Name: "Karaca", BrandSlug: "karaca", key: "karaca"},
		{Name: "Karaca Home", BrandSlug: "karaca-home", key: "karaca home"},
	}
	got := collapseNames(found, 6)
	want := []string{"Yataş Bedding", "Karaca", "Karaca Home"}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %v", got, want)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("name %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestCollapseNamesStopsAtTheLimit(t *testing.T) {
	found := []NameSuggestion{{Name: "A", key: "a"}, {Name: "B", key: "b"}, {Name: "C", key: "c"}}
	if got := collapseNames(found, 2); len(got) != 2 {
		t.Fatalf("got %d names, want 2", len(got))
	}
}

// One letter answers with half the catalogue, so it is not asked at all -- and nothing
// reaches the database, which this test proves by having none.
func TestSuggestNamesNeedsTwoLetters(t *testing.T) {
	s := &Service{}
	for _, typed := range []string{"", " ", "a", "İ", "-"} {
		got, e := s.SuggestNames(context.Background(), typed, 41, 29, 6)
		if e != nil || len(got) != 0 {
			t.Fatalf("SuggestNames(%q) = %v, %v; want nothing", typed, got, e)
		}
	}
}
