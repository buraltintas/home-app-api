package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	emailpkg "github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/google/uuid"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	for _, locale := range i18n.Supported() {
		message, renderErr := emailpkg.RenderLoginCode(locale, "123456", 10)
		if renderErr != nil || message.Subject == "" || message.HTML == "" || message.Text == "" {
			log.Fatalf("login_code template is incomplete for %s: %v", locale, renderErr)
		}
	}
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer tx.Rollback(ctx)
	mustExec := func(query string, args ...any) {
		if _, execErr := tx.Exec(ctx, query, args...); execErr != nil {
			log.Fatalf("seed statement failed (%s): %v", query, execErr)
		}
	}
	type store struct {
		name, slug, brand, address, city, district string
		lat, lon                                   float64
		category, google                           string
	}
	stores := []store{{"IKEA Bayrampaşa", "ikea-bayrampasa", "IKEA", "Kocatepe Mah. Paşa Cad. No:1", "İstanbul", "Bayrampaşa", 41.0451, 28.8972, "furniture", "ChIJ_seed_ikea_bayrampasa"}, {"Akasya Ev Tekstili", "akasya-ev-tekstili", "", "Kadıköy çarşı", "İstanbul", "Kadıköy", 40.9908, 29.0277, "home_textile", ""}, {"Çankaya Modern Mobilya", "cankaya-modern-mobilya", "", "Turan Güneş Bulvarı", "Ankara", "Çankaya", 39.8736, 32.8597, "furniture", ""}, {"Lara Perde & Ev", "lara-perde-ev", "", "Lara Caddesi", "Antalya", "Muratpaşa", 36.8534, 30.7774, "curtain", ""}, {"İzmir Işık Tasarım", "izmir-isik-tasarim", "", "Mithatpaşa Caddesi", "İzmir", "Konak", 38.4089, 27.1177, "lighting", ""}}
	ids := make([]uuid.UUID, len(stores))
	for i, x := range stores {
		ids[i] = seedID("store", i)
		_, e = tx.Exec(ctx, `INSERT INTO stores(id,name,slug,brand_name,address,city,district,location) VALUES($1,$2,$3,$4,$5,$6,$7,ST_SetSRID(ST_MakePoint($9,$8),4326)::geography) ON CONFLICT(slug) DO UPDATE SET name=excluded.name RETURNING id`, ids[i], x.name, x.slug, x.brand, x.address, x.city, x.district, x.lat, x.lon)
		if e != nil {
			log.Fatal(e)
		}
		_ = tx.QueryRow(ctx, `SELECT id FROM stores WHERE slug=$1`, x.slug).Scan(&ids[i])
		_, e = tx.Exec(ctx, `INSERT INTO store_stats(store_id) VALUES($1) ON CONFLICT DO NOTHING`, ids[i])
		if e != nil {
			log.Fatal(e)
		}
		_, e = tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=$2 ON CONFLICT DO NOTHING`, ids[i], x.category)
		if e != nil {
			log.Fatal(e)
		}
		if x.google != "" {
			_, e = tx.Exec(ctx, `INSERT INTO store_external_sources(store_id,provider,external_id,attribution) VALUES($1,'google',$2,'{"provider":"Google"}') ON CONFLICT(provider,external_id) DO NOTHING`, ids[i], x.google)
			if e != nil {
				log.Fatal(e)
			}
		}
	}
	users := []struct{ email, username, name, locale, bio string }{
		{"ayse@example.test", "ayseevde", "Ayşe Yılmaz", "tr", "Ev ve dekorasyon mağazalarını geziyorum."},
		{"emma@example.test", "emmahome", "Emma Smith", "en", "Discovering physical home and living stores."},
		{"lena@example.test", "lenawohnt", "Lena Wagner", "de", "Ich entdecke Geschäfte für Wohnen und Einrichtung."},
		{"anna@example.test", "annahome", "Анна Иванова", "ru", "Ищу интересные магазины товаров для дома."},
	}
	uids := make([]uuid.UUID, len(users))
	for i, u := range users {
		uids[i] = seedID("user", i)
		// Deterministic IDs allow seed evolution (including locale/email changes)
		// without creating duplicate fixture identities.
		_, e = tx.Exec(ctx, `INSERT INTO users(id,primary_email,preferred_locale) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET primary_email=excluded.primary_email,preferred_locale=excluded.preferred_locale,updated_at=now()`, uids[i], u.email, u.locale)
		if e != nil {
			log.Fatal(e)
		}
		mustExec(`INSERT INTO user_profiles(user_id,username,display_name,city,bio,bio_language) VALUES($1,$2,$3,'İstanbul',$4,$5) ON CONFLICT(user_id) DO UPDATE SET bio=excluded.bio,bio_language=excluded.bio_language`, uids[i], u.username, u.name, u.bio, u.locale)
		mustExec(`INSERT INTO auth_identities(user_id,provider,provider_subject,normalized_email,email_verified) VALUES($1,'email',$2::text,$2::text::citext,true) ON CONFLICT DO NOTHING`, uids[i], u.email)
	}
	posts := []struct {
		u, st          int
		text, language string
		rating         int
	}{{0, 0, "Geniş ürün seçeneği var; hafta içi gitmek çok daha rahat.", "tr", 5}, {1, 1, "The fabric quality is excellent and the staff were helpful.", "en", 4}, {2, 2, "Die Auswahl an modernen Möbeln ist einen Besuch wert.", "de", 4}, {3, 3, "Большой выбор штор, сотрудники очень помогли с размерами.", "ru", 5}}
	for i, p := range posts {
		id := seedID("post", i)
		_, e = tx.Exec(ctx, `INSERT INTO posts(id,user_id,store_id,body,content_language,rating,verification_distance_meters,verified_at) VALUES($1,$2,$3,$4,$5,$6,25,now()) ON CONFLICT(id) DO UPDATE SET body=excluded.body,content_language=excluded.content_language,rating=excluded.rating`, id, uids[p.u], ids[p.st], p.text, p.language, p.rating)
		if e != nil {
			log.Fatal(e)
		}
		mustExec(`INSERT INTO likes(user_id,post_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uids[(p.u+1)%len(uids)], id)
		mustExec(`INSERT INTO comments(id,post_id,user_id,body,content_language) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, seedID("comment", i), id, uids[(p.u+2)%len(uids)], []string{"Paylaşım için teşekkürler!", "Thanks for sharing!", "Danke fürs Teilen!", "Спасибо за отзыв!"}[i], []string{"tr", "en", "de", "ru"}[i])
		mediaID := seedID("media", i)
		storageKey := "seed/reviews/" + mediaID.String() + ".jpg"
		mustExec(`INSERT INTO media(id,owner_user_id,storage_key,mime_type,width,height,size_bytes,status) VALUES($1,$2,$3,'image/jpeg',1200,900,250000,'ready') ON CONFLICT(id) DO NOTHING`, mediaID, uids[p.u], storageKey)
		mustExec(`INSERT INTO post_media(post_id,media_id,position) VALUES($1,$2,0) ON CONFLICT DO NOTHING`, id, mediaID)
	}
	_, e = tx.Exec(ctx, `UPDATE store_stats ss SET rating_count=x.n,review_count=x.n,post_count=x.n,average_rating=x.avg FROM (SELECT store_id,count(*)::int n,avg(rating) avg FROM posts WHERE deleted_at IS NULL GROUP BY store_id)x WHERE ss.store_id=x.store_id`)
	if e != nil {
		log.Fatal(e)
	}
	for i := range users {
		mustExec(`INSERT INTO favorites(user_id,store_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uids[i], ids[i%len(ids)])
	}
	mustExec(`INSERT INTO follows(follower_id,following_id) VALUES($1,$2),($2,$3),($3,$1) ON CONFLICT DO NOTHING`, uids[0], uids[1], uids[2])
	mustExec(`UPDATE store_stats ss SET favorite_count=(SELECT count(*) FROM favorites f WHERE f.store_id=ss.store_id)`)
	searches := []struct{ query, locale string }{{"Antalya'da uygun fiyatlı perde mağazası", "tr"}, {"affordable curtain stores in Antalya", "en"}, {"günstige Gardinengeschäfte in Antalya", "de"}, {"недорогие магазины штор в Анталии", "ru"}}
	for i, seeded := range searches {
		visitorID, searchID := seedID("visitor", i), seedID("search", i)
		mustExec(`INSERT INTO visitor_sessions(id,expires_at,locale) VALUES($1,now()+interval '180 days',$2) ON CONFLICT(id) DO UPDATE SET last_seen_at=now(),expires_at=excluded.expires_at,locale=excluded.locale`, visitorID, seeded.locale)
		mustExec(`INSERT INTO searches(id,visitor_session_id,raw_query,normalized_query,parsed_intent,search_mode,location_text,internal_result_count,total_result_count,status,query_language) VALUES($1,$2,$3,lower($3),jsonb_build_object('query_language',$4::text,'normalized_query',lower($3),'location_text','Antalya','categories',jsonb_build_array('curtain','home_textile'),'product_terms',jsonb_build_array('curtain'),'price_intent','budget'),'classic','Antalya',2,2,'completed',$4::supported_locale) ON CONFLICT(id) DO UPDATE SET raw_query=excluded.raw_query,query_language=excluded.query_language`, searchID, visitorID, seeded.query, seeded.locale)
		for rank := 1; rank <= 2; rank++ {
			storeID := ids[rank-1]
			resultID := seedID("search-result", i*10+rank)
			mustExec(`INSERT INTO search_results(id,search_id,rank,store_id,source,platform_rating_at_time,platform_review_count_at_time,favorite_count_at_time,platform_post_count_at_time,ranking_reason) SELECT $1,$2,$3,$4,'internal',average_rating,review_count,favorite_count,post_count,'seed_internal' FROM store_stats WHERE store_id=$4 ON CONFLICT(id) DO NOTHING`, resultID, searchID, rank, storeID)
			if rank == 1 {
				mustExec(`INSERT INTO search_interactions(search_id,search_result_id,visitor_session_id,store_id,event_type,idempotency_key) VALUES($1,$2,$3,$4,'result_click',$5) ON CONFLICT DO NOTHING`, searchID, resultID, visitorID, storeID, "seed-click-"+seeded.locale)
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	if reportSvc, re := reporting.NewService(db, cfg.ReportingTimezone, cfg.SearchAttributionWindow); re == nil {
		if re = reportSvc.Rebuild(ctx); re != nil {
			log.Fatal(re)
		}
	} else {
		log.Fatal(re)
	}
	log.Printf("seeded %d stores and %d users", len(stores), len(users))
}

func seedID(kind string, index int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("home-app-seed:"+kind+":"+fmt.Sprint(index)))
}
