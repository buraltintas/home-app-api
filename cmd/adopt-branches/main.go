// Command adopt-branches gives a chain's branch back to its chain.
//
// A shop that came from the public map or from the old provider carries no brand, because
// neither source says which chain a shop belongs to -- a mapper writes "Bellona" and stops
// there. So a row that is plainly a branch of a chain we already hold sits in the catalogue
// as though it were somebody's independent furniture shop: no mark, no brand page, and no
// way for search to know that a query for the chain should find it.
//
// There are two rules, and the shop's own website is the stronger one.
//
//	the shop's website is the chain's website,
//	or the compact name starts with the chain's compact name
//	   and the shop's categories overlap the chain's own trade.
//
// A host is an identity in a way a name is not: nobody puts a competitor's address on their
// own shop. So a website match needs no guard, while a name match does.
//
// The guard is not decoration. "Korkmaz" is a pot maker and also one of the commonest
// surnames in the country: "Korkmaz Mobilya" is a furniture shop belonging to a family, not
// a branch of a kitchenware chain, and only the trade tells the two apart. A chain whose
// categories the shop does not share is refused, however well the name reads.
//
// What this writes is one column: brand_id. It does not rename the shop, does not mark it
// verified, and does not touch its address or its point -- a mapper's row stays a mapper's
// row. What changes is that it now shows its chain's mark and answers to its chain's name.
//
// Two rows the same chain owns at the same address are a duplicate, not an adoption. This
// does not merge anything; it prints what it adopted so a person can look for pairs.
//
//	go run ./cmd/adopt-branches            # dry run
//	go run ./cmd/adopt-branches -apply
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/textnorm"
)

// A chain name shorter than this matches too much to be trusted on a prefix alone: a
// three-letter name is a syllable, and it is the start of a hundred ordinary words.
const shortestBrandName = 4

type brand struct {
	id         string
	name       string
	compact    string
	host       string
	categories map[string]bool
}

type candidate struct {
	storeID, name, city, district string
	brand                         brand
	why                           string
}

// host reduces an address to the thing that identifies whose site it is. A shop writing
// "https://www.yatsan.com/magazalar" and a chain writing "https://yatsan.com" are the same
// business; the path and the "www." are not part of who it is.
func host(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	raw = strings.TrimPrefix(raw, "www.")
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func main() {
	apply := flag.Bool("apply", false, "write the brand; without it nothing is written")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()

	brands, e := readBrands(ctx, db)
	if e != nil {
		log.Fatal(e)
	}

	// Only shops with no chain of their own, and only the two provenances that cannot name
	// one: a row the chain's own list produced already knows what it is.
	rows, e := db.Query(ctx, `
SELECT s.id::text, s.name, s.compact_name, coalesce(s.city,''), coalesce(s.district,''),
       coalesce(s.website,''), coalesce(array_agg(c.slug) FILTER (WHERE c.slug IS NOT NULL),'{}')
  FROM stores s
  LEFT JOIN store_category_links l ON l.store_id=s.id
  LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.deleted_at IS NULL AND s.brand_id IS NULL
   AND s.source_kind IN ('osm','legacy')
 GROUP BY s.id
 ORDER BY s.name`)
	if e != nil {
		log.Fatal(e)
	}
	defer rows.Close()

	var found []candidate
	refusedByTrade := 0
	for rows.Next() {
		var id, name, compact, city, district, website string
		var categories []string
		if e := rows.Scan(&id, &name, &compact, &city, &district, &website, &categories); e != nil {
			log.Fatal(e)
		}
		if match := byHost(website, brands); match != nil {
			found = append(found, candidate{storeID: id, name: name, city: city, district: district, brand: *match, why: "sitesi: " + host(website)})
			continue
		}
		match, shared, refused := pick(compact, categories, brands)
		if refused {
			refusedByTrade++
		}
		if match == nil {
			continue
		}
		found = append(found, candidate{storeID: id, name: name, city: city, district: district, brand: *match, why: "adı + " + strings.Join(shared, ", ")})
	}
	if e := rows.Err(); e != nil {
		log.Fatal(e)
	}

	perBrand := map[string]int{}
	for _, c := range found {
		perBrand[c.brand.name]++
	}
	names := make([]string, 0, len(perBrand))
	for n := range perBrand {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return perBrand[names[i]] > perBrand[names[j]] })
	for _, n := range names {
		fmt.Printf("  %-22s %4d\n", n, perBrand[n])
	}

	for _, c := range found {
		where := strings.TrimSpace(strings.Join([]string{c.district, c.city}, " "))
		fmt.Printf("  %-42s %-16s %-20s %s\n", trim(c.name, 42), trim(where, 16), c.brand.name, c.why)
	}

	fmt.Printf("toplam: %d şube %d markaya bağlanacak; %d satır adı uydu ama ticareti uymadı\n",
		len(found), len(perBrand), refusedByTrade)
	if !*apply {
		fmt.Println("kuru çalıştırma; yazmak için -apply")
		return
	}
	written := 0
	for _, c := range found {
		// brand_name is the label a listing prints; it was left as the chain wrote it for
		// rows the chain produced, and is empty on these. Filling it keeps the two columns
		// telling the same story.
		tag, e := db.Exec(ctx, `UPDATE stores SET brand_id=$2,brand_name=coalesce(nullif(brand_name,''),$3),updated_at=now()
       WHERE id=$1 AND brand_id IS NULL AND deleted_at IS NULL`, c.storeID, c.brand.id, c.brand.name)
		if e != nil {
			log.Fatal(e)
		}
		written += int(tag.RowsAffected())
	}
	fmt.Printf("%d satır markasına bağlandı\n", written)
}

// byHost is the rule that needs no guard: the shop publishes the chain's own address.
func byHost(website string, brands []brand) *brand {
	h := host(website)
	if h == "" {
		return nil
	}
	for i := range brands {
		if brands[i].host != "" && brands[i].host == h {
			return &brands[i]
		}
	}
	return nil
}

// pick returns the chain a shop's name claims, if its trade agrees. The second return says
// which categories the two share, and the third that a name matched and the trade did not --
// the number worth printing, because it is the guard doing its work.
func pick(compact string, categories []string, brands []brand) (*brand, []string, bool) {
	held := map[string]bool{}
	for _, c := range categories {
		held[c] = true
	}
	var refused bool
	// Longest chain name first: "English Home" must win over a chain called "English".
	for i := range brands {
		b := brands[i]
		if !strings.HasPrefix(compact, b.compact) {
			continue
		}
		var shared []string
		for c := range held {
			if b.categories[c] {
				shared = append(shared, c)
			}
		}
		// A shop with no category at all tells us nothing about its trade, so the guard
		// cannot clear it and will not pretend to.
		if len(shared) == 0 {
			refused = true
			continue
		}
		sort.Strings(shared)
		return &b, shared, false
	}
	return nil, nil, refused
}

func readBrands(ctx context.Context, db *pgxpool.Pool) ([]brand, error) {
	rows, e := db.Query(ctx, `SELECT id::text,name,coalesce(website,''),category_profile FROM brands WHERE active`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []brand
	for rows.Next() {
		var b brand
		var website string
		var profile []string
		if e := rows.Scan(&b.id, &b.name, &website, &profile); e != nil {
			return nil, e
		}
		b.compact = textnorm.Compact(b.name)
		b.host = host(website)
		if len([]rune(b.compact)) < shortestBrandName || len(profile) == 0 {
			continue
		}
		b.categories = map[string]bool{}
		for _, c := range profile {
			b.categories[c] = true
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].compact) > len(out[j].compact) })
	return out, rows.Err()
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
