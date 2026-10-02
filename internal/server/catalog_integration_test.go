//go:build integration

package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	appmw "github.com/burakaltintas/home-app-api/internal/middleware"
	"github.com/burakaltintas/home-app-api/internal/readcache"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	searchpkg "github.com/burakaltintas/home-app-api/internal/search"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/burakaltintas/home-app-api/internal/social"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type catalogueRig struct {
	server  *Server
	router  http.Handler
	db      *pgxpool.Pool
	queries *atomic.Int64
	tokens  *security.TokenManager
	shops   []string
	quiet   string
	review  string
}

// newCatalogueRig is the API's catalogue routes over a throwaway local database holding a
// small catalogue, with every database round trip counted.
func newCatalogueRig(t *testing.T) *catalogueRig {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL/PostGIS integration suite")
	}
	if parsed, e := url.Parse(raw); e != nil || !map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}[parsed.Hostname()] {
		t.Fatal("refusing to seed a test catalogue anywhere but a throwaway local database")
	}
	activity := &database.Activity{}
	db, e := database.OpenWatched(context.Background(), raw, activity)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Close)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := db.Exec(context.Background(), sql, args...); e != nil {
			t.Fatal(sql, e)
		}
	}
	clean := func() {
		exec(`DELETE FROM posts WHERE store_id IN (SELECT id FROM stores WHERE slug LIKE 'rigtest-%')`)
		exec(`DELETE FROM stores WHERE slug LIKE 'rigtest-%'`)
		exec(`DELETE FROM brands WHERE slug='rigtest-yatas'`)
		exec(`DELETE FROM user_profiles WHERE display_name='Rig Tester'`)
		exec(`DELETE FROM users WHERE primary_email::text LIKE 'rigtest-%'`)
	}
	clean()
	t.Cleanup(clean)
	rig := &catalogueRig{db: db, queries: &atomic.Int64{}}
	brand := uuid.New()
	exec(`INSERT INTO brands(id,slug,name) VALUES($1,'rigtest-yatas','Yataş')`, brand)
	for i := 0; i < 14; i++ {
		id := uuid.New()
		slug := fmt.Sprintf("rigtest-%02d", i)
		var b any
		if i < 4 {
			b = brand
		}
		exec(`INSERT INTO stores(id,name,slug,city,district,location,brand_id) VALUES($1,$2,$3,'Rigkent','Merkez',ST_SetSRID(ST_MakePoint($5,$4),4326)::geography,$6)`,
			id, fmt.Sprintf("Rig Mağaza %02d", i), slug, 40.0+float64(i)*0.003, 29.0+float64(i)*0.002, b)
		exec(`INSERT INTO store_stats(store_id) VALUES($1)`, id)
		exec(`INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug='furniture'`, id)
		rig.shops = append(rig.shops, slug)
	}
	rig.quiet, rig.review = rig.shops[1], rig.shops[2]
	user := uuid.New()
	exec(`INSERT INTO users(id,primary_email) VALUES($1,$2)`, user, "rigtest-"+user.String()+"@example.test")
	exec(`INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2::text::citext,'Rig Tester')`, user, "rig_"+user.String()[:8])
	exec(`INSERT INTO posts(user_id,store_id,body,rating,verification_distance_meters,verified_at) SELECT $1,id,'İyi','5',10,now() FROM stores WHERE slug=$2`, user, rig.review)
	exec(`UPDATE store_stats SET review_count=1,post_count=1,rating_count=1,average_rating=5 WHERE store_id=(SELECT id FROM stores WHERE slug=$1)`, rig.review)

	report, e := reporting.NewService(db, "Europe/Istanbul", 72*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	stores := storepkg.NewService(db, report)
	socialSvc := social.NewService(db, social.Config{ReviewRadiusMeters: 500, VisitProofTTL: time.Hour, MaxLocationAccuracyMeters: 100}, report)
	search := searchpkg.NewService(db, stores, nil, "", 3, report, 72*time.Hour, 24*time.Hour)
	rig.server = NewServer(db, nil, stores, socialSvc, search, nil, nil, nil, nil, report, nil, []byte("rig-hash-key-rig-hash-key-rig-hash"))
	rig.server.SetReadCache(readcache.New(1<<20, time.Hour))
	catalog := storepkg.NewCatalog(db, activity, 6*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e := catalog.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
	rig.server.SetCatalog(catalog, false)
	activity.OnUse(func() { rig.queries.Add(1) })
	rig.tokens = security.NewTokenManager("rig-access-secret-more-than-32-bytes-long", time.Hour, time.Hour)
	r := chi.NewRouter()
	r.Use(appmw.RequestLocale(i18n.DefaultLocale), appmw.OptionalAuth(rig.tokens))
	r.Get("/v1/categories", rig.server.storeCategories)
	r.Get("/v1/search/highlights", rig.server.searchHighlights)
	r.Get("/v1/search/popular-cities", rig.server.searchPopularCities)
	r.Get("/v1/stores/index", rig.server.storeIndex)
	r.Get("/v1/discovery/city-categories", rig.server.cityCategories)
	r.Get("/v1/discovery/stores", rig.server.cityCategoryStores)
	r.Get("/v1/discovery/city-brands", rig.server.cityBrands)
	r.Get("/v1/discovery/brand-stores", rig.server.cityBrandStores)
	r.Get("/v1/stores/{id}", rig.server.storeDetail)
	r.Get("/v1/stores/{id}/nearby", rig.server.storeNearby)
	rig.router = r
	return rig
}

func (rig *catalogueRig) get(t *testing.T, path string, header ...string) (*httptest.ResponseRecorder, int64) {
	t.Helper()
	before := rig.queries.Load()
	req := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	rig.router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%s answered %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec, rig.queries.Load() - before
}

func (rig *catalogueRig) paths() []string {
	return []string{
		"/v1/stores/" + rig.quiet, "/v1/stores/" + strings.ToUpper(rig.quiet), "/v1/stores/" + rig.quiet + "/nearby?limit=6",
		"/v1/stores/" + rig.review + "/nearby", "/v1/discovery/city-categories?minimum=1", "/v1/discovery/stores?city=rigkent&category=furniture&limit=5&offset=2",
		"/v1/discovery/city-brands?minimum=1", "/v1/discovery/brand-stores?city=rigkent&brand=rigtest-yatas", "/v1/stores/index?limit=5000", "/v1/categories",
	}
}

// The whole point: a crawl of the catalogue, in every language, makes no round trip to the
// database at all.
func TestAnonymousCatalogueReadsDoNotTouchTheDatabase(t *testing.T) {
	rig := newCatalogueRig(t)
	for _, locale := range i18n.Supported() {
		for _, path := range rig.paths() {
			rec, queries := rig.get(t, path, "X-Locale", string(locale))
			if rec.Header().Get("X-Cache") != "snapshot" || queries != 0 {
				t.Fatalf("%s (%s): X-Cache %q, %d database round trips", path, locale, rec.Header().Get("X-Cache"), queries)
			}
		}
	}
}

// What stays with the database stays there: the shop with a review (the owner's exception),
// and anybody signed in.
func TestReviewedShopsAndSignedInReadersStillReadTheDatabase(t *testing.T) {
	rig := newCatalogueRig(t)
	rec, queries := rig.get(t, "/v1/stores/"+rig.review)
	if rec.Header().Get("X-Cache") == "snapshot" || queries == 0 || !strings.Contains(rec.Body.String(), `"recent_posts":[{`) {
		t.Fatalf("a reviewed shop was not read from the database: %s", rec.Header().Get("X-Cache"))
	}
	token, _, e := rig.tokens.Access(uuid.New(), uuid.New(), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	rec, queries = rig.get(t, "/v1/stores/"+rig.quiet, "Authorization", "Bearer "+token)
	if rec.Header().Get("X-Cache") != "" || queries == 0 {
		t.Fatalf("a signed-in reader was answered from shared memory: %q", rec.Header().Get("X-Cache"))
	}
}

// A change written by this process shows on the next anonymous read; a change written by
// anybody else waits for the copy's next read, which is the one effect a reader can see.
func TestAWriteHereIsVisibleOnTheNextRead(t *testing.T) {
	rig := newCatalogueRig(t)
	rename := func(name string) {
		if _, e := rig.db.Exec(context.Background(), `UPDATE stores SET name=$1 WHERE slug=$2`, name, rig.quiet); e != nil {
			t.Fatal(e)
		}
	}
	rename("Elsewhere Renamed")
	if rec, _ := rig.get(t, "/v1/stores/"+rig.quiet); strings.Contains(rec.Body.String(), "Elsewhere Renamed") {
		t.Fatal("a change made outside this process appeared before the copy was read again")
	}
	rename("Renamed Here")
	rig.server.catalogueChanged()
	rec, queries := rig.get(t, "/v1/stores/"+rig.quiet)
	if !strings.Contains(rec.Body.String(), "Renamed Here") || queries == 0 {
		t.Fatalf("a change made here did not show on the next read: %s", rec.Body.String())
	}
	if _, queries = rig.get(t, "/v1/discovery/stores?city=rigkent&category=furniture"); queries != 0 {
		t.Fatalf("the catalogue read again for a second request (%d round trips)", queries)
	}
	// A favourite: only that shop's page goes to the database, nothing is read again.
	id := uuid.MustParse(gjsonID(t, rec.Body.Bytes()))
	rig.server.storePageChanged(id)
	if rec, queries = rig.get(t, "/v1/stores/"+rig.quiet); rec.Header().Get("X-Cache") == "snapshot" || queries == 0 {
		t.Fatal("the changed page was answered from before the change")
	}
	if rec, queries = rig.get(t, "/v1/stores/"+rig.shops[5]); rec.Header().Get("X-Cache") != "snapshot" || queries != 0 {
		t.Fatal("another shop's page was sent to the database by one shop's change")
	}
}

func gjsonID(t *testing.T, body []byte) string {
	t.Helper()
	i := bytes.Index(body, []byte(`"id":"`))
	if i < 0 {
		t.Fatalf("no id in %s", body)
	}
	return string(body[i+6 : i+42])
}

// The database going away while a refresh is due changes nothing for a reader: the copy that
// was answering goes on answering.
func TestAnUnreachableDatabaseLeavesTheCopyAnswering(t *testing.T) {
	rig := newCatalogueRig(t)
	// The copy reads through a connection of its own, which is then taken away, and it is
	// always past its maximum age, so every read tries to refresh it and fails.
	gone, e := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	catalog := storepkg.NewCatalog(gone, &database.Activity{}, time.Nanosecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e := catalog.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
	rig.server.SetCatalog(catalog, false)
	gone.Close()
	for _, path := range rig.paths() {
		if rec, queries := rig.get(t, path); rec.Header().Get("X-Cache") != "snapshot" || queries != 0 {
			t.Fatalf("%s was not answered from the copy with the database gone", path)
		}
	}
}

// Shadow mode answers from the database and compares; on an unchanged catalogue the copy
// agrees with every answer.
func TestShadowModeFindsNoDifferenceOnAnUnchangedCatalogue(t *testing.T) {
	rig := newCatalogueRig(t)
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	rig.server.catalogShadow = true
	for _, locale := range i18n.Supported() {
		for _, path := range rig.paths() {
			if rec, queries := rig.get(t, path, "X-Locale", string(locale)); rec.Header().Get("X-Cache") == "snapshot" || queries == 0 {
				t.Fatalf("%s: shadow mode served the copy", path)
			}
		}
	}
	if strings.Contains(logged.String(), "differs") {
		t.Fatalf("shadow mode found differences:\n%s", logged.String())
	}
	if _, e := rig.db.Exec(context.Background(), `UPDATE stores SET name='Changed Elsewhere' WHERE slug=$1`, rig.shops[7]); e != nil {
		t.Fatal(e)
	}
	rig.get(t, "/v1/stores/"+rig.shops[7])
	if !strings.Contains(logged.String(), "catalog snapshot differs from the database") || !strings.Contains(logged.String(), ".store.name") {
		t.Fatalf("a real difference went unreported:\n%s", logged.String())
	}
}
