//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/burakaltintas/home-app-api/internal/search"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
)

type fixedIntentParser struct{ intent search.Intent }

func (p fixedIntentParser) ParseSearchIntent(context.Context, string, search.Context) (search.Intent, error) {
	return p.intent, nil
}

// Asking for a shop by its name finds it. Search answers from the catalogue alone now, so
// this is the whole of a store lookup: the row is in PostgreSQL, the query names it, and it
// comes back. The intent is fixed so the test is about the catalogue, not the classifier.
func TestSearchFindsANamedStoreInTheCatalogue(t *testing.T) {
	db := database(t)
	id := uuid.New()
	name := "Yerel Mobilya " + id.String()[:8]
	if _, err := db.Exec(t.Context(), `INSERT INTO stores(id,name,slug,city,district,address,location) VALUES($1,$2,$3,'Antalya','Muratpaşa','Cadde No:3, Muratpaşa/Antalya, Türkiye',ST_SetSRID(ST_MakePoint(30.70,36.88),4326)::geography)`, id, name, "named-"+id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO store_stats(store_id) VALUES($1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug='furniture' ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatal(err)
	}
	report, err := reporting.NewService(db, "Europe/Istanbul", 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parser := fixedIntentParser{intent: search.Intent{
		Scope: search.ScopeHomeLiving, QueryLanguage: i18n.LocaleTR,
		NormalizedQuery: name, StoreName: name, Categories: []string{"furniture"},
		ProductTerms: []string{}, StyleTerms: []string{}, Attributes: []string{}, SemanticTerms: []string{},
	}}
	svc := search.NewService(db, storepkg.NewService(db, report), parser, "", 3, report, 72*time.Hour, 24*time.Hour)

	response, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, search.Request{Query: name})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range response.Results {
		found = found || result.ID != nil && *result.ID == id
	}
	if !found {
		t.Fatalf("named store missing from results: %+v", response.Results)
	}
}
