// Command merge-twins joins the rows that are one shop written down twice.
//
// The importer's job is to never create these, and for a shop whose two names resemble each
// other it does not. It failed on one shape: a chain's dealer. The chain writes
// "Bellona - İstanbul Eyüpsultan Balcı Mobilya", because the dealer's own trading name is
// most of what identifies the branch to the chain; the map writes "Bellona". Those two
// strings share so little of their length that trigram similarity reads 0.17 -- far under any
// threshold -- while the two rows stand one metre apart with the same sign over the door.
//
// The rule that finds them is not the name. It is the brand plus the distance: two rows of
// the same chain at the same address are one shop, because a chain does not open two of its
// own branches in one doorway. That identity only became available once the map's rows were
// linked to their chains.
//
// Merging is the admin service's own merge, not a second implementation: reviews, favourites,
// comments and ratings move to the surviving row, the other is soft-deleted with a redirect
// so its address does not break, and the whole thing is written to the audit log under the
// operator who ran it.
//
// There is a second shape, found while clearing the first. A shop can be listed by two
// chains at once: Taç publishes the Linens shops, and Linens publishes them too, so
// "İstanbul Kadıköy Tepe Nautilus Linens" and "Taç - İstanbul Kadıköy Tepe Nautilus Linens"
// stand at the same point under two different brands. One name holding the other whole, at
// the same address, is one shop -- whoever listed it. The survivor there is the row whose own
// chain both names agree on: both say Linens, only one says Taç, so the Linens row is the
// shop and the Taç row is Taç's mention of it.
//
// The survivor in the first shape is always the chain's own row -- it is the verified one, and it carries the
// chain's identifier, which is what the next import will match on.
//
//	go run ./cmd/merge-twins -actor info@bosagezme.com
//	go run ./cmd/merge-twins -actor info@bosagezme.com -apply
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/burakaltintas/home-app-api/internal/admin"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/textnorm"
)

// Two rows of one chain this close are the same shop. A chain's two genuine branches in one
// shopping centre are further apart than this, and a shop's published point and a mapper's
// point for the same door differ by a few metres at most.
const twinMeters = 120

// Two rows whose names hold one another this close are one shop whoever listed them. It is
// tighter than twinMeters because nothing but the address is doing the work here: there is no
// shared chain to corroborate it.
const sameShopMeters = 30

type twin struct {
	keepID, keepName   string
	dropID, dropName   string
	city               string
	metres             float64
	dropKind           string
	dropReviews        int
	dropFavourites     int
	dropExternalIDName string
}

func main() {
	apply := flag.Bool("apply", false, "merge; without it nothing is written")
	actorEmail := flag.String("actor", "", "the operator's email; the merge is recorded against it")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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

	var actor uuid.UUID
	if *apply {
		if *actorEmail == "" {
			log.Fatal("-actor is required to write: a merge is an operator's decision and is recorded as one")
		}
		if e := db.QueryRow(ctx, `SELECT id FROM users WHERE primary_email=$1 AND deleted_at IS NULL`, *actorEmail).Scan(&actor); e != nil {
			log.Fatalf("no such user: %s", *actorEmail)
		}
	}

	// Each pair once, and only where the survivor is the chain's own verified row. A pair of
	// two map rows, or two of the chain's own, is a different question and is left alone.
	rows, e := db.Query(ctx, `
SELECT k.id::text, k.name, d.id::text, d.name, coalesce(d.city,''),
       ST_Distance(k.location,d.location),
       d.source_kind,
       (SELECT count(*) FROM posts p WHERE p.store_id=d.id AND p.deleted_at IS NULL),
       (SELECT count(*) FROM favorites f WHERE f.store_id=d.id)
  FROM stores d
  JOIN stores k ON k.brand_id=d.brand_id AND k.id<>d.id
   AND ST_DWithin(k.location,d.location,$1)
 WHERE d.deleted_at IS NULL AND k.deleted_at IS NULL
   AND d.source_kind IN ('osm','legacy') AND k.source_kind='brand_locator'
   AND d.brand_id IS NOT NULL
 ORDER BY d.name`, twinMeters)
	if e != nil {
		log.Fatal(e)
	}
	defer rows.Close()

	var twins []twin
	seen := map[string]bool{}
	for rows.Next() {
		var t twin
		if e := rows.Scan(&t.keepID, &t.keepName, &t.dropID, &t.dropName, &t.city, &t.metres, &t.dropKind, &t.dropReviews, &t.dropFavourites); e != nil {
			log.Fatal(e)
		}
		// A map row near two branches of one chain would otherwise be merged twice; the
		// first pairing wins, and it is the nearest because the query is ordered by nothing
		// else that matters here.
		if seen[t.dropID] || seen[t.keepID] {
			continue
		}
		seen[t.dropID] = true
		twins = append(twins, t)
	}
	if e := rows.Err(); e != nil {
		log.Fatal(e)
	}

	twins = append(twins, listed(ctx, db)...)

	carried := 0
	for _, t := range twins {
		carried += t.dropReviews + t.dropFavourites
		fmt.Printf("  %3.0f m  %-34s → %s\n", t.metres, trim(t.dropName, 34), trim(t.keepName, 62))
	}
	fmt.Printf("toplam: %d çift, %d değerlendirme/favori taşınacak\n", len(twins), carried)
	if !*apply {
		fmt.Println("kuru çalıştırma; birleştirmek için -apply")
		return
	}

	service := admin.NewService(db)
	merged := 0
	for _, t := range twins {
		keep, e := uuid.Parse(t.keepID)
		if e != nil {
			log.Fatal(e)
		}
		drop, e := uuid.Parse(t.dropID)
		if e != nil {
			log.Fatal(e)
		}
		if e := service.MergeStores(ctx, actor, *actorEmail, keep, drop); e != nil {
			log.Printf("birleştirilemedi %s → %s: %v", t.dropName, t.keepName, e)
			continue
		}
		merged++
	}
	fmt.Printf("%d çift birleştirildi\n", merged)
}

// listed finds the second shape: two rows at one address whose names hold one another, under
// different chains or none. The survivor is decided in the open, in this order.
//
//  1. the chain both names name -- Taç's copy of a Linens shop loses to Linens' own row;
//  2. otherwise the verified row, which has a chain standing behind it;
//  3. otherwise the fuller record, counting the fields a reader actually uses;
//  4. otherwise the older row, so the choice is at least stable.
func listed(ctx context.Context, db *pgxpool.Pool) []twin {
	rows, e := db.Query(ctx, `
SELECT a.id::text, a.name, a.compact_name, coalesce(ba.name,''), a.data_verified_at IS NOT NULL,
       (a.address<>'')::int + (coalesce(a.phone,'')<>'')::int + (coalesce(a.website,'')<>'')::int,
       b.id::text, b.name, b.compact_name, coalesce(bb.name,''), b.data_verified_at IS NOT NULL,
       (b.address<>'')::int + (coalesce(b.phone,'')<>'')::int + (coalesce(b.website,'')<>'')::int,
       coalesce(a.city,''), ST_Distance(a.location,b.location), b.source_kind
  FROM stores a
  JOIN stores b ON a.id<b.id AND ST_DWithin(a.location,b.location,$1)
  LEFT JOIN brands ba ON ba.id=a.brand_id
  LEFT JOIN brands bb ON bb.id=b.brand_id
 WHERE a.deleted_at IS NULL AND b.deleted_at IS NULL
   AND length(a.compact_name)>=6 AND length(b.compact_name)>=6
   AND (a.compact_name LIKE '%'||b.compact_name||'%' OR b.compact_name LIKE '%'||a.compact_name||'%')
   AND (a.brand_id IS NULL OR b.brand_id IS NULL OR a.brand_id<>b.brand_id)
 ORDER BY a.created_at`, sameShopMeters)
	if e != nil {
		log.Fatal(e)
	}
	defer rows.Close()
	var out []twin
	seen := map[string]bool{}
	for rows.Next() {
		var aID, aName, aCompact, aBrand, bID, bName, bCompact, bBrand, city, kind string
		var aVerified, bVerified bool
		var aFields, bFields int
		var metres float64
		if e := rows.Scan(&aID, &aName, &aCompact, &aBrand, &aVerified, &aFields,
			&bID, &bName, &bCompact, &bBrand, &bVerified, &bFields, &city, &metres, &kind); e != nil {
			log.Fatal(e)
		}
		if seen[aID] || seen[bID] {
			continue
		}
		keepA := prefer(aCompact, aBrand, aVerified, aFields, bCompact, bBrand, bVerified, bFields)
		t := twin{city: city, metres: metres, dropKind: kind}
		// Dropping a verified row loses nothing: the merge moves every identifier to the
		// survivor and takes the stronger of the two provenances with it, so the survivor
		// ends up verified and the next import still recognises it by the chain's own id.
		// That is why the order above can put identity before provenance.
		if keepA {
			t.keepID, t.keepName, t.dropID, t.dropName = aID, aName, bID, bName
		} else {
			t.keepID, t.keepName, t.dropID, t.dropName = bID, bName, aID, aName
		}
		seen[t.dropID] = true
		out = append(out, t)
	}
	if e := rows.Err(); e != nil {
		log.Fatal(e)
	}
	return out
}

// prefer answers which of the two rows is the shop rather than somebody's mention of it.
func prefer(aCompact, aBrand string, aVerified bool, aFields int, bCompact, bBrand string, bVerified bool, bFields int) bool {
	// The chain both names carry is the shop's own chain.
	aAgreed := aBrand != "" && strings.Contains(bCompact, textnorm.Compact(aBrand))
	bAgreed := bBrand != "" && strings.Contains(aCompact, textnorm.Compact(bBrand))
	if aAgreed != bAgreed {
		return aAgreed
	}
	if aVerified != bVerified {
		return aVerified
	}
	if aFields != bFields {
		return aFields > bFields
	}
	return true // the older row, which the query ordered first
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
