package store

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/google/uuid"
)

// fixture is a small catalogue built the way loadSnapshot builds one, without a database.
type fixture struct {
	rows snapshotRows
	cat  map[string]uuid.UUID
}

func newFixture() *fixture {
	f := &fixture{rows: snapshotRows{
		stats: map[uuid.UUID]Stats{}, brandOf: map[uuid.UUID]uuid.UUID{}, sources: map[uuid.UUID][]byte{},
		withPosts: map[uuid.UUID]bool{}, searchCounts: map[uuid.UUID]int64{},
	}, cat: map[string]uuid.UUID{}}
	for i, slug := range []string{"furniture", "lighting", "carpet", "retired"} {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", 900+i))
		f.cat[slug] = id
		f.rows.categories = append(f.rows.categories, snapCategory{id: id, slug: slug, nameTR: "TR " + slug, active: slug != "retired"})
		f.rows.categoryText = append(f.rows.categoryText, categoryText{id, i18n.LocaleEN, "EN " + slug})
	}
	f.rows.brands = []snapBrand{{id: uuid.MustParse("00000000-0000-4000-8000-000000000800"), slug: "yatas", name: "Yataş"}}
	return f
}

func storeID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
}

func (f *fixture) add(n int, city string, lat, lon float64, categories ...string) uuid.UUID {
	id := storeID(n)
	f.rows.stores = append(f.rows.stores, snapStore{id: id, slug: fmt.Sprintf("shop-%d", n), name: fmt.Sprintf("Shop %03d", n), city: city, lat: lat, lon: lon, live: true, updatedAt: time.Unix(int64(n), 0)})
	f.rows.stats[id] = Stats{}
	for _, c := range categories {
		f.rows.links = append(f.rows.links, [2]uuid.UUID{id, f.cat[c]})
	}
	return id
}

// build groups the links by store the way the loader's ORDER BY store_id,ctid does, keeping
// the order each shop's links were added in.
func (f *fixture) build() *Snapshot {
	sort.SliceStable(f.rows.links, func(a, b int) bool {
		return bytes.Compare(f.rows.links[a][0][:], f.rows.links[b][0][:]) < 0
	})
	return buildSnapshot(f.rows)
}

// A shop nobody has written about is answered from memory, in the reader's language.
func TestAQuietShopIsAnsweredFromTheCopy(t *testing.T) {
	f := newFixture()
	id := f.add(1, "İzmir", 38.4, 27.1, "lighting", "furniture")
	name := "Lamba Evi"
	f.rows.storeTexts = append(f.rows.storeTexts, storeTextRow{id, storeText{locale: i18n.LocaleEN, displayName: &name}})
	s := f.build()
	en, ok := s.Store(id.String(), i18n.LocaleEN)
	if !ok {
		t.Fatal("a quiet shop was left to the database")
	}
	if en.Name != "Lamba Evi" || en.Slug != "shop-1" {
		t.Fatalf("translated name not used: %+v", en)
	}
	// Categories in stored link order; labels in slug order and only those with a name in
	// the language -- the store page's own two rules.
	if fmt.Sprint(en.Categories) != "[lighting furniture]" || fmt.Sprint(en.CategoryLabels) != "[EN furniture EN lighting]" {
		t.Fatalf("categories %v labels %v", en.Categories, en.CategoryLabels)
	}
	tr, _ := s.Store("SHOP-1", i18n.LocaleTR)
	if tr.Name != "Shop 001" || len(tr.CategoryLabels) != 0 || tr.CategoryLabels == nil {
		t.Fatalf("a slug is case-insensitive and a missing translation falls back: %+v", tr)
	}
}

// The owner's exception stands: a shop with any review, or any post waiting for a moderator,
// is the database's to answer, and so is anything this copy has never heard of.
func TestReviewedAndUnknownShopsAreLeftToTheDatabase(t *testing.T) {
	f := newFixture()
	reviewed := f.add(1, "İzmir", 38.4, 27.1, "lighting")
	f.rows.stats[reviewed] = Stats{ReviewCount: 1, AverageRating: 4}
	held := f.add(2, "İzmir", 38.4, 27.1, "lighting")
	f.rows.withPosts[held] = true
	noFigures := f.add(3, "İzmir", 38.4, 27.1, "lighting")
	delete(f.rows.stats, noFigures)
	s := f.build()
	for _, ref := range []string{reviewed.String(), held.String(), noFigures.String(), storeID(99).String(), "never-imported", ""} {
		if _, ok := s.Store(ref, i18n.LocaleTR); ok {
			t.Fatalf("%q was answered from memory", ref)
		}
	}
}

// A merged shop's old id follows one hop to the shop it became; its old slug follows only to
// a shop that is still live -- FollowMerge and ResolveSlug, exactly.
func TestMergedShopsAnswerWithTheShopTheyBecame(t *testing.T) {
	f := newFixture()
	keep := f.add(1, "İzmir", 38.4, 27.1, "lighting")
	gone := f.add(2, "İzmir", 38.4, 27.1, "lighting")
	f.rows.stores[1].live, f.rows.stores[1].merged, f.rows.stores[1].mergedInto = false, true, keep
	s := f.build()
	for _, ref := range []string{gone.String(), "shop-2"} {
		x, ok := s.Store(ref, i18n.LocaleTR)
		if !ok || x.ID != keep {
			t.Fatalf("%s answered %v %v, want the survivor", ref, x.ID, ok)
		}
	}
}

// Neighbours: within ten kilometres on the ellipsoid, sharing a category, nearest first, the
// shop itself and shops with no figures left out, and the limit read the way the page reads it.
func TestNearbyMeasuresAndFiltersLikePostGIS(t *testing.T) {
	f := newFixture()
	subject := f.add(1, "Antalya", 36.9, 30.7, "lighting", "furniture")
	// One degree of latitude here is about 110.9 km, so 0.0890 degrees is about 9.87 km and
	// 0.0910 about 10.09 km.
	inside := f.add(2, "Antalya", 36.9+0.0890, 30.7, "furniture")
	f.add(3, "Antalya", 36.9+0.0910, 30.7, "furniture")
	f.add(4, "Antalya", 36.9001, 30.7, "carpet")
	closest := f.add(5, "Antalya", 36.9001, 30.7, "lighting")
	noFigures := f.add(6, "Antalya", 36.9002, 30.7, "lighting")
	delete(f.rows.stats, noFigures)
	s := f.build()
	got, ok := s.Nearby("shop-1", 0)
	if !ok {
		t.Fatal("a live shop's neighbours were left to the database")
	}
	if len(got) != 2 || got[0].ID != closest || got[1].ID != inside {
		t.Fatalf("neighbours %+v", got)
	}
	if got[1].DistanceMeters < 9800 || got[1].DistanceMeters > 9950 {
		t.Fatalf("distance %f is not the ellipsoid's", got[1].DistanceMeters)
	}
	if one, _ := s.Nearby(subject.String(), 1); len(one) != 1 {
		t.Fatalf("limit 1 gave %d", len(one))
	}
	if _, ok := s.Nearby(storeID(42).String(), 6); ok {
		t.Fatal("a shop this copy has never seen was answered")
	}
}

// Shops stood at the same point -- the centre of a district -- are exactly as far away as
// each other. The database leaves them in the order it read them; the copy uses id order,
// which is at least the same order every time.
func TestNearbyBreaksExactTiesById(t *testing.T) {
	f := newFixture()
	f.add(1, "Antalya", 36.9, 30.7, "lighting")
	for n := 9; n >= 2; n-- {
		f.add(n, "Antalya", 36.91, 30.71, "lighting")
	}
	got, _ := f.build().Nearby("shop-1", 24)
	for i := 1; i < len(got); i++ {
		if got[i].DistanceMeters != got[0].DistanceMeters || got[i].ID.String() < got[i-1].ID.String() {
			t.Fatalf("ties out of id order at %d: %+v", i, got)
		}
	}
}

// The city lists count every live shop, page through the ones with figures, and leave a
// city whose address two spellings share to the database rather than guess which it meant.
func TestCityCategoryPagesFollowTheDatabaseRules(t *testing.T) {
	f := newFixture()
	for n := 1; n <= 12; n++ {
		f.add(n, "İzmir", 38.4, 27.1, "furniture")
	}
	f.rows.stats[storeID(5)] = Stats{ReviewCount: 3, AverageRating: 4.5}
	f.rows.stats[storeID(7)] = Stats{ReviewCount: 3, AverageRating: 4.8}
	delete(f.rows.stats, storeID(12))
	f.add(20, "Ankara", 39.9, 32.8, "furniture", "retired")
	s := f.build()
	list := s.CityCategories(0, i18n.LocaleEN)
	if len(list) != 1 || list[0].City != "İzmir" || list[0].StoreCount != 12 || list[0].CitySlug != "izmir" || list[0].CategoryName != "EN furniture" {
		t.Fatalf("city categories %+v", list)
	}
	if all := s.CityCategories(1, i18n.LocaleEN); len(all) != 2 || all[1].City != "Ankara" {
		t.Fatalf("an inactive category was listed, or the order is wrong: %+v", all)
	}
	page, ok := s.ByCityCategory("izmir", "furniture", 3, 0, i18n.LocaleTR)
	if !ok || page.Total != 12 || len(page.Items) != 3 {
		t.Fatalf("page %+v %v", page, ok)
	}
	if page.Items[0].ID != storeID(7) || page.Items[1].ID != storeID(5) || page.Items[2].Name != "Shop 001" {
		t.Fatalf("reviewed shops first, by rating, then by name: %+v", page.Items)
	}
	if rest, _ := s.ByCityCategory("izmir", "furniture", 60, 3, i18n.LocaleTR); len(rest.Items) != 8 {
		t.Fatalf("the shop with no figures is counted but not listed, got %d", len(rest.Items))
	}
	for _, c := range [][2]string{{"izmir", "retired"}, {"izmir", "carpet"}, {"bursa", "furniture"}} {
		if _, ok := s.ByCityCategory(c[0], c[1], 60, 0, i18n.LocaleTR); ok {
			t.Fatalf("%v should be the database's to refuse", c)
		}
	}
	f.add(30, "Izmir", 38.4, 27.1, "furniture")
	if _, ok := f.build().ByCityCategory("izmir", "furniture", 60, 0, i18n.LocaleTR); ok {
		t.Fatal("two spellings of one address were resolved by guessing")
	}
}

func TestCityBrandPagesListBranchesByDistrict(t *testing.T) {
	f := newFixture()
	brand := f.rows.brands[0].id
	for n, district := range map[int]string{1: "Muratpaşa", 2: "Konyaaltı", 3: "Konyaaltı", 4: ""} {
		f.add(n, "Antalya", 36.9, 30.7, "furniture")
		f.rows.brandOf[storeID(n)] = brand
		for i := range f.rows.stores {
			if f.rows.stores[i].id == storeID(n) {
				f.rows.stores[i].district = district
			}
		}
	}
	s := f.build()
	if list := s.CityBrands(0); len(list) != 1 || list[0].StoreCount != 4 || list[0].BrandName != "Yataş" {
		t.Fatalf("city brands %+v", list)
	}
	page, ok := s.ByCityBrand("antalya", "yatas", 60, 0, i18n.LocaleTR)
	if !ok || page.Total != 4 {
		t.Fatalf("page %+v", page)
	}
	order := ""
	for _, x := range page.Items {
		order += x.District + "/" + x.Name + ";"
	}
	if order != "/Shop 004;Konyaaltı/Shop 002;Konyaaltı/Shop 003;Muratpaşa/Shop 001;" {
		t.Fatalf("order %s", order)
	}
	if _, ok := s.ByCityBrand("antalya", "unknown-brand", 60, 0, i18n.LocaleTR); ok {
		t.Fatal("a brand this copy has not seen was refused from memory")
	}
}

func TestIndexPagesByIdAndCategoriesBySearches(t *testing.T) {
	f := newFixture()
	for _, n := range []int{5, 3, 9, 1} {
		f.add(n, "Bursa", 40.2, 29.0, "carpet")
	}
	f.rows.searchCounts[f.cat["carpet"]] = 7
	f.rows.searchCounts[f.cat["lighting"]] = 7
	s := f.build()
	page := s.Index(1, 2)
	if len(page) != 2 || page[0].ID != storeID(3) || page[1].ID != storeID(5) {
		t.Fatalf("index page %+v", page)
	}
	if all := s.Index(-4, 0); len(all) != 4 {
		t.Fatalf("defaults not applied: %d", len(all))
	}
	categories := s.Categories(i18n.LocaleTR)
	got := ""
	for _, c := range categories {
		got += fmt.Sprintf("%s:%d;", c.Slug, c.SearchCount)
	}
	if got != "carpet:7;lighting:7;furniture:0;" {
		t.Fatalf("categories %s", got)
	}
}

// A distance PostGIS 3.6 reported for these two points: ST_Distance on geography, 4326.
func TestSpheroidDistanceAgreesWithPostGIS(t *testing.T) {
	got := spheroidDistance(36.9, 30.7, 36.989, 30.7)
	if want := 9876.917616; got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("got %.9f want %.9f", got, want)
	}
	if spheroidDistance(41, 29, 41, 29) != 0 {
		t.Fatal("a point is no distance from itself")
	}
}
