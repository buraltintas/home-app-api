package store

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Snapshot is the live catalogue as it stood at one moment, held in this process so that
// reading it does not wake the database.
//
// Every crawler fetch of a store page asked Postgres for the shop, its neighbours and the
// city lists around it, and those fetches arrive every few seconds day and night: over 79
// measured hours the database never had the five quiet minutes it needs to suspend. The
// per-answer cache in internal/readcache could not fix that -- each crawler asks for each
// shop about once per language, so 96% of store pages were still a miss. The catalogue
// itself is small: fifteen and a half thousand shops fit in a few tens of megabytes, so the
// whole of it is read in one go and every anonymous catalogue answer is computed from that.
//
// A Snapshot never changes once built. A newer one replaces it whole (see Catalog), so a
// reader holding one sees one consistent catalogue however long it keeps it.
//
// It answers only what it is sure of, and says so with its second return value. Anything it
// does not know -- a shop added since it was read, a city it has never seen, a slug two
// spellings fold to -- is left to the database, which answers as it always has. It never
// answers "not found": a shop imported a minute ago is not missing, it is newer than this.
type Snapshot struct {
	loadedAt time.Time
	// The catalogue's change counter when this copy began reading; see Catalog.
	epoch uint64
	// Shops changed through this process since; see Catalog.InvalidateStore.
	dirty *sync.Map

	stores         []snapStore
	byID           map[uuid.UUID]int32
	bySlug         map[string]int32
	categories     []snapCategory
	categoryBySlug map[string]int32
	brands         []snapBrand
	brandBySlug    map[string]int32
	// A city's address back to its name. Empty when two spellings fold to the same address,
	// because which of them the database would pick is up to the database.
	cityBySlug map[string]string
	citySlugs  map[string]string
	grid       map[int64][]int32
	// Live shops with figures, in the order the sitemap pages through them.
	index []int32
	// Every (city, category) and (city, brand) pair with its count, in the order the
	// discovery lists are published.
	cityCategories []placeCount
	cityBrands     []placeCount
	// The shops behind each pair, already in the order the page lists them.
	categoryShops map[placeKey][]int32
	categoryTotal map[placeKey]int
	brandShops    map[placeKey][]int32
	brandTotal    map[placeKey]int
	bytes         int
}

type snapStore struct {
	id                                                             uuid.UUID
	slug, name, brandName, address, city, district, phone, website string
	description                                                    string
	cover                                                          string
	lat, lon                                                       float64
	premium, catalogStore, approximate                             bool
	live, hasStats, hasPosts                                       bool
	merged                                                         bool
	mergedInto                                                     uuid.UUID
	brand                                                          int32
	updatedAt                                                      time.Time
	stats                                                          Stats
	// In the order the store page reads them: the link table's stored order.
	categories []int32
	texts      []storeText
	// The store page's external_sources exactly as the database builds them, decoded only
	// when a page is asked for.
	sources []byte
}

type storeText struct {
	locale      i18n.Locale
	displayName *string
	description *string
}

type snapCategory struct {
	id          uuid.UUID
	slug        string
	nameTR      string
	urlSlug     string
	active      bool
	names       map[i18n.Locale]string
	searchCount int64
}

type snapBrand struct {
	id         uuid.UUID
	slug, name string
}

type placeKey struct {
	city string
	what int32
}

type placeCount struct {
	placeKey
	count int
}

// Stores reports how many shops this copy holds and roughly how many bytes they take.
func (s *Snapshot) Stores() (count, bytes int) {
	live := 0
	for i := range s.stores {
		if s.stores[i].live {
			live++
		}
	}
	return live, s.bytes
}

// LoadedAt is when this copy began reading the database.
func (s *Snapshot) LoadedAt() time.Time { return s.loadedAt }

// snapshotRows is what the database hands over, before any of it is indexed. Kept apart from
// the reading so the indexing can be tested without a database.
type snapshotRows struct {
	stores       []snapStore
	stats        map[uuid.UUID]Stats
	links        [][2]uuid.UUID
	categories   []snapCategory
	categoryText []categoryText
	storeTexts   []storeTextRow
	brands       []snapBrand
	brandOf      map[uuid.UUID]uuid.UUID
	sources      map[uuid.UUID][]byte
	withPosts    map[uuid.UUID]bool
	searchCounts map[uuid.UUID]int64
}

type categoryText struct {
	category uuid.UUID
	locale   i18n.Locale
	name     string
}

type storeTextRow struct {
	store uuid.UUID
	storeText
}

// loadSnapshot reads the catalogue in one repeatable-read transaction, so every table is
// seen as of the same instant: a review landing halfway through cannot leave a shop counted
// in one list and missing from another.
func loadSnapshot(ctx context.Context, db *pgxpool.Pool) (*Snapshot, error) {
	tx, e := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	rows := snapshotRows{
		stats:        map[uuid.UUID]Stats{},
		brandOf:      map[uuid.UUID]uuid.UUID{},
		sources:      map[uuid.UUID][]byte{},
		withPosts:    map[uuid.UUID]bool{},
		searchCounts: map[uuid.UUID]int64{},
	}
	// Live shops, and the rows merged into another one: their ids and slugs are in people's
	// links and still answer, with the shop they became.
	if e = scanAll(ctx, tx, `SELECT s.id,s.slug,s.name,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),
 coalesce(s.phone,''),coalesce(s.website,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),coalesce(s.description,''),
 s.is_premium,s.is_catalog_store,s.location_from LIKE 'placed at%',s.brand_id,coalesce(s.cover_media_id::text,''),s.updated_at,
 s.deleted_at IS NULL,s.merged_into
 FROM stores s WHERE s.deleted_at IS NULL OR s.merged_into IS NOT NULL`, func(r pgx.Rows) error {
		var x snapStore
		var brand, merged *uuid.UUID
		if e := r.Scan(&x.id, &x.slug, &x.name, &x.brandName, &x.address, &x.city, &x.district, &x.phone, &x.website, &x.lat, &x.lon, &x.description,
			&x.premium, &x.catalogStore, &x.approximate, &brand, &x.cover, &x.updatedAt, &x.live, &merged); e != nil {
			return e
		}
		if brand != nil {
			rows.brandOf[x.id] = *brand
		}
		if merged != nil {
			x.merged, x.mergedInto = true, *merged
		}
		rows.stores = append(rows.stores, x)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = scanAll(ctx, tx, `SELECT store_id,average_rating,rating_count,review_count,favorite_count,post_count FROM store_stats`, func(r pgx.Rows) error {
		var id uuid.UUID
		var x Stats
		if e := r.Scan(&id, &x.AverageRating, &x.RatingCount, &x.ReviewCount, &x.FavoriteCount, &x.PostCount); e != nil {
			return e
		}
		rows.stats[id] = x
		return nil
	}); e != nil {
		return nil, e
	}
	// The store page's query aggregates a shop's category slugs with no ORDER BY, so they come
	// back in whatever order its plan reads the links: with the statistics the catalogue has,
	// a bitmap scan of the link table, which is the order the rows are stored in. ctid is that
	// order. The web treats the list as a set; this keeps the bytes the same as well.
	if e = scanAll(ctx, tx, `SELECT store_id,category_id FROM store_category_links ORDER BY store_id,ctid`, func(r pgx.Rows) error {
		var link [2]uuid.UUID
		if e := r.Scan(&link[0], &link[1]); e != nil {
			return e
		}
		rows.links = append(rows.links, link)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = scanAll(ctx, tx, `SELECT id,slug,name_tr,active FROM store_categories`, func(r pgx.Rows) error {
		var x snapCategory
		if e := r.Scan(&x.id, &x.slug, &x.nameTR, &x.active); e != nil {
			return e
		}
		rows.categories = append(rows.categories, x)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = scanAll(ctx, tx, `SELECT category_id,locale::text,name FROM store_category_translations`, func(r pgx.Rows) error {
		var x categoryText
		var locale string
		if e := r.Scan(&x.category, &locale, &x.name); e != nil {
			return e
		}
		x.locale = i18n.Locale(locale)
		rows.categoryText = append(rows.categoryText, x)
		return nil
	}); e != nil {
		return nil, e
	}
	// The same figure /v1/categories reads, for the same thirty days.
	if e = scanAll(ctx, tx, `SELECT c.id,coalesce((SELECT sum(m.search_count) FROM search_intent_daily_metrics m
   WHERE m.dimension='category' AND m.value=c.slug AND m.metric_date >= (now()-interval '30 days')::date),0)
 FROM store_categories c WHERE c.active`, func(r pgx.Rows) error {
		var id uuid.UUID
		var count int64
		if e := r.Scan(&id, &count); e != nil {
			return e
		}
		rows.searchCounts[id] = count
		return nil
	}); e != nil {
		return nil, e
	}
	if e = scanAll(ctx, tx, `SELECT store_id,locale::text,display_name,description FROM store_translations`, func(r pgx.Rows) error {
		var x storeTextRow
		var locale string
		if e := r.Scan(&x.store, &locale, &x.displayName, &x.description); e != nil {
			return e
		}
		x.locale = i18n.Locale(locale)
		rows.storeTexts = append(rows.storeTexts, x)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = scanAll(ctx, tx, `SELECT id,slug,name FROM brands`, func(r pgx.Rows) error {
		var x snapBrand
		if e := r.Scan(&x.id, &x.slug, &x.name); e != nil {
			return e
		}
		rows.brands = append(rows.brands, x)
		return nil
	}); e != nil {
		return nil, e
	}
	// Built by the same expression the store page uses, so decoding it gives the page the
	// same values it would have read itself.
	if e = scanAll(ctx, tx, `SELECT x.store_id,jsonb_agg(jsonb_build_object('provider',x.provider,'external_id',x.external_id,'attribution',x.attribution,'refreshed_at',x.refreshed_at) ORDER BY x.provider)
 FROM store_external_sources x JOIN stores s ON s.id=x.store_id AND s.deleted_at IS NULL GROUP BY x.store_id`, func(r pgx.Rows) error {
		var id uuid.UUID
		var raw []byte
		if e := r.Scan(&id, &raw); e != nil {
			return e
		}
		rows.sources[id] = raw
		return nil
	}); e != nil {
		return nil, e
	}
	// Any post at all, whatever its state. A shop with one held for a moderator may have it
	// published by another instance at any moment, and its page is then the owner's
	// exception: left to the database.
	if e = scanAll(ctx, tx, `SELECT DISTINCT store_id FROM posts WHERE deleted_at IS NULL`, func(r pgx.Rows) error {
		var id uuid.UUID
		if e := r.Scan(&id); e != nil {
			return e
		}
		rows.withPosts[id] = true
		return nil
	}); e != nil {
		return nil, e
	}
	return buildSnapshot(rows), nil
}

func scanAll(ctx context.Context, tx pgx.Tx, sql string, each func(pgx.Rows) error) error {
	rows, e := tx.Query(ctx, sql)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		if e = each(rows); e != nil {
			return e
		}
	}
	return rows.Err()
}

// A grid cell is a tenth of a degree: about eleven kilometres north to south, and eight to
// nine east to west at Turkey's latitudes, so a ten-kilometre neighbourhood spans a handful.
const gridDegrees = 0.1

func gridKey(latCell, lonCell int64) int64 { return latCell<<32 | (lonCell & 0xffffffff) }

func gridCell(degrees float64) int64 { return int64(math.Floor(degrees / gridDegrees)) }

func buildSnapshot(rows snapshotRows) *Snapshot {
	s := &Snapshot{
		stores:         rows.stores,
		byID:           make(map[uuid.UUID]int32, len(rows.stores)),
		bySlug:         make(map[string]int32, len(rows.stores)),
		categories:     rows.categories,
		categoryBySlug: make(map[string]int32, len(rows.categories)),
		brands:         rows.brands,
		brandBySlug:    make(map[string]int32, len(rows.brands)),
		cityBySlug:     map[string]string{},
		citySlugs:      map[string]string{},
		grid:           map[int64][]int32{},
		categoryShops:  map[placeKey][]int32{},
		categoryTotal:  map[placeKey]int{},
		brandShops:     map[placeKey][]int32{},
		brandTotal:     map[placeKey]int{},
	}
	categoryByID := make(map[uuid.UUID]int32, len(s.categories))
	for i := range s.categories {
		c := &s.categories[i]
		categoryByID[c.id] = int32(i)
		s.categoryBySlug[c.slug] = int32(i)
		c.urlSlug = textnorm.Slug(c.nameTR)
		c.names = map[i18n.Locale]string{}
		c.searchCount = rows.searchCounts[c.id]
		s.bytes += len(c.slug) + len(c.nameTR) + len(c.urlSlug) + 96
	}
	for _, t := range rows.categoryText {
		if i, ok := categoryByID[t.category]; ok {
			s.categories[i].names[t.locale] = t.name
			s.bytes += len(t.name) + 32
		}
	}
	brandByID := make(map[uuid.UUID]int32, len(s.brands))
	for i, b := range s.brands {
		brandByID[b.id] = int32(i)
		s.brandBySlug[b.slug] = int32(i)
		s.bytes += len(b.slug) + len(b.name) + 64
	}
	for i := range s.stores {
		x := &s.stores[i]
		s.byID[x.id] = int32(i)
		s.bySlug[x.slug] = int32(i)
		x.brand = -1
		if b, ok := rows.brandOf[x.id]; ok {
			if j, ok := brandByID[b]; ok {
				x.brand = j
			}
		}
		x.stats, x.hasStats = rows.stats[x.id]
		x.hasPosts = rows.withPosts[x.id]
		x.sources = rows.sources[x.id]
		s.bytes += 360 + len(x.slug) + len(x.name) + len(x.brandName) + len(x.address) + len(x.city) + len(x.district) +
			len(x.phone) + len(x.website) + len(x.description) + len(x.cover) + len(x.sources) + 2*(len(x.slug)+48)
	}
	// Links arrive grouped by store in stored order, so appending keeps each shop's list in
	// the order its page reads them.
	for _, link := range rows.links {
		i, ok := s.byID[link[0]]
		c, known := categoryByID[link[1]]
		if ok && known {
			s.stores[i].categories = append(s.stores[i].categories, c)
			s.bytes += 4
		}
	}
	for _, t := range rows.storeTexts {
		if i, ok := s.byID[t.store]; ok {
			s.stores[i].texts = append(s.stores[i].texts, t.storeText)
			s.bytes += 64
			if t.displayName != nil {
				s.bytes += len(*t.displayName)
			}
			if t.description != nil {
				s.bytes += len(*t.description)
			}
		}
	}

	categoryCounts := map[placeKey]int{}
	brandCounts := map[placeKey]int{}
	ambiguous := map[string]bool{}
	for i := range s.stores {
		x := &s.stores[i]
		if !x.live {
			continue
		}
		if x.city != "" {
			slug, ok := s.citySlugs[x.city]
			if !ok {
				slug = textnorm.Slug(x.city)
				s.citySlugs[x.city] = slug
			}
			if seen, ok := s.cityBySlug[slug]; ok && seen != x.city {
				ambiguous[slug] = true
			}
			s.cityBySlug[slug] = x.city
		}
		for _, c := range x.categories {
			key := placeKey{x.city, c}
			s.categoryTotal[key]++
			if s.categories[c].active && x.city != "" {
				categoryCounts[key]++
			}
			if x.hasStats {
				s.categoryShops[key] = append(s.categoryShops[key], int32(i))
			}
		}
		if x.brand >= 0 {
			key := placeKey{x.city, x.brand}
			s.brandTotal[key]++
			if x.city != "" {
				brandCounts[key]++
			}
			if x.hasStats {
				s.brandShops[key] = append(s.brandShops[key], int32(i))
			}
		}
		if x.hasStats {
			s.index = append(s.index, int32(i))
			key := gridKey(gridCell(x.lat), gridCell(x.lon))
			s.grid[key] = append(s.grid[key], int32(i))
		}
	}
	for slug := range ambiguous {
		s.cityBySlug[slug] = ""
	}
	sort.Slice(s.index, func(a, b int) bool {
		return bytes.Compare(s.stores[s.index[a]].id[:], s.stores[s.index[b]].id[:]) < 0
	})
	// ORDER BY ss.review_count DESC, ss.average_rating DESC, s.name -- and then by id, where
	// the database leaves equal names in whatever order it read them.
	for _, list := range s.categoryShops {
		sort.Slice(list, func(a, b int) bool {
			x, y := &s.stores[list[a]], &s.stores[list[b]]
			if x.stats.ReviewCount != y.stats.ReviewCount {
				return x.stats.ReviewCount > y.stats.ReviewCount
			}
			if x.stats.AverageRating != y.stats.AverageRating {
				return x.stats.AverageRating > y.stats.AverageRating
			}
			if x.name != y.name {
				return x.name < y.name
			}
			return bytes.Compare(x.id[:], y.id[:]) < 0
		})
	}
	// ORDER BY coalesce(s.district,''), s.name, then id for the same reason.
	for _, list := range s.brandShops {
		sort.Slice(list, func(a, b int) bool {
			x, y := &s.stores[list[a]], &s.stores[list[b]]
			if x.district != y.district {
				return x.district < y.district
			}
			if x.name != y.name {
				return x.name < y.name
			}
			return bytes.Compare(x.id[:], y.id[:]) < 0
		})
	}
	for key, count := range categoryCounts {
		s.cityCategories = append(s.cityCategories, placeCount{key, count})
	}
	sort.Slice(s.cityCategories, func(a, b int) bool {
		x, y := s.cityCategories[a], s.cityCategories[b]
		if x.count != y.count {
			return x.count > y.count
		}
		if x.city != y.city {
			return x.city < y.city
		}
		return s.categories[x.what].slug < s.categories[y.what].slug
	})
	for key, count := range brandCounts {
		s.cityBrands = append(s.cityBrands, placeCount{key, count})
	}
	sort.Slice(s.cityBrands, func(a, b int) bool {
		x, y := s.cityBrands[a], s.cityBrands[b]
		if x.count != y.count {
			return x.count > y.count
		}
		if x.city != y.city {
			return x.city < y.city
		}
		return s.brands[x.what].slug < s.brands[y.what].slug
	})
	s.bytes += 16*len(s.index) + 8*len(s.stores) + 48*(len(s.categoryTotal)+len(s.brandTotal))
	return s
}

// resolve turns a reference from a URL into the shop it names, by the rules the database
// path follows: a uuid follows one merge (FollowMerge), a slug is lower-cased and follows a
// merge only to a shop that is still live (ResolveSlug). False when the answer is not
// certain from here.
func (s *Snapshot) resolve(ref string) (*snapStore, bool) {
	if id, e := uuid.Parse(ref); e == nil {
		i, ok := s.byID[id]
		if !ok {
			return nil, false
		}
		x := &s.stores[i]
		if x.merged {
			if i, ok = s.byID[x.mergedInto]; !ok {
				return nil, false
			}
			x = &s.stores[i]
		}
		return x, x.live
	}
	slug := strings.ToLower(strings.TrimSpace(ref))
	if slug == "" || len(slug) > 200 {
		return nil, false
	}
	i, ok := s.bySlug[slug]
	if !ok {
		return nil, false
	}
	x := &s.stores[i]
	if x.merged {
		if j, ok := s.byID[x.mergedInto]; ok && s.stores[j].live {
			x = &s.stores[j]
		}
	}
	return x, x.live
}

func (s *Snapshot) changedSince(id uuid.UUID) bool {
	if s.dirty == nil {
		return false
	}
	marked, ok := s.dirty.Load(id)
	return ok && marked.(uint64) > s.epoch
}

func (x *snapStore) displayName(locale i18n.Locale) string {
	for _, t := range x.texts {
		if t.locale == locale && t.displayName != nil {
			return *t.displayName
		}
	}
	return x.name
}

func (x *snapStore) localizedDescription(locale i18n.Locale) string {
	for _, t := range x.texts {
		if t.locale == locale && t.description != nil {
			return *t.description
		}
	}
	return x.description
}

func (s *Snapshot) categoryName(c int32, locale i18n.Locale) string {
	if name, ok := s.categories[c].names[locale]; ok {
		return name
	}
	return s.categories[c].nameTR
}

// labels are the shop's category names in the reader's language, ordered by category slug,
// leaving out a category with no name in that language -- the store page's own rule.
func (s *Snapshot) labels(x *snapStore, locale i18n.Locale) []string {
	type label struct{ slug, name string }
	found := make([]label, 0, len(x.categories))
	for _, c := range x.categories {
		if name, ok := s.categories[c].names[locale]; ok {
			found = append(found, label{s.categories[c].slug, name})
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].slug < found[b].slug })
	out := make([]string, len(found))
	for i, l := range found {
		out[i] = l.name
	}
	return out
}

func (s *Snapshot) brandSlug(x *snapStore) string {
	if x.brand < 0 {
		return ""
	}
	return s.brands[x.brand].slug
}

func (s *Snapshot) photo(x *snapStore) *Photo {
	item := Item{BrandSlug: s.brandSlug(x)}
	assignPhoto(&item, x.cover)
	return item.Photo
}

// Store is a shop's page as somebody who is not signed in reads it, or false when that page
// belongs to the database: the shop is unknown here, has any post at all, or was changed by
// this process after this copy was read.
//
// A shop with posts is the owner's standing exception (see storeDetail): everything on its
// page that a reader would notice going stale is a review, and a write can only reach the
// copy in the process it arrived at.
func (s *Snapshot) Store(ref string, locale i18n.Locale) (Item, bool) {
	x, ok := s.resolve(ref)
	if !ok || !x.hasStats || x.hasPosts || x.stats.ReviewCount > 0 || x.stats.PostCount > 0 || s.changedSince(x.id) {
		return Item{}, false
	}
	item := Item{
		ID: x.id, Name: x.displayName(locale), Slug: x.slug, BrandName: x.brandName, Address: x.address,
		City: x.city, District: x.district, Phone: x.phone, Website: x.website,
		Latitude: x.lat, Longitude: x.lon,
		Categories:           make([]string, len(x.categories)),
		CategoryLabels:       s.labels(x, locale),
		LocalizedDescription: x.localizedDescription(locale),
		Platform:             x.stats,
		BrandSlug:            s.brandSlug(x),
		IsPremium:            x.premium,
		IsCatalogStore:       x.catalogStore,
		LocationApproximate:  x.approximate,
	}
	for i, c := range x.categories {
		item.Categories[i] = s.categories[c].slug
	}
	if len(x.sources) > 0 {
		if e := json.Unmarshal(x.sources, &item.ExternalSources); e != nil {
			return Item{}, false
		}
	}
	assignPhoto(&item, x.cover)
	return item, true
}

// Nearby is Service.Nearby answered from here: shops within ten kilometres sharing a
// category, nearest first, measured on the ellipsoid as PostGIS measures. Shops at exactly the
// same distance -- the ones stood at the centre of the same district -- come in id order,
// where the database left them in whatever order it read them.
func (s *Snapshot) Nearby(ref string, limit int) ([]NearbyEntry, bool) {
	subject, ok := s.resolve(ref)
	if !ok {
		return nil, false
	}
	limit = nearbyLimit(limit)
	type candidate struct {
		store    int32
		distance float64
	}
	var found []candidate
	// Ten kilometres in degrees, with room to spare: a degree of latitude is never less than
	// 110.5 km, and a degree of longitude shrinks with the cosine of the latitude.
	latSpan := nearbyRadiusMeters/110500.0 + 0.001
	lonSpan := latSpan / math.Max(math.Cos((math.Abs(subject.lat)+latSpan)*math.Pi/180), 0.01)
	for latCell := gridCell(subject.lat - latSpan); latCell <= gridCell(subject.lat+latSpan); latCell++ {
		for lonCell := gridCell(subject.lon - lonSpan); lonCell <= gridCell(subject.lon+lonSpan); lonCell++ {
			for _, i := range s.grid[gridKey(latCell, lonCell)] {
				other := &s.stores[i]
				if other.id == subject.id || !sharesCategory(subject.categories, other.categories) {
					continue
				}
				if math.Abs(other.lat-subject.lat) > latSpan || math.Abs(other.lon-subject.lon) > lonSpan {
					continue
				}
				d := spheroidDistance(subject.lat, subject.lon, other.lat, other.lon)
				if d <= nearbyRadiusMeters {
					found = append(found, candidate{i, d})
				}
			}
		}
	}
	sort.Slice(found, func(a, b int) bool {
		if found[a].distance != found[b].distance {
			return found[a].distance < found[b].distance
		}
		return bytes.Compare(s.stores[found[a].store].id[:], s.stores[found[b].store].id[:]) < 0
	})
	if len(found) > limit {
		found = found[:limit]
	}
	out := make([]NearbyEntry, 0, limit)
	for _, c := range found {
		x := &s.stores[c.store]
		out = append(out, NearbyEntry{
			ID: x.id, Slug: x.slug, Name: x.name, District: x.district, City: x.city,
			DistanceMeters: c.distance, AverageRating: x.stats.AverageRating, ReviewCount: x.stats.ReviewCount,
			BrandSlug: s.brandSlug(x), Photo: s.photo(x),
		})
	}
	return out, true
}

func sharesCategory(a, b []int32) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// CityCategories is Service.CityCategories answered from here.
func (s *Snapshot) CityCategories(minimum int, locale i18n.Locale) []CityCategory {
	if minimum < 1 {
		minimum = cityCategoryMinimum
	}
	out := []CityCategory{}
	for _, p := range s.cityCategories {
		if p.count < minimum {
			break
		}
		c := &s.categories[p.what]
		out = append(out, CityCategory{
			City: p.city, CitySlug: s.citySlugs[p.city], CategorySlug: c.slug,
			CategoryName: s.categoryName(p.what, locale), CategoryURLSlug: c.urlSlug, StoreCount: p.count,
		})
	}
	return out
}

func (s *Snapshot) city(slug string) (string, bool) {
	city, ok := s.cityBySlug[slug]
	return city, ok && city != ""
}

func (s *Snapshot) entry(i int32, locale i18n.Locale) CatalogEntry {
	x := &s.stores[i]
	return CatalogEntry{
		ID: x.id, Slug: x.slug, Name: x.displayName(locale), Address: x.address, District: x.district, City: x.city,
		AverageRating: x.stats.AverageRating, ReviewCount: x.stats.ReviewCount, BrandName: x.brandName,
		BrandSlug: s.brandSlug(x), CategoryLabels: s.labels(x, locale), Photo: s.photo(x),
	}
}

func (s *Snapshot) page(list []int32, limit, offset int, locale i18n.Locale) []CatalogEntry {
	items := []CatalogEntry{}
	for i := offset; i < len(list) && i < offset+limit; i++ {
		items = append(items, s.entry(list[i], locale))
	}
	return items
}

// ByCityCategory is Service.ByCityCategory answered from here. A city, category or pair it
// does not know is not a 404 from here; the database says so, or knows better.
func (s *Snapshot) ByCityCategory(citySlug, categorySlug string, limit, offset int, locale i18n.Locale) (CityCategoryPage, bool) {
	if limit < 1 || limit > 200 {
		limit = 60
	}
	if offset < 0 {
		offset = 0
	}
	city, ok := s.city(citySlug)
	if !ok {
		return CityCategoryPage{}, false
	}
	c, ok := s.categoryBySlug[categorySlug]
	if !ok || !s.categories[c].active {
		return CityCategoryPage{}, false
	}
	key := placeKey{city, c}
	total := s.categoryTotal[key]
	if total == 0 {
		return CityCategoryPage{}, false
	}
	return CityCategoryPage{
		City: city, CategorySlug: categorySlug, CategoryName: s.categoryName(c, locale), Total: total,
		Items: s.page(s.categoryShops[key], limit, offset, locale),
	}, true
}

// CityBrands is Service.CityBrands answered from here.
func (s *Snapshot) CityBrands(minimum int) []CityBrand {
	if minimum < 1 {
		minimum = cityBrandMinimum
	}
	out := []CityBrand{}
	for _, p := range s.cityBrands {
		if p.count < minimum {
			break
		}
		b := &s.brands[p.what]
		out = append(out, CityBrand{City: p.city, CitySlug: s.citySlugs[p.city], BrandSlug: b.slug, BrandName: b.name, StoreCount: p.count})
	}
	return out
}

// ByCityBrand is Service.ByCityBrand answered from here.
func (s *Snapshot) ByCityBrand(citySlug, brandSlug string, limit, offset int, locale i18n.Locale) (CityBrandPage, bool) {
	if limit < 1 || limit > 200 {
		limit = 60
	}
	if offset < 0 {
		offset = 0
	}
	city, ok := s.city(citySlug)
	if !ok {
		return CityBrandPage{}, false
	}
	b, ok := s.brandBySlug[brandSlug]
	if !ok {
		return CityBrandPage{}, false
	}
	key := placeKey{city, b}
	total := s.brandTotal[key]
	if total == 0 {
		return CityBrandPage{}, false
	}
	return CityBrandPage{
		City: city, BrandSlug: brandSlug, BrandName: s.brands[b].name, Total: total,
		Items: s.page(s.brandShops[key], limit, offset, locale),
	}, true
}

// Index is Service.Index answered from here.
func (s *Snapshot) Index(offset, limit int) []IndexEntry {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	out := make([]IndexEntry, 0, limit)
	for i := offset; i < len(s.index) && i < offset+limit; i++ {
		x := &s.stores[s.index[i]]
		out = append(out, IndexEntry{ID: x.id, Slug: x.slug, Name: x.name, City: x.city, UpdatedAt: x.updatedAt, ReviewCount: x.stats.ReviewCount})
	}
	return out
}

// Categories is Service.Categories answered from here: the search counts are as of when
// this copy was read, which is at most the snapshot's age behind a figure that only moves
// once a day anyway.
func (s *Snapshot) Categories(locale i18n.Locale) []Category {
	out := []Category{}
	for i := range s.categories {
		c := &s.categories[i]
		if c.active {
			out = append(out, Category{Slug: c.slug, Name: s.categoryName(int32(i), locale), SearchCount: c.searchCount})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].SearchCount != out[b].SearchCount {
			return out[a].SearchCount > out[b].SearchCount
		}
		if out[a].Name != out[b].Name {
			return out[a].Name < out[b].Name
		}
		return out[a].Slug < out[b].Slug
	})
	return out
}
