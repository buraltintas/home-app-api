//go:build integration

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// localDatabase opens TEST_DATABASE_URL, and only when it is on this machine. These tests
// write a catalogue of their own and delete it again; aimed at a shared database they would
// do both to somebody else's data.
func localDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL/PostGIS integration suite")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		t.Fatalf("refusing to seed a test catalogue into %q: point TEST_DATABASE_URL at a throwaway local database", parsed.Hostname())
	}
	pool, err := pgxpool.New(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

const seedPrefix = "snaptest-"

type seeded struct {
	stores []uuid.UUID
	slugs  []string
	pairs  [][2]string // city slug, category slug
	brands [][2]string // city slug, brand slug
}

// seedCatalogue writes n shops shaped like the real catalogue: clustered in a few cities,
// one to three categories each, some branded, translated, sourced, merged, retired, reviewed
// or held for a moderator, a few without figures and a few stood at a district's centre.
func seedCatalogue(t *testing.T, db *pgxpool.Pool, n int, seed int64) seeded {
	t.Helper()
	ctx := context.Background()
	clean := func() {
		for _, sql := range []string{
			`DELETE FROM posts WHERE store_id IN (SELECT id FROM stores WHERE slug LIKE 'snaptest-%')`,
			`UPDATE stores SET merged_into=NULL, cover_media_id=NULL WHERE slug LIKE 'snaptest-%'`,
			`DELETE FROM stores WHERE slug LIKE 'snaptest-%'`,
			`DELETE FROM brands WHERE slug LIKE 'snaptest-%'`,
			`DELETE FROM media WHERE storage_key LIKE 'snaptest-%'`,
			`DELETE FROM user_profiles WHERE display_name='Snap Tester'`,
			`DELETE FROM users WHERE primary_email::text LIKE 'snaptest-%'`,
			`DELETE FROM store_category_translations WHERE category_id IN (SELECT id FROM store_categories WHERE slug LIKE 'snaptest-%')`,
			`DELETE FROM store_categories WHERE slug LIKE 'snaptest-%'`,
			`DELETE FROM search_intent_daily_metrics WHERE value LIKE 'snaptest-%' OR updated_at='2001-01-01'`,
		} {
			if _, e := db.Exec(ctx, sql); e != nil {
				t.Fatal(sql, e)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, sql, args...); e != nil {
			t.Fatal(sql, e)
		}
	}
	rng := rand.New(rand.NewSource(seed))
	exec(`INSERT INTO store_categories(slug,name_tr,active) VALUES('snaptest-inactive','Eski Kategori',false)`)
	exec(`INSERT INTO store_category_translations(category_id,locale,name) SELECT id,'en','Old category' FROM store_categories WHERE slug='snaptest-inactive'`)
	var categories []string
	rows, e := db.Query(ctx, `SELECT slug FROM store_categories ORDER BY slug`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var slug string
		_ = rows.Scan(&slug)
		categories = append(categories, slug)
	}
	rows.Close()
	// A day the database already counted -- by its own rollup, or a seed -- keeps its count:
	// the comparison reads both answers from the same rows, so whose rows they are is moot.
	for _, c := range categories {
		if rng.Intn(3) > 0 {
			exec(`INSERT INTO search_intent_daily_metrics(metric_date,dimension,value,search_count,updated_at) VALUES(current_date - $1::int,'category',$2,$3,'2001-01-01') ON CONFLICT DO NOTHING`, rng.Intn(40), c, rng.Intn(50))
		}
	}
	user := uuid.New()
	exec(`INSERT INTO users(id,primary_email) VALUES($1,$2)`, user, seedPrefix+user.String()+"@example.test")
	exec(`INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2::text::citext,'Snap Tester')`, user, "snap_"+user.String()[:8])
	brands := []string{"snaptest-yatas", "snaptest-englishhome", "snaptest-karaca"}
	brandIDs := map[string]uuid.UUID{}
	for i, slug := range brands {
		id := uuid.New()
		brandIDs[slug] = id
		exec(`INSERT INTO brands(id,slug,name) VALUES($1,$2,$3)`, id, slug, []string{"Yataş", "English Home", "Karaca & Co <Ev>"}[i])
	}
	cities := []struct {
		name     string
		lat, lon float64
		weight   int
	}{{"Snapİzmir", 38.42, 27.14, 5}, {"Snapantalya", 36.89, 30.70, 4}, {"Snapkent", 40.19, 29.06, 1}, {"SNAPKENT", 40.21, 29.02, 1}, {"Snapşanlıurfa", 37.16, 38.79, 1}, {"", 39.9, 32.8, 1}}
	var weighted []int
	for i, c := range cities {
		for j := 0; j < c.weight; j++ {
			weighted = append(weighted, i)
		}
	}
	var out seeded
	pairs := map[[2]string]bool{}
	brandPairs := map[[2]string]bool{}
	// Ids, slugs and names chosen so the database has no ties to break by whim: the ordering
	// rules that do tie are exercised by the unit tests.
	for i := 0; i < n; i++ {
		id := uuid.New()
		city := cities[weighted[rng.Intn(len(weighted))]]
		lat := city.lat + rng.NormFloat64()*0.05
		lon := city.lon + rng.NormFloat64()*0.05
		from := ""
		if i%37 == 0 {
			// Stood at the centre of the district: exactly where four others stand too.
			lat, lon, from = city.lat, city.lon, "placed at district centre"
		}
		slug := fmt.Sprintf("%s%05d", seedPrefix, i)
		district := []string{"", "Konak", "Bornova", "Muratpaşa", "Konyaaltı", "Çankaya"}[rng.Intn(6)]
		var brand any
		brandName := ""
		if rng.Intn(4) == 0 {
			b := brands[rng.Intn(len(brands))]
			brand, brandName = brandIDs[b], strings.TrimPrefix(b, seedPrefix)
		}
		var description any
		if rng.Intn(3) == 0 {
			description = strings.Repeat("Ev ve yaşam ürünleri. ", 1+rng.Intn(6))
		}
		var cover any
		if i%53 == 0 {
			// A cover belongs to one shop.
			picture := uuid.New()
			exec(`INSERT INTO media(id,owner_user_id,storage_key,mime_type,status) VALUES($1,$2,$3,'image/webp','ready')`, picture, user, seedPrefix+picture.String())
			cover = picture
		}
		exec(`INSERT INTO stores(id,name,slug,brand_name,description,address,city,district,location,website,phone,is_premium,is_catalog_store,brand_id,location_from,cover_media_id,updated_at)
 VALUES($1,$2,$3,nullif($4,''),$5,$6,$7,nullif($8,''),ST_SetSRID(ST_MakePoint($10,$9),4326)::geography,$11,$12,$13,$14,$15,$16,$17,now()-($18::int*interval '1 minute'))`,
			id, fmt.Sprintf("Snap Mağaza %05d", i), slug, brandName, description, fmt.Sprintf("%d. Sokak No:%d", rng.Intn(900), i), city.name, district,
			lat, lon, "https://example.test/"+slug, fmt.Sprintf("+90 5%09d", rng.Intn(1e9)), i%41 == 0, i%43 == 0, brand, from, cover, rng.Intn(100000))
		if i%61 != 0 {
			rating := float64(rng.Intn(500)) / 100
			reviews := 0
			if rng.Intn(9) == 0 {
				reviews = 1 + rng.Intn(20)
			}
			exec(`INSERT INTO store_stats(store_id,average_rating,rating_count,review_count,favorite_count,post_count) VALUES($1,$2,$3,$3,$4,$3)`, id, rating, reviews, rng.Intn(30))
		}
		chosen := map[string]bool{}
		for k := 0; k < 1+rng.Intn(3); k++ {
			c := categories[rng.Intn(len(categories))]
			if chosen[c] {
				continue
			}
			chosen[c] = true
			exec(`INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=$2`, id, c)
			pairs[[2]string{city.name, c}] = true
		}
		if brand != nil {
			for b, bid := range brandIDs {
				if bid == brand {
					brandPairs[[2]string{city.name, b}] = true
				}
			}
		}
		if i%7 == 0 {
			exec(`INSERT INTO store_translations(store_id,locale,display_name,description) VALUES($1,'en',$2,NULL)`, id, fmt.Sprintf("Snap Store %05d", i))
		}
		if i%11 == 0 {
			exec(`INSERT INTO store_translations(store_id,locale,display_name,description) VALUES($1,'de',NULL,$2)`, id, "Wohnen & Einrichten <b>")
		}
		if i%5 == 0 {
			exec(`INSERT INTO store_external_sources(store_id,provider,external_id,attribution,refreshed_at) VALUES($1,'google',$2,$3,now()-interval '3 days 4 hours 0.123456 seconds')`,
				id, "g-"+slug, fmt.Sprintf(`{"types":["store","furniture_store"],"rating":%d.%d,"html":"<b>Ünal & Co</b>","nested":{"z":1,"a":[true,null,"x"]}}`, rng.Intn(5), rng.Intn(10)))
		}
		if i%15 == 0 {
			exec(`INSERT INTO store_external_sources(store_id,provider,external_id,attribution) VALUES($1,'osm',$2,'{}')`, id, "o-"+slug)
		}
		if i%67 == 0 && i > 0 {
			exec(`INSERT INTO posts(user_id,store_id,body,rating,verification_distance_meters,verified_at,moderation) VALUES($1,$2,'Güzel','4',12,now(),$3)`, user, id, []string{"published", "held"}[i%2])
		}
		out.stores = append(out.stores, id)
		out.slugs = append(out.slugs, slug)
	}
	// One shop merged into another, one retired outright.
	exec(`UPDATE stores SET deleted_at=now(), merged_into=$2 WHERE id=$1`, out.stores[3], out.stores[4])
	exec(`UPDATE stores SET deleted_at=now() WHERE id=$1`, out.stores[6])
	// With statistics, as the real catalogue has them, so the store page's plan is the real one.
	exec(`ANALYZE stores, store_stats, store_category_links, store_categories, store_translations`)
	for p := range pairs {
		out.pairs = append(out.pairs, p)
	}
	for p := range brandPairs {
		out.brands = append(out.brands, p)
	}
	return out
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func citySlug(name string) string {
	// The same fold cityForSlug compares against.
	s := buildSnapshot(snapshotRows{stores: []snapStore{{city: name, live: true}}})
	return s.citySlugs[name]
}

// Every answer the copy gives is the answer the database gives, byte for byte, for every
// shop, in every language. Distances are the one exception, and only below a micrometre.
func TestTheCopyAnswersExactlyAsTheDatabaseDoes(t *testing.T) {
	db := localDatabase(t)
	seed := seedCatalogue(t, db, 1500, 7)
	svc := NewService(db, nil)
	snap, err := loadSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	answered, refused, reordered := 0, 0, 0
	for _, locale := range i18n.Supported() {
		ctx := i18n.WithLocale(context.Background(), locale)
		for i, id := range seed.stores {
			for _, ref := range []string{id.String(), seed.slugs[i]} {
				fromCopy, ok := snap.Store(ref, locale)
				resolved, e := resolveLikeTheServer(ctx, svc, ref)
				if !ok {
					refused++
					continue
				}
				answered++
				if e != nil {
					t.Fatalf("%s: the copy answered a shop the database cannot resolve: %v", ref, e)
				}
				fromDB, e := svc.Get(ctx, resolved, nil, nil, nil)
				if e != nil {
					t.Fatalf("%s: the copy answered where the database says %v", ref, e)
				}
				if a, b := asJSON(t, fromCopy), asJSON(t, fromDB); a != b {
					// Aggregated with no ORDER BY, so the database's order is its plan's; the
					// set has to agree and, with the statistics in place, so does the order.
					sort.Strings(fromCopy.Categories)
					sort.Strings(fromDB.Categories)
					if asJSON(t, fromCopy) != asJSON(t, fromDB) {
						t.Fatalf("%s %s differs:\n copy %s\n db   %s", ref, locale, a, b)
					}
					reordered++
				}
			}
		}
		for _, minimum := range []int{0, 1, 3} {
			if a, b := asJSON(t, snap.CityCategories(minimum, locale)), asJSON(t, must(svc.CityCategories(ctx, minimum))); a != b {
				t.Fatalf("city categories %d %s differ:\n copy %s\n db   %s", minimum, locale, a, b)
			}
		}
		for _, p := range seed.pairs {
			for _, window := range [][2]int{{60, 0}, {5, 3}, {500, 0}, {60, 1000}} {
				page, ok := snap.ByCityCategory(citySlug(p[0]), p[1], window[0], window[1], locale)
				fromDB, e := svc.ByCityCategory(ctx, citySlug(p[0]), p[1], window[0], window[1])
				if !ok {
					continue
				}
				if e != nil {
					t.Fatalf("%v: the copy answered where the database says %v", p, e)
				}
				if a, b := asJSON(t, page), asJSON(t, fromDB); a != b {
					t.Fatalf("%v %v %s differs:\n copy %s\n db   %s", p, window, locale, a, b)
				}
			}
		}
		for _, p := range seed.brands {
			page, ok := snap.ByCityBrand(citySlug(p[0]), p[1], 60, 0, locale)
			fromDB, e := svc.ByCityBrand(ctx, citySlug(p[0]), p[1], 60, 0)
			if !ok {
				continue
			}
			if e != nil {
				t.Fatalf("%v: the copy answered where the database says %v", p, e)
			}
			if a, b := asJSON(t, page), asJSON(t, fromDB); a != b {
				t.Fatalf("%v %s differs:\n copy %s\n db   %s", p, locale, a, b)
			}
		}
		if a, b := asJSON(t, snap.Categories(locale)), asJSON(t, must(svc.Categories(ctx))); a != b {
			t.Fatalf("categories %s differ:\n copy %s\n db   %s", locale, a, b)
		}
	}
	// Every live shop with figures and nothing written about it, by id and by slug, in four
	// languages; everything else left to the database.
	if answered < 4*2*1200 || refused == 0 {
		t.Fatalf("answered %d, refused %d", answered, refused)
	}
	t.Logf("answered %d, left to the database %d, category slugs in another order %d", answered, refused, reordered)
	if _, ok := snap.ByCityCategory("snapkent", "furniture", 60, 0, i18n.LocaleTR); ok {
		t.Fatal("two spellings of one city were resolved from memory")
	}
	for _, minimum := range []int{0, 1} {
		if a, b := asJSON(t, snap.CityBrands(minimum)), asJSON(t, must(svc.CityBrands(context.Background(), minimum))); a != b {
			t.Fatalf("city brands differ:\n copy %s\n db   %s", a, b)
		}
	}
	for _, window := range [][2]int{{0, 5000}, {10, 7}, {1490, 100}} {
		if a, b := asJSON(t, snap.Index(window[0], window[1])), asJSON(t, must(svc.Index(context.Background(), window[0], window[1]))); a != b {
			t.Fatalf("index %v differs:\n copy %s\n db   %s", window, a, b)
		}
	}
	for i, id := range seed.stores {
		for _, limit := range []int{6, 24} {
			ref := []string{id.String(), seed.slugs[i]}[i%2]
			fromCopy, ok := snap.Nearby(ref, limit)
			if !ok {
				continue
			}
			resolved, _ := resolveLikeTheServer(context.Background(), svc, ref)
			fromDB, e := svc.Nearby(context.Background(), resolved, limit)
			if e != nil {
				t.Fatal(e)
			}
			sameNeighbours(t, ref, fromCopy, fromDB)
		}
	}
}

func resolveLikeTheServer(ctx context.Context, svc *Service, ref string) (uuid.UUID, error) {
	if id, e := uuid.Parse(ref); e == nil {
		return svc.FollowMerge(ctx, id), nil
	}
	return svc.ResolveSlug(ctx, ref)
}

func must[T any](v T, e error) T {
	if e != nil {
		panic(e)
	}
	return v
}

// sameNeighbours compares two neighbour lists the way a reader would: the same distances in
// the same order, and the same shop wherever only one shop is at that distance. Shops at
// exactly the same distance -- stood at the same district centre -- are a tie the database
// breaks by whim, so there either may come first, and the limit may cut through them.
func sameNeighbours(t *testing.T, ref string, fromCopy, fromDB []NearbyEntry) {
	t.Helper()
	if len(fromCopy) != len(fromDB) {
		t.Fatalf("%s: %d neighbours from the copy, %d from the database", ref, len(fromCopy), len(fromDB))
	}
	for i := range fromCopy {
		a, b := fromCopy[i], fromDB[i]
		if math.Abs(a.DistanceMeters-b.DistanceMeters) > 1e-6 {
			t.Fatalf("%s[%d]: distance %.9f against %.9f\n copy %s\n db   %s", ref, i, a.DistanceMeters, b.DistanceMeters, asJSON(t, fromCopy), asJSON(t, fromDB))
		}
		if a.ID != b.ID {
			continue
		}
		a.DistanceMeters, b.DistanceMeters = 0, 0
		if x, y := asJSON(t, a), asJSON(t, b); x != y {
			t.Fatalf("%s[%d] differs:\n copy %s\n db   %s", ref, i, x, y)
		}
	}
}

// What the copy costs, measured on a catalogue the size of the real one: fifteen and a half
// thousand shops, a third with a description, a fifth with a provider record. Opt in with
// CATALOG_SNAPSHOT_SIZE=1; it takes a minute to seed.
func TestTheCopyOfAFullCatalogueFitsTheContainer(t *testing.T) {
	if os.Getenv("CATALOG_SNAPSHOT_SIZE") == "" {
		t.Skip("set CATALOG_SNAPSHOT_SIZE=1 to measure a full-size copy")
	}
	db := localDatabase(t)
	seedCatalogue(t, db, 15600, 11)
	// The provider records are the bulk of it: about ten megabytes across the real catalogue,
	// so every shop gets one of roughly that size here.
	for _, sql := range []string{
		`INSERT INTO store_external_sources(store_id,provider,external_id,attribution) SELECT id,'google','g2-'||slug,'{}' FROM stores s
		  WHERE slug LIKE 'snaptest-%' AND NOT EXISTS (SELECT 1 FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google')`,
		`UPDATE store_external_sources SET attribution = attribution || jsonb_build_object(
		  'types', jsonb_build_array('furniture_store','home_goods_store','store','point_of_interest','establishment'),
		  'formatted_address', repeat('Atatürk Caddesi No:12 ', 4), 'weekday_text', repeat('Pazartesi: 09:00–21:00 ', 7))
		  WHERE store_id IN (SELECT id FROM stores WHERE slug LIKE 'snaptest-%')`,
	} {
		if _, e := db.Exec(context.Background(), sql); e != nil {
			t.Fatal(e)
		}
	}
	var sources int64
	if e := db.QueryRow(context.Background(), `SELECT sum(octet_length(attribution::text)) FROM store_external_sources`).Scan(&sources); e != nil {
		t.Fatal(e)
	}
	t.Logf("provider records: %.1f MB", float64(sources)/(1<<20))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	snap, err := loadSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	runtime.GC()
	runtime.ReadMemStats(&after)
	stores, estimate := snap.Stores()
	heap := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("%d live shops: heap %.1f MB, estimate %.1f MB, read in %s", stores, float64(heap)/(1<<20), float64(estimate)/(1<<20), took.Round(time.Millisecond))
	var sample bytes.Buffer
	for i := 0; i < 1000; i++ {
		x, _ := snap.Nearby(snap.stores[i].id.String(), 6)
		sample.WriteString(asJSON(t, x))
	}
	started = time.Now()
	for i := 0; i < 2000; i++ {
		snap.Nearby(snap.stores[i%len(snap.stores)].id.String(), 6)
	}
	t.Logf("nearby from memory: %s per answer", (time.Since(started) / 2000).Round(time.Microsecond))
	if heap > 120<<20 {
		t.Fatalf("a full copy takes %d MB of a 512 MB container", heap>>20)
	}
	runtime.KeepAlive(snap)
}
