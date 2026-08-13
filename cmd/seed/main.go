package main

import (
	"context"
	"log"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
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
	type store struct {
		name, slug, brand, address, city, district string
		lat, lon                                   float64
		category, google                           string
	}
	stores := []store{{"IKEA Bayrampaşa", "ikea-bayrampasa", "IKEA", "Kocatepe Mah. Paşa Cad. No:1", "İstanbul", "Bayrampaşa", 41.0451, 28.8972, "furniture", "ChIJ_seed_ikea_bayrampasa"}, {"Akasya Ev Tekstili", "akasya-ev-tekstili", "", "Kadıköy çarşı", "İstanbul", "Kadıköy", 40.9908, 29.0277, "home_textile", ""}, {"Çankaya Modern Mobilya", "cankaya-modern-mobilya", "", "Turan Güneş Bulvarı", "Ankara", "Çankaya", 39.8736, 32.8597, "furniture", ""}, {"Lara Perde & Ev", "lara-perde-ev", "", "Lara Caddesi", "Antalya", "Muratpaşa", 36.8534, 30.7774, "curtain", ""}, {"İzmir Işık Tasarım", "izmir-isik-tasarim", "", "Mithatpaşa Caddesi", "İzmir", "Konak", 38.4089, 27.1177, "lighting", ""}}
	ids := make([]uuid.UUID, len(stores))
	for i, x := range stores {
		ids[i] = uuid.New()
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
	users := []struct{ email, username, name string }{{"ayse@example.test", "ayseevde", "Ayşe Yılmaz"}, {"mert@example.test", "mertdekor", "Mert Kaya"}, {"selin@example.test", "selinhome", "Selin Demir"}}
	uids := make([]uuid.UUID, len(users))
	for i, u := range users {
		uids[i] = uuid.New()
		_, e = tx.Exec(ctx, `INSERT INTO users(id,primary_email) VALUES($1,$2) ON CONFLICT(primary_email) DO NOTHING`, uids[i], u.email)
		if e != nil {
			log.Fatal(e)
		}
		_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE primary_email=$1`, u.email).Scan(&uids[i])
		_, _ = tx.Exec(ctx, `INSERT INTO user_profiles(user_id,username,display_name,city,bio) VALUES($1,$2,$3,'İstanbul','Ev ve dekorasyon mağazalarını geziyorum.') ON CONFLICT(user_id) DO NOTHING`, uids[i], u.username, u.name)
		_, _ = tx.Exec(ctx, `INSERT INTO auth_identities(user_id,provider,provider_subject,normalized_email,email_verified) VALUES($1,'email',$2,$2,true) ON CONFLICT DO NOTHING`, uids[i], u.email)
	}
	posts := []struct {
		u, st  int
		text   string
		rating int
	}{{0, 0, "Geniş ürün seçeneği var; hafta içi gitmek çok daha rahat.", 5}, {1, 1, "Kumaş kalitesi güzel, fiyatları karşılaştırmaya değer.", 4}, {2, 2, "Modern koltuk seçeneklerini yerinde görmek faydalı oldu.", 4}, {0, 3, "Perde ölçüsü konusunda çok yardımcı oldular.", 5}}
	for _, p := range posts {
		id := uuid.New()
		_, e = tx.Exec(ctx, `INSERT INTO posts(id,user_id,store_id,body,rating,verification_distance_meters,verified_at) VALUES($1,$2,$3,$4,$5,25,now()) ON CONFLICT DO NOTHING`, id, uids[p.u], ids[p.st], p.text, p.rating)
		if e != nil {
			log.Fatal(e)
		}
		_, _ = tx.Exec(ctx, `INSERT INTO likes(user_id,post_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uids[(p.u+1)%len(uids)], id)
		_, _ = tx.Exec(ctx, `INSERT INTO comments(post_id,user_id,body) VALUES($1,$2,'Paylaşım için teşekkürler!')`, id, uids[(p.u+2)%len(uids)])
	}
	_, e = tx.Exec(ctx, `UPDATE store_stats ss SET rating_count=x.n,review_count=x.n,post_count=x.n,average_rating=x.avg FROM (SELECT store_id,count(*)::int n,avg(rating) avg FROM posts WHERE deleted_at IS NULL GROUP BY store_id)x WHERE ss.store_id=x.store_id`)
	if e != nil {
		log.Fatal(e)
	}
	for i := range users {
		_, _ = tx.Exec(ctx, `INSERT INTO favorites(user_id,store_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uids[i], ids[i%len(ids)])
	}
	_, _ = tx.Exec(ctx, `UPDATE store_stats ss SET favorite_count=(SELECT count(*) FROM favorites f WHERE f.store_id=ss.store_id)`)
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	if reportSvc, re := reporting.NewService(db, cfg.ReportingTimezone); re == nil {
		if re = reportSvc.Rebuild(ctx); re != nil {
			log.Fatal(re)
		}
	} else {
		log.Fatal(re)
	}
	log.Printf("seeded %d stores and %d users", len(stores), len(users))
}
