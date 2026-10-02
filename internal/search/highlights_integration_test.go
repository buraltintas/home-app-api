//go:build integration

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The inner join gives the home page exactly what the outer join gave it. Checked on the
// cases where the two could part: shops with no reviews, with only deleted ones, with only
// ones held for a moderator, with reviews deleted after the window opened (which still count
// towards the earlier rating), and shops at and around every threshold.
func TestTheInnerJoinGivesTheSameHighlights(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL/PostGIS integration suite")
	}
	if parsed, e := url.Parse(raw); e != nil || !map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}[parsed.Hostname()] {
		t.Fatal("refusing to seed test reviews anywhere but a throwaway local database")
	}
	ctx := context.Background()
	db, e := pgxpool.New(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, sql, args...); e != nil {
			t.Fatal(sql, e)
		}
	}
	clean := func() {
		exec(`DELETE FROM posts WHERE store_id IN (SELECT id FROM stores WHERE slug LIKE 'hltest-%')`)
		exec(`DELETE FROM stores WHERE slug LIKE 'hltest-%'`)
		exec(`DELETE FROM user_profiles WHERE display_name='Highlight Tester'`)
		exec(`DELETE FROM users WHERE primary_email::text LIKE 'hltest-%'`)
	}
	clean()
	defer clean()
	var users []uuid.UUID
	for i := 0; i < 6; i++ {
		id := uuid.New()
		exec(`INSERT INTO users(id,primary_email) VALUES($1,$2)`, id, fmt.Sprintf("hltest-%s@example.test", id))
		exec(`INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2::text::citext,'Highlight Tester')`, id, "hl_"+id.String()[:8])
		users = append(users, id)
	}
	type review struct {
		user      int
		rating    int
		daysAgo   int
		deleted   int // days ago, 0 for not deleted
		moderated string
	}
	shops := map[string][]review{
		"no-reviews":     nil,
		"only-deleted":   {{0, 5, 3, 1, "published"}, {1, 4, 50, 40, "published"}},
		"only-held":      {{0, 5, 2, 0, "held"}, {1, 5, 2, 0, "removed"}},
		"one-recent":     {{2, 3, 1, 0, "published"}},
		"crowd":          {{0, 5, 1, 0, "published"}, {1, 5, 2, 0, "published"}, {2, 4, 3, 0, "published"}, {3, 2, 60, 0, "published"}, {4, 2, 70, 0, "published"}},
		"crowd-deleted":  {{0, 5, 1, 0, "published"}, {1, 5, 2, 0, "published"}, {2, 5, 3, 0, "published"}, {3, 1, 60, 5, "published"}, {4, 1, 70, 0, "published"}, {5, 3, 80, 0, "published"}},
		"one-voice":      {{0, 5, 1, 0, "published"}, {0, 5, 2, 0, "published"}, {0, 5, 3, 0, "published"}, {0, 1, 60, 0, "published"}, {0, 1, 70, 0, "published"}},
		"old-crowd":      {{0, 4, 40, 0, "published"}, {1, 4, 41, 0, "published"}, {2, 4, 42, 0, "published"}, {3, 4, 43, 0, "published"}, {4, 4, 44, 0, "published"}},
		"just-enough":    {{0, 4, 5, 0, "published"}, {1, 4, 6, 0, "published"}, {2, 5, 7, 0, "published"}, {3, 3, 45, 0, "published"}, {4, 3, 46, 2, "published"}},
		"held-and-shown": {{0, 4, 5, 0, "held"}, {1, 4, 6, 0, "published"}},
	}
	for name, reviews := range shops {
		id := uuid.New()
		exec(`INSERT INTO stores(id,name,slug,city,location) VALUES($1,$2,$3,'Hlkent',ST_SetSRID(ST_MakePoint(29,41),4326)::geography)`, id, "HL "+name, "hltest-"+name)
		for _, r := range reviews {
			var deleted any
			if r.deleted > 0 {
				deleted = time.Now().Add(-time.Duration(r.deleted) * 24 * time.Hour)
			}
			exec(`INSERT INTO posts(user_id,store_id,body,rating,verification_distance_meters,verified_at,created_at,deleted_at,moderation) VALUES($1,$2,'x',$3,1,now(),now()-($4::int*interval '1 day'),$5,$6)`,
				users[r.user], id, fmt.Sprint(r.rating), r.daysAgo, deleted, r.moderated)
		}
	}
	svc := &Service{db: db, now: time.Now}
	outer := strings.Replace(monthlyHighlightBase, "\n  JOIN posts p ON", "\n  LEFT JOIN posts p ON", 1)
	if outer == monthlyHighlightBase {
		t.Fatal("the comparison is not comparing anything: the join was not found")
	}
	fixed := time.Now()
	svc.now = func() time.Time { return fixed }
	inner, e := svc.monthlyHighlights(ctx, monthlyHighlightBase)
	if e != nil {
		t.Fatal(e)
	}
	before, e := svc.monthlyHighlights(ctx, outer)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(inner)
	b, _ := json.Marshal(before)
	if string(a) != string(b) {
		t.Fatalf("the inner join changed the home page:\n inner %s\n outer %s", a, b)
	}
	if inner.MostReviewed == nil || inner.RatingGainer == nil || len(inner.Recent) == 0 {
		t.Fatalf("the fixture did not reach every list, so it proves nothing: %s", a)
	}
}
