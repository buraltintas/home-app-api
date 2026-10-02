//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
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

	adminpkg "github.com/burakaltintas/home-app-api/internal/admin"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	appmw "github.com/burakaltintas/home-app-api/internal/middleware"
	"github.com/burakaltintas/home-app-api/internal/readcache"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	searchpkg "github.com/burakaltintas/home-app-api/internal/search"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/burakaltintas/home-app-api/internal/social"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	userpkg "github.com/burakaltintas/home-app-api/internal/user"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type catalogueRig struct {
	server   *Server
	router   http.Handler
	db       *pgxpool.Pool
	activity *database.Activity
	queries  *atomic.Int64
	tokens   *security.TokenManager
	shops    []string
	ids      map[string]uuid.UUID
	quiet    string
	review   string
	// Everybody the rig signed in, so they can be removed whatever became of their address.
	people []uuid.UUID
}

// The address the rig's administrator signs in with; the admin routes allow it alone.
const rigAdmin = "rigtest-admin@example.test"

// newCatalogueRig is the API's catalogue routes, and the routes that write to the
// catalogue, over a throwaway local database holding a small catalogue, with every database
// round trip counted.
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
	rig := &catalogueRig{db: db, activity: activity, queries: &atomic.Int64{}, ids: map[string]uuid.UUID{}}
	clean := func() {
		exec(`DELETE FROM posts WHERE store_id IN (SELECT id FROM stores WHERE slug LIKE 'rigtest-%') OR user_id=ANY($1)`, rig.people)
		exec(`DELETE FROM stores WHERE slug LIKE 'rigtest-%' OR name LIKE 'Rigtest %'`)
		exec(`DELETE FROM brands WHERE slug='rigtest-yatas'`)
		exec(`DELETE FROM admin_actions WHERE actor_email::text LIKE 'rigtest-%'`)
		exec(`DELETE FROM user_profiles WHERE display_name='Rig Tester' OR user_id=ANY($1)`, rig.people)
		exec(`DELETE FROM users WHERE primary_email::text LIKE 'rigtest-%' OR id=ANY($1)`, rig.people)
	}
	clean()
	t.Cleanup(clean)
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
		rig.ids[slug] = id
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
	rig.server = NewServer(db, nil, stores, socialSvc, search, nil, userpkg.NewService(db, report), nil, adminpkg.NewService(db), report, nil, []byte("rig-hash-key-rig-hash-key-rig-hash"))
	rig.fresh(t)
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
	// Every route that writes something an anonymous catalogue read shows, behind the same
	// checks as in Router. Importing a chain (it calls the provider) and settling the match
	// queue (it needs an import run) are the two left out.
	r.Group(func(r chi.Router) {
		r.Use(appmw.ActiveAccount(db), appmw.RequireAuth)
		r.Post("/v1/posts", rig.server.createPost)
		r.Delete("/v1/posts/{id}", rig.server.deletePost)
		r.Post("/v1/stores/{id}/favorite", rig.server.favorite)
		r.Delete("/v1/stores/{id}/favorite", rig.server.unfavorite)
		r.Delete("/v1/me", rig.server.deleteAccount)
		r.Route("/v1/admin", func(r chi.Router) {
			r.Use(appmw.RequireAdmin(db, []string{rigAdmin}))
			r.Post("/stores", rig.server.adminCreateStore)
			r.Post("/stores/{id}/premium", rig.server.adminSetPremium)
			r.Post("/stores/{id}/merge", rig.server.adminMergeStores)
			r.Post("/stores/{id}/catalog", rig.server.adminSetCatalogStore)
			r.Post("/stores/{id}/categories", rig.server.adminSetStoreCategories)
			r.Delete("/stores/{id}/cover", rig.server.adminClearStoreCover)
			r.Delete("/users/{id}", rig.server.adminDeleteUser)
			r.Delete("/reviews/{id}", rig.server.adminDeleteReview)
			r.Post("/moderation/{id}", rig.server.adminDecideReview)
		})
	})
	rig.router = r
	return rig
}

// fresh reads the catalogue again and empties the read cache, as a new instance would have
// it, so each write starts from a copy that holds the state before it.
func (rig *catalogueRig) fresh(t *testing.T) {
	t.Helper()
	catalog := storepkg.NewCatalog(rig.db, rig.activity, 6*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e := catalog.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
	rig.server.SetCatalog(catalog, false)
	rig.server.SetReadCache(readcache.New(1<<20, time.Hour))
}

// signIn makes a person with a live session and returns them with an access token.
func (rig *catalogueRig) signIn(t *testing.T, email string) (uuid.UUID, string) {
	t.Helper()
	user, session := uuid.New(), uuid.New()
	rig.people = append(rig.people, user)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,primary_email) VALUES($1,$2)`, []any{user, email}},
		{`INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2::text::citext,'Rig Tester')`, []any{user, "rig_" + user.String()[:8]}},
		{`INSERT INTO auth_sessions(id,user_id,family_id,refresh_token_hash,expires_at) VALUES($1,$2,gen_random_uuid(),$3,now()+interval '1 hour')`, []any{session, user, []byte(session.String())}},
	} {
		if _, e := rig.db.Exec(context.Background(), q.sql, q.args...); e != nil {
			t.Fatal(q.sql, e)
		}
	}
	token, _, e := rig.tokens.Access(user, session, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return user, token
}

// send makes a signed-in request and insists on the status the route answers with.
func (rig *catalogueRig) send(t *testing.T, method, path, token string, body any, want int) []byte {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		payload = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, payload)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	rig.router.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec.Body.Bytes()
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
	// The copy was read under a minute ago, so the catalogue is not read again yet: the
	// database answers until it is (see Catalog: at most one read a minute for writes here).
	if rec, queries = rig.get(t, "/v1/discovery/stores?city=rigkent&category=furniture"); rec.Header().Get("X-Cache") == "snapshot" || !strings.Contains(rec.Body.String(), "Renamed Here") {
		t.Fatalf("a list was answered from the copy from before the change: %s", rec.Body.String())
	}
	// A favourite: only that shop's page goes to the database, nothing is read again.
	rig.fresh(t)
	rig.server.storePageChanged(rig.ids[rig.quiet])
	if rec, queries = rig.get(t, "/v1/stores/"+rig.quiet); rec.Header().Get("X-Cache") == "snapshot" || queries == 0 {
		t.Fatal("the changed page was answered from before the change")
	}
	if rec, queries = rig.get(t, "/v1/stores/"+rig.shops[5]); rec.Header().Get("X-Cache") != "snapshot" || queries != 0 {
		t.Fatal("another shop's page was sent to the database by one shop's change")
	}
}

// A copy that could not be read again by its maximum age is not answered from: the database
// answers instead, as it did before the copy existed, and nothing older than the maximum age
// reaches a reader. (A copy within its maximum age that fails to refresh goes on answering;
// that is the policy's own test in internal/store, where the clock can be moved.)
func TestACopyThatCannotBeReadAgainIsNotServedPastItsMaximumAge(t *testing.T) {
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
	if _, e := rig.db.Exec(context.Background(), `UPDATE stores SET name='Rig Renamed Since' WHERE slug=$1`, rig.quiet); e != nil {
		t.Fatal(e)
	}
	for _, path := range rig.paths() {
		if rec, queries := rig.get(t, path); rec.Header().Get("X-Cache") == "snapshot" || queries == 0 {
			t.Fatalf("%s was answered from a copy past its maximum age", path)
		}
	}
	if rec, _ := rig.get(t, "/v1/stores/"+rig.quiet); !strings.Contains(rec.Body.String(), "Rig Renamed Since") {
		t.Fatal("the database's answer was not the current one")
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

// The home page's highlights are read once and held; a review written here drops them.
func TestHighlightsAreHeldUntilAReviewChanges(t *testing.T) {
	rig := newCatalogueRig(t)
	if _, queries := rig.get(t, "/v1/search/highlights"); queries == 0 {
		t.Fatal("the first request did not read the highlights")
	}
	for _, locale := range i18n.Supported() {
		if rec, queries := rig.get(t, "/v1/search/highlights", "X-Locale", string(locale)); queries != 0 || rec.Header().Get("X-Cache") != "hit" {
			t.Fatalf("highlights in %s read the database again (%d)", locale, queries)
		}
	}
	rig.get(t, "/v1/search/popular-cities?limit=5")
	if _, queries := rig.get(t, "/v1/search/popular-cities"); queries != 0 {
		t.Fatal("the default limit is the same list and was read again")
	}
	rig.server.reviewsChanged()
	if rec, queries := rig.get(t, "/v1/search/highlights"); queries == 0 || rec.Header().Get("X-Cache") != "miss" {
		t.Fatal("a review written here did not drop the held highlights")
	}
}

// listed finds a shop by slug in a list answer, {"items": [...]} with or without a total.
func listed(t *testing.T, body []byte, slug string) map[string]any {
	t.Helper()
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if e := json.Unmarshal(body, &page); e != nil {
		t.Fatalf("%v: %s", e, body)
	}
	for _, item := range page.Items {
		if item["slug"] == slug {
			return item
		}
	}
	return nil
}

// Every route that writes something a catalogue page shows, through the router as a client
// calls it: the next anonymous read shows the write, and a write that changes a review drops
// the home page's highlights too. Each write starts from a copy read just before it, which the
// read before the write is checked to come from, so a handler that forgot to say what it
// changed -- or said "one shop's page" where a list moved -- fails here.
func TestEveryWriteRouteShowsOnTheNextAnonymousRead(t *testing.T) {
	rig := newCatalogueRig(t)
	ctx := context.Background()
	authorID, author := rig.signIn(t, "rigtest-author@example.test")
	adminID, adminToken := rig.signIn(t, rigAdmin)
	criteria := map[string]int{"availability": 4, "value": 4, "layout": 4, "staff_care": 4, "staff_knowledge": 4, "checkout": 4, "returns": 4, "cleanliness": 4}
	review := func(token, slug string) string {
		var created struct {
			ID string `json:"id"`
		}
		body := rig.send(t, "POST", "/v1/posts", token, map[string]any{"store_id": rig.ids[slug], "criteria": criteria}, 201)
		if e := json.Unmarshal(body, &created); e != nil {
			t.Fatal(e)
		}
		return created.ID
	}
	list := "/v1/discovery/stores?city=rigkent&category=furniture&limit=200"
	reviews := func(slug string, want float64) func([]byte) bool {
		return func(body []byte) bool {
			item := listed(t, body, slug)
			return item != nil && item["review_count"] == want
		}
	}
	page := func(fragment string) func([]byte) bool {
		return func(body []byte) bool { return bytes.Contains(body, []byte(fragment)) }
	}
	// The sitemap's date for a shop, as the database holds it before the write.
	dated := map[string]string{}
	remember := func(slug string) func() {
		return func() {
			var at time.Time
			if e := rig.db.QueryRow(ctx, `SELECT updated_at FROM stores WHERE slug=$1`, slug).Scan(&at); e != nil {
				t.Fatal(e)
			}
			encoded, _ := json.Marshal(at)
			dated[slug] = strings.Trim(string(encoded), `"`)
		}
	}
	redated := func(slug string) func([]byte) bool {
		return func(body []byte) bool {
			item := listed(t, body, slug)
			return item != nil && item["updated_at"] != dated[slug]
		}
	}
	type read struct {
		path  string
		shows func([]byte) bool
	}
	// Where a write is sent for the nth shop, by its id as the clients send it.
	shop := func(n int, rest string) string { return "/v1/stores/" + rig.ids[rig.shops[n]].String() + rest }
	admin := func(n int, rest string) string { return "/v1/admin/stores/" + rig.ids[rig.shops[n]].String() + rest }
	var post, held, leaving, removed string
	var removedID uuid.UUID
	cases := []struct {
		name    string
		before  func()
		write   func()
		reads   []read
		reviews bool
	}{{
		name:  "favourite",
		write: func() { rig.send(t, "POST", shop(5, "/favorite"), author, nil, 204) },
		reads: []read{{"/v1/stores/" + rig.shops[5], page(`"favorite_count":1`)}},
	}, {
		name:   "unfavourite",
		before: func() { rig.send(t, "POST", shop(6, "/favorite"), author, nil, 204) },
		write:  func() { rig.send(t, "DELETE", shop(6, "/favorite"), author, nil, 204) },
		reads:  []read{{"/v1/stores/" + rig.shops[6], page(`"favorite_count":0`)}},
	}, {
		name:    "first review",
		write:   func() { post = review(author, rig.shops[10]) },
		reads:   []read{{list, reviews(rig.shops[10], 1)}, {"/v1/stores/" + rig.shops[10], page(`"recent_posts":[{`)}},
		reviews: true,
	}, {
		name:    "review deleted by its author",
		write:   func() { rig.send(t, "DELETE", "/v1/posts/"+post, author, nil, 204) },
		reads:   []read{{list, reviews(rig.shops[10], 0)}},
		reviews: true,
	}, {
		name:   "premium",
		before: remember(rig.shops[7]),
		write:  func() { rig.send(t, "POST", admin(7, "/premium"), adminToken, map[string]any{"is_premium": true}, 200) },
		reads:  []read{{"/v1/stores/" + rig.shops[7], page(`"is_premium":true`)}, {"/v1/stores/index?limit=5000", redated(rig.shops[7])}},
	}, {
		name:   "catalogue flag",
		before: remember(rig.shops[8]),
		write: func() {
			rig.send(t, "POST", admin(8, "/catalog"), adminToken, map[string]any{"is_catalog_store": true}, 200)
		},
		reads: []read{{"/v1/stores/" + rig.shops[8], page(`"is_catalog_store":true`)}, {"/v1/stores/index?limit=5000", redated(rig.shops[8])}},
	}, {
		name: "categories",
		write: func() {
			rig.send(t, "POST", admin(9, "/categories"), adminToken, map[string]any{"slugs": []string{"furniture", "lighting"}}, 200)
		},
		reads: []read{{"/v1/discovery/city-categories?minimum=1", page(`"category_slug":"lighting"`)}, {"/v1/stores/" + rig.shops[9], page(`"lighting"`)}},
	}, {
		name: "cover cleared",
		before: func() {
			media := uuid.New()
			if _, e := rig.db.Exec(ctx, `INSERT INTO media(id,owner_user_id,storage_key,mime_type,status) VALUES($1,$2,$3,'image/jpeg','ready')`, media, adminID, "rigtest-"+media.String()); e != nil {
				t.Fatal(e)
			}
			if _, e := rig.db.Exec(ctx, `UPDATE stores SET cover_media_id=$1 WHERE slug=$2`, media, rig.shops[11]); e != nil {
				t.Fatal(e)
			}
		},
		write: func() { rig.send(t, "DELETE", admin(11, "/cover"), adminToken, nil, 204) },
		reads: []read{{list, func(body []byte) bool {
			item := listed(t, body, rig.shops[11])
			return item != nil && item["photo"] == nil
		}}},
	}, {
		name: "shop added",
		write: func() {
			rig.send(t, "POST", "/v1/admin/stores", adminToken, map[string]any{"name": "Rigtest Yeni Mağaza", "city": "Rigkent", "district": "Merkez",
				"latitude": 40.05, "longitude": 29.05, "categories": []string{"furniture"}}, 201)
		},
		reads: []read{{list, page(`"Rigtest Yeni Mağaza"`)}},
	}, {
		name: "shops merged",
		write: func() {
			rig.send(t, "POST", admin(13, "/merge"), adminToken, map[string]any{"merge": rig.ids[rig.shops[12]].String()}, 200)
		},
		reads:   []read{{list, func(body []byte) bool { return listed(t, body, rig.shops[12]) == nil }}},
		reviews: true,
	}, {
		name: "review removed by an administrator",
		write: func() {
			var id uuid.UUID
			if e := rig.db.QueryRow(ctx, `SELECT id FROM posts WHERE store_id=$1`, rig.ids[rig.review]).Scan(&id); e != nil {
				t.Fatal(e)
			}
			rig.send(t, "DELETE", "/v1/admin/reviews/"+id.String(), adminToken, nil, 204)
		},
		reads:   []read{{list, reviews(rig.review, 0)}},
		reviews: true,
	}, {
		name: "held review approved",
		before: func() {
			var id uuid.UUID
			if e := rig.db.QueryRow(ctx, `INSERT INTO posts(user_id,store_id,body,rating,verification_distance_meters,verified_at,moderation) VALUES($1,$2,'Güzel',4,10,now(),'held') RETURNING id`, authorID, rig.ids[rig.shops[3]]).Scan(&id); e != nil {
				t.Fatal(e)
			}
			held = id.String()
		},
		write: func() {
			rig.send(t, "POST", "/v1/admin/moderation/"+held, adminToken, map[string]any{"decision": "approved"}, 200)
		},
		reads:   []read{{list, reviews(rig.shops[3], 1)}},
		reviews: true,
	}, {
		name: "account deleted by its owner",
		before: func() {
			_, leaving = rig.signIn(t, "rigtest-leaving@example.test")
			review(leaving, rig.shops[4])
		},
		write:   func() { rig.send(t, "DELETE", "/v1/me", leaving, nil, 204) },
		reads:   []read{{list, reviews(rig.shops[4], 0)}},
		reviews: true,
	}, {
		name: "account deleted by an administrator",
		before: func() {
			removedID, removed = rig.signIn(t, "rigtest-removed@example.test")
			review(removed, rig.shops[0])
		},
		write:   func() { rig.send(t, "DELETE", "/v1/admin/users/"+removedID.String(), adminToken, nil, 204) },
		reads:   []read{{list, reviews(rig.shops[0], 0)}},
		reviews: true,
	}}
	for _, c := range cases {
		if c.before != nil {
			c.before()
		}
		rig.fresh(t)
		for _, r := range c.reads {
			rec, _ := rig.get(t, r.path)
			if r.shows(rec.Body.Bytes()) {
				t.Fatalf("%s: %s already shows the write before it was made", c.name, r.path)
			}
		}
		if c.reviews {
			rig.get(t, "/v1/search/highlights")
			if rec, _ := rig.get(t, "/v1/search/highlights"); rec.Header().Get("X-Cache") != "hit" {
				t.Fatalf("%s: the highlights were not held to begin with", c.name)
			}
		}
		c.write()
		for _, r := range c.reads {
			if rec, _ := rig.get(t, r.path); !r.shows(rec.Body.Bytes()) {
				t.Fatalf("%s: %s does not show the write on the next read (X-Cache %q): %s", c.name, r.path, rec.Header().Get("X-Cache"), rec.Body.String())
			}
		}
		if c.reviews {
			if rec, _ := rig.get(t, "/v1/search/highlights"); rec.Header().Get("X-Cache") != "miss" {
				t.Fatalf("%s: the home page's highlights outlived a change to the reviews", c.name)
			}
		}
	}
}

// A favourite changes one shop's page. That page shows it at once; every other answer stays
// in memory, and the catalogue is not read again for it.
func TestAFavouriteLeavesEveryOtherAnswerInMemory(t *testing.T) {
	rig := newCatalogueRig(t)
	_, token := rig.signIn(t, "rigtest-saver@example.test")
	rig.send(t, "POST", "/v1/stores/"+rig.ids[rig.quiet].String()+"/favorite", token, nil, 204)
	if rec, _ := rig.get(t, "/v1/stores/"+rig.quiet); rec.Header().Get("X-Cache") == "snapshot" || !strings.Contains(rec.Body.String(), `"favorite_count":1`) {
		t.Fatal("the favourite did not show on the shop's page")
	}
	for _, path := range rig.paths()[2:] {
		if rec, queries := rig.get(t, path); rec.Header().Get("X-Cache") != "snapshot" || queries != 0 {
			t.Fatalf("%s left memory for one shop's favourite (%d round trips)", path, queries)
		}
	}
}
