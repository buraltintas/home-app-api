//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/auth"
	"github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/media"
	"github.com/burakaltintas/home-app-api/internal/middleware"
	"github.com/burakaltintas/home-app-api/internal/notification"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/burakaltintas/home-app-api/internal/search"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/burakaltintas/home-app-api/internal/social"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	userpkg "github.com/burakaltintas/home-app-api/internal/user"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testHashKey = "integration-otp-secret-more-than-32-bytes"

type googleStub struct {
	identities map[string]auth.GoogleIdentity
}

func (g googleStub) Verify(_ context.Context, token string) (auth.GoogleIdentity, error) {
	v, ok := g.identities[token]
	if !ok {
		return v, errors.New("invalid token")
	}
	return v, nil
}

type placesStub struct{ place search.Place }

func (p placesStub) TextSearch(context.Context, string, *float64, *float64, int) ([]search.Place, error) {
	return []search.Place{p.place}, nil
}
func (p placesStub) PlaceDetails(context.Context, string) (search.Place, error) { return p.place, nil }

type countingPlacesStub struct{ calls int }

func (p *countingPlacesStub) TextSearch(context.Context, string, *float64, *float64, int) ([]search.Place, error) {
	p.calls++
	return nil, nil
}

func (p *countingPlacesStub) PlaceDetails(context.Context, string) (search.Place, error) {
	return search.Place{}, errors.New("unexpected place details call")
}

func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL/PostGIS integration suite")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.Ping(t.Context()); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func services(t *testing.T, db *pgxpool.Pool, google auth.GoogleVerifier, places search.PlacesProvider) (*auth.Service, *storepkg.Service, *social.Service, *search.Service, *reporting.Service) {
	t.Helper()
	report, err := reporting.NewService(db, "Europe/Istanbul", 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokens := security.NewTokenManager("integration-access-secret-more-than-32-bytes", 15*time.Minute, 24*time.Hour)
	authSvc := auth.NewService(db, auth.Config{OTPTTL: 10 * time.Minute, OTPMaxAttempts: 5, OTPEmailLimit: 20, OTPIPLimit: 20, OTPVisitorLimit: 20, VisitorTTL: 24 * time.Hour, RefreshTTL: 24 * time.Hour, HashKey: []byte(testHashKey)}, tokens, google, report)
	stores := storepkg.NewService(db, report)
	socialSvc := social.NewService(db, social.Config{ReviewRadiusMeters: 500, VisitProofTTL: 30 * 24 * time.Hour, MaxLocationAccuracyMeters: 100}, report)
	searchSvc := search.NewService(db, stores, nil, places, "", 3, report, 72*time.Hour, 24*time.Hour)
	return authSvc, stores, socialSvc, searchSvc, report
}

func user(t *testing.T, db *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	username := "u_" + id.String()[:8]
	if _, err := db.Exec(t.Context(), `INSERT INTO users(id,primary_email) VALUES($1,$2)`, id, email); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2::text::citext,$2::text)`, id, username); err != nil {
		t.Fatal(err)
	}
	return id
}

func store(t *testing.T, db *pgxpool.Pool, lat, lon float64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Exec(t.Context(), `INSERT INTO stores(id,name,slug,city,location) VALUES($1,'Integration Store',$2,'İstanbul',ST_SetSRID(ST_MakePoint($4,$3),4326)::geography)`, id, "integration-"+id.String(), lat, lon); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO store_stats(store_id) VALUES($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func appCode(err error) string {
	var app *httpapi.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

func TestAuthenticationIdentityAndSessionLifecycle(t *testing.T) {
	db := database(t)
	email := "integration-" + uuid.NewString() + "@example.test"
	google := googleStub{map[string]auth.GoogleIdentity{"google": {Subject: "google-" + uuid.NewString(), Email: email, EmailVerified: true}}}
	authSvc, _, _, _, _ := services(t, db, google, nil)
	code := "123456"
	if _, err := db.Exec(t.Context(), `INSERT INTO email_verification_codes(normalized_email,code_hash,max_attempts,expires_at) VALUES($1,$2,5,now()+interval '10 minutes')`, email, security.Hash([]byte(testHashKey), code)); err != nil {
		t.Fatal(err)
	}
	pairA, err := authSvc.VerifyCode(t.Context(), email, code, auth.Client{Type: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	pairGoogle, err := authSvc.Google(t.Context(), "google", auth.Client{Type: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	if pairGoogle.UserID != pairA.UserID {
		t.Fatalf("identity merge created different users: %s != %s", pairGoogle.UserID, pairA.UserID)
	}
	var users, identities, sessions int
	if err = db.QueryRow(t.Context(), `SELECT count(DISTINCT u.id),count(DISTINCT i.id),count(DISTINCT s.id) FROM users u JOIN auth_identities i ON i.user_id=u.id JOIN auth_sessions s ON s.user_id=u.id WHERE u.primary_email=$1`, email).Scan(&users, &identities, &sessions); err != nil {
		t.Fatal(err)
	}
	if users != 1 || identities != 2 || sessions != 2 {
		t.Fatalf("users=%d identities=%d sessions=%d", users, identities, sessions)
	}
	pairB, err := authSvc.Refresh(t.Context(), pairA.RefreshToken, auth.Client{Type: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authSvc.Refresh(t.Context(), pairA.RefreshToken, auth.Client{}); appCode(err) != "INVALID_REFRESH_TOKEN" {
		t.Fatalf("old refresh replay: %v", err)
	}
	if _, err = authSvc.Refresh(t.Context(), pairB.RefreshToken, auth.Client{}); appCode(err) != "INVALID_REFRESH_TOKEN" {
		t.Fatalf("session family survived replay: %v", err)
	}
	if err = authSvc.Logout(t.Context(), pairGoogle.UserID, pairGoogle.SessionID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = authSvc.Refresh(t.Context(), pairGoogle.RefreshToken, auth.Client{}); appCode(err) != "INVALID_REFRESH_TOKEN" {
		t.Fatalf("logout-all refresh: %v", err)
	}
}

func TestConcurrentIdentityCreationProducesOneUser(t *testing.T) {
	db := database(t)
	email := "concurrent-" + uuid.NewString() + "@example.test"
	google := googleStub{map[string]auth.GoogleIdentity{"same-token": {Subject: "subject-" + uuid.NewString(), Email: email, EmailVerified: true}}}
	authSvc, _, _, _, _ := services(t, db, google, nil)
	const attempts = 20
	users := make(chan uuid.UUID, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pair, err := authSvc.Google(context.Background(), "same-token", auth.Client{Type: "integration"})
			users <- pair.UserID
			errs <- err
		}()
	}
	wg.Wait()
	close(users)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := uuid.Nil
	for id := range users {
		if want == uuid.Nil {
			want = id
		} else if id != want {
			t.Fatalf("concurrent identity resolved to %s and %s", want, id)
		}
	}
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE primary_email=$1`, email).Scan(&count); err != nil || count != 1 {
		t.Fatalf("users=%d err=%v", count, err)
	}
}

func TestConcurrentEmailAndGoogleIdentityMergeProducesOneUser(t *testing.T) {
	db := database(t)
	emailAddress := "cross-provider-" + uuid.NewString() + "@example.test"
	code := "246810"
	google := googleStub{map[string]auth.GoogleIdentity{"google-concurrent": {Subject: "subject-" + uuid.NewString(), Email: emailAddress, EmailVerified: true}}}
	authSvc, _, _, _, _ := services(t, db, google, nil)
	if _, err := db.Exec(t.Context(), `INSERT INTO email_verification_codes(normalized_email,code_hash,max_attempts,expires_at,locale) VALUES($1,$2,5,now()+interval '10 minutes','en')`, emailAddress, security.Hash([]byte(testHashKey), code)); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	pairs := make(chan auth.TokenPair, 2)
	errs := make(chan error, 2)
	go func() {
		<-start
		pair, err := authSvc.VerifyCode(context.Background(), emailAddress, code, auth.Client{Type: "email-concurrent"})
		pairs <- pair
		errs <- err
	}()
	go func() {
		<-start
		pair, err := authSvc.Google(context.Background(), "google-concurrent", auth.Client{Type: "google-concurrent"})
		pairs <- pair
		errs <- err
	}()
	close(start)
	var got []auth.TokenPair
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		got = append(got, <-pairs)
	}
	if got[0].UserID == uuid.Nil || got[0].UserID != got[1].UserID {
		t.Fatalf("cross-provider user IDs differ: %s %s", got[0].UserID, got[1].UserID)
	}
	var users, identities int
	if err := db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users WHERE primary_email=$1),(SELECT count(*) FROM auth_identities WHERE normalized_email=$1 AND email_verified)`, emailAddress).Scan(&users, &identities); err != nil || users != 1 || identities != 2 {
		t.Fatalf("users=%d identities=%d err=%v", users, identities, err)
	}
}

func TestOTPExpiryAttemptsLimitsAndHashedStorage(t *testing.T) {
	db := database(t)
	_, _, _, _, report := services(t, db, googleStub{}, nil)
	tokens := security.NewTokenManager("integration-access-secret-more-than-32-bytes", 15*time.Minute, 24*time.Hour)
	authSvc := auth.NewService(db, auth.Config{OTPTTL: 10 * time.Minute, OTPMaxAttempts: 2, OTPEmailLimit: 2, OTPIPLimit: 10, OTPVisitorLimit: 10, VisitorTTL: 24 * time.Hour, RefreshTTL: 24 * time.Hour, HashKey: []byte(testHashKey)}, tokens, googleStub{}, report)

	expiredEmail := "expired-" + uuid.NewString() + "@example.test"
	if _, err := db.Exec(t.Context(), `INSERT INTO email_verification_codes(normalized_email,code_hash,max_attempts,expires_at,locale) VALUES($1,$2,2,now()-interval '1 second','tr')`, expiredEmail, security.Hash([]byte(testHashKey), "123456")); err != nil {
		t.Fatal(err)
	}
	if _, err := authSvc.VerifyCode(t.Context(), expiredEmail, "123456", auth.Client{}); appCode(err) != "INVALID_CODE" {
		t.Fatalf("expired OTP: %v", err)
	}

	attemptEmail := "attempt-" + uuid.NewString() + "@example.test"
	if _, err := db.Exec(t.Context(), `INSERT INTO email_verification_codes(normalized_email,code_hash,max_attempts,expires_at,locale) VALUES($1,$2,2,now()+interval '10 minutes','tr')`, attemptEmail, security.Hash([]byte(testHashKey), "123456")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := authSvc.VerifyCode(t.Context(), attemptEmail, "000000", auth.Client{}); appCode(err) != "INVALID_CODE" {
			t.Fatalf("wrong OTP: %v", err)
		}
	}
	if _, err := authSvc.VerifyCode(t.Context(), attemptEmail, "123456", auth.Client{}); appCode(err) != "INVALID_CODE" {
		t.Fatalf("attempt-limited OTP: %v", err)
	}

	limitedEmail := "limited-" + uuid.NewString() + "@example.test"
	for i := 0; i < 2; i++ {
		if err := authSvc.RequestCode(i18n.WithLocale(t.Context(), i18n.LocaleDE), limitedEmail, nil, []byte("same-ip")); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if err := authSvc.RequestCode(t.Context(), limitedEmail, nil, []byte("same-ip")); appCode(err) != "RATE_LIMITED" {
		t.Fatalf("OTP request limit: %v", err)
	}
	var hash []byte
	var payload string
	if err := db.QueryRow(t.Context(), `SELECT c.code_hash,o.payload::text FROM email_verification_codes c JOIN email_outbox o ON o.idempotency_key='otp:'||c.id::text WHERE c.normalized_email=$1 ORDER BY c.created_at DESC LIMIT 1`, limitedEmail).Scan(&hash, &payload); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 || strings.Contains(payload, `"code"`) {
		t.Fatalf("OTP storage hash_len=%d payload=%s", len(hash), payload)
	}
}

func TestPostGISReviewSocialFeedSearchAndReporting(t *testing.T) {
	db := database(t)
	author := user(t, db, "author-"+uuid.NewString()+"@example.test")
	viewer := user(t, db, "viewer-"+uuid.NewString()+"@example.test")
	storeID := store(t, db, 41.0000, 29.0000)
	_, stores, socialSvc, searchSvc, report := services(t, db, googleStub{}, nil)

	postID, err := socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: "Gerçek PostGIS entegrasyon yorumu", Rating: 5, Latitude: 41.003, Longitude: 29.0000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: "Bu konum çok uzakta", Rating: 3, Latitude: 41.010, Longitude: 29.0000}); appCode(err) != "STORE_VISIT_NOT_VERIFIED" {
		t.Fatalf("outside-radius review: %v", err)
	}
	if _, err = socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: "Sınırın hemen içindeki ziyaret", Rating: 4, Latitude: 41.00448, Longitude: 29.0000}); err != nil {
		t.Fatalf("inside boundary review: %v", err)
	}
	if _, err = socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: "Sınırın hemen dışındaki ziyaret", Rating: 4, Latitude: 41.00452, Longitude: 29.0000}); appCode(err) != "STORE_VISIT_NOT_VERIFIED" {
		t.Fatalf("outside boundary review: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err = stores.Favorite(t.Context(), viewer, storeID, true); err != nil {
			t.Fatal(err)
		}
		if err = socialSvc.Like(t.Context(), viewer, postID, true); err != nil {
			t.Fatal(err)
		}
		if err = socialSvc.Follow(t.Context(), viewer, author, true); err != nil {
			t.Fatal(err)
		}
	}
	if err = socialSvc.Follow(t.Context(), viewer, viewer, true); appCode(err) != "CANNOT_FOLLOW_SELF" {
		t.Fatalf("self-follow: %v", err)
	}
	feed, cursor, err := socialSvc.Feed(t.Context(), &viewer, "", 1)
	if err != nil || len(feed) != 1 {
		t.Fatalf("feed: len=%d cursor=%q err=%v", len(feed), cursor, err)
	}
	post, err := socialSvc.GetPost(t.Context(), postID, &viewer)
	if err != nil {
		t.Fatal(err)
	}
	if !post.ViewerLiked || !post.ViewerFollows || !post.ViewerFavorited || !post.VisitVerified {
		t.Fatalf("incorrect viewer state: %+v", post)
	}
	items, err := stores.Search(t.Context(), "Integration", nil, "", nil, nil, 10000, 20, &viewer)
	if err != nil || len(items) == 0 {
		t.Fatalf("store search: %v (%d results)", err, len(items))
	}
	searchID, visitorID, err := searchSvc.RecordInternalSearch(t.Context(), &viewer, nil, search.Request{Query: "Integration Store"}, items, time.Millisecond)
	if err != nil || visitorID != nil {
		t.Fatalf("record search: visitor=%v err=%v", visitorID, err)
	}
	var resultID uuid.UUID
	if err = db.QueryRow(t.Context(), `SELECT id FROM search_results WHERE search_id=$1 ORDER BY rank LIMIT 1`, searchID).Scan(&resultID); err != nil {
		t.Fatal(err)
	}
	if err = searchSvc.Interaction(t.Context(), searchID, &viewer, nil, &resultID, "result_click", "click-1"); err != nil {
		t.Fatal(err)
	}
	if err = searchSvc.Attribute(t.Context(), searchID, resultID, viewer, items[0].ID, "favorite", "favorite-1"); err != nil {
		t.Fatal(err)
	}
	if err = searchSvc.Attribute(t.Context(), searchID, resultID, viewer, items[0].ID, "review_created", "review-1"); err != nil {
		t.Fatalf("review attribution: %v", err)
	}
	var attributedReviews int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM search_interactions WHERE search_id=$1 AND search_result_id=$2 AND event_type='review_created'`, searchID, resultID).Scan(&attributedReviews); err != nil || attributedReviews != 1 {
		t.Fatalf("attributed reviews=%d err=%v", attributedReviews, err)
	}
	if _, err = db.Exec(t.Context(), `UPDATE searches SET created_at=now()-interval '73 hours' WHERE id=$1`, searchID); err != nil {
		t.Fatal(err)
	}
	if err = searchSvc.Attribute(t.Context(), searchID, resultID, viewer, items[0].ID, "favorite", "favorite-expired"); appCode(err) != "SEARCH_ATTRIBUTION_INVALID" {
		t.Fatalf("expired attribution: %v", err)
	}
	if err = report.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := report.GetPlatformSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = report.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := report.GetPlatformSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.RegisteredUsersTotal != second.RegisteredUsersTotal || first.PostsCurrentTotal != second.PostsCurrentTotal || first.SearchesLifetime != second.SearchesLifetime {
		t.Fatalf("rebuild is not idempotent: first=%+v second=%+v", first, second)
	}
}

func TestStoredVisitVerificationCanCreateExactlyOneLaterReview(t *testing.T) {
	db := database(t)
	author := user(t, db, "visit-proof-"+uuid.NewString()+"@example.test")
	storeID := store(t, db, 41, 29)
	_, _, socialSvc, _, _ := services(t, db, googleStub{}, nil)

	if _, err := socialSvc.VerifyVisit(t.Context(), author, storeID, 41, 29, 101); appCode(err) != "INVALID_INPUT" {
		t.Fatalf("inaccurate visit: %v", err)
	}
	if _, err := socialSvc.VerifyVisit(t.Context(), author, storeID, 41.01, 29, 10); appCode(err) != "STORE_VISIT_NOT_VERIFIED" {
		t.Fatalf("distant visit: %v", err)
	}
	proof, err := socialSvc.VerifyVisit(t.Context(), author, storeID, 41, 29, 10)
	if err != nil {
		t.Fatal(err)
	}
	postID, err := socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: "Daha önce doğruladığım mağaza ziyareti", Rating: 5, VisitVerificationID: &proof.ID})
	if err != nil {
		t.Fatal(err)
	}
	post, err := socialSvc.GetPost(t.Context(), postID, &author)
	if err != nil || !post.VisitVerified || post.DistanceMeters != proof.DistanceMeters {
		t.Fatalf("post=%+v err=%v proof=%+v", post, err, proof)
	}
	if _, err = socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: "Aynı kanıt yeniden kullanılamaz", Rating: 4, VisitVerificationID: &proof.ID}); appCode(err) != "VISIT_VERIFICATION_INVALID" {
		t.Fatalf("reused proof: %v", err)
	}
}

func TestFeedCursorTieBreakerHasNoDuplicatesOrGaps(t *testing.T) {
	db := database(t)
	author := user(t, db, "pagination-"+uuid.NewString()+"@example.test")
	storeID := store(t, db, 41, 29)
	_, _, socialSvc, _, _ := services(t, db, googleStub{}, nil)
	created := time.Now().Add(24 * time.Hour).Truncate(time.Microsecond)
	expected := map[uuid.UUID]bool{}
	for i := 0; i < 6; i++ {
		id := uuid.New()
		expected[id] = true
		if _, err := db.Exec(t.Context(), `INSERT INTO posts(id,user_id,store_id,body,rating,verification_distance_meters,verified_at,created_at) VALUES($1,$2,$3,$4,5,0,$5,$5)`, id, author, storeID, fmt.Sprintf("pagination %d", i), created); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for page := 0; page < 3; page++ {
		items, next, err := socialSvc.Feed(t.Context(), nil, cursor, 2)
		if err != nil || len(items) != 2 {
			t.Fatalf("page=%d len=%d err=%v", page, len(items), err)
		}
		for _, item := range items {
			if !expected[item.ID] || seen[item.ID] {
				t.Fatalf("unexpected or duplicate post %s", item.ID)
			}
			seen[item.ID] = true
		}
		cursor = next
	}
	if len(seen) != len(expected) {
		t.Fatalf("seen=%d expected=%d", len(seen), len(expected))
	}
}

func TestFeedWithLocationRanksStoresByViewerDistance(t *testing.T) {
	db := database(t)
	author := user(t, db, "nearby-feed-"+uuid.NewString()+"@example.test")
	nearStore := store(t, db, 41.001, 29)
	farStore := store(t, db, 41.02, 29)
	_, _, socialSvc, _, _ := services(t, db, googleStub{}, nil)
	nearPost, farPost := uuid.New(), uuid.New()
	created := time.Now().Add(48 * time.Hour).Truncate(time.Microsecond)
	for _, item := range []struct {
		id, store uuid.UUID
		body      string
	}{{nearPost, nearStore, "nearby feed near"}, {farPost, farStore, "nearby feed far"}} {
		if _, err := db.Exec(t.Context(), `INSERT INTO posts(id,user_id,store_id,body,rating,verification_distance_meters,verified_at,created_at) VALUES($1,$2,$3,$4,5,0,$5,$5)`, item.id, author, item.store, item.body, created); err != nil {
			t.Fatal(err)
		}
	}
	lat, lon := 41.0, 29.0
	items, _, err := socialSvc.Feed(t.Context(), nil, "", 50, social.FeedContext{Latitude: &lat, Longitude: &lon})
	if err != nil {
		t.Fatal(err)
	}
	nearIndex, farIndex := -1, -1
	lastDistance := -1.0
	for index, item := range items {
		if item.StoreDistanceMeters == nil || *item.StoreDistanceMeters < lastDistance {
			t.Fatalf("feed is not distance ordered at %d: %+v", index, item)
		}
		lastDistance = *item.StoreDistanceMeters
		if item.ID == nearPost {
			nearIndex = index
		}
		if item.ID == farPost {
			farIndex = index
		}
	}
	if nearIndex < 0 || farIndex < 0 || nearIndex >= farIndex {
		t.Fatalf("near_index=%d far_index=%d", nearIndex, farIndex)
	}
}

func TestConcurrentGoogleStoreMaterialization(t *testing.T) {
	db := database(t)
	placeID := "integration-place-" + uuid.NewString()
	place := search.Place{PlaceID: placeID, Name: "Concurrent Store", Address: "Kadıköy, İstanbul, TR", Latitude: 40.99, Longitude: 29.03}
	_, _, _, searchSvc, _ := services(t, db, googleStub{}, placesStub{place})
	const count = 20
	ids := make(chan uuid.UUID, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(locale i18n.Locale) {
			defer wg.Done()
			id, err := searchSvc.MaterializeGoogleStore(i18n.WithLocale(context.Background(), locale), placeID)
			ids <- id
			errs <- err
		}(i18n.Supported()[i%len(i18n.Supported())])
	}
	wg.Wait()
	close(ids)
	close(errs)
	var want uuid.UUID
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if want == uuid.Nil {
			want = id
		} else if id != want {
			t.Fatalf("materialization returned %s and %s", want, id)
		}
	}
	var stores, mappings int
	if err := db.QueryRow(t.Context(), `SELECT count(DISTINCT s.id),count(DISTINCT x.id) FROM stores s JOIN store_external_sources x ON x.store_id=s.id WHERE x.provider='google' AND x.external_id=$1`, placeID).Scan(&stores, &mappings); err != nil {
		t.Fatal(err)
	}
	if stores != 1 || mappings != 1 {
		t.Fatal(fmt.Sprintf("stores=%d mappings=%d", stores, mappings))
	}
}

func TestSearchSeparatesGoogleOnlyAndPlatformEnrichedRatings(t *testing.T) {
	db := database(t)
	searcher := user(t, db, "search-enrichment-"+uuid.NewString()+"@example.test")
	placeID := "enrichment-place-" + uuid.NewString()
	place := search.Place{PlaceID: placeID, Name: "External Locale Store", Address: "Kadıköy, İstanbul, TR", Latitude: 40.99, Longitude: 29.03, Rating: 4.8, RatingCount: 321}
	_, _, _, searchSvc, _ := services(t, db, googleStub{}, placesStub{place})

	first, err := searchSvc.Search(i18n.WithLocale(t.Context(), i18n.LocaleEN), &searcher, nil, search.Request{Query: "furniture store External Locale Store"})
	if err != nil {
		t.Fatal(err)
	}
	var googleOnly *search.Result
	for i := range first.Results {
		if first.Results[i].Google != nil && first.Results[i].Google.PlaceID == placeID {
			googleOnly = &first.Results[i]
			break
		}
	}
	if googleOnly == nil || googleOnly.Source != "google" || googleOnly.Platform != nil || googleOnly.Google.Rating != 4.8 || googleOnly.Google.RatingCount != 321 {
		t.Fatalf("google-only result=%+v", googleOnly)
	}

	storeID, err := searchSvc.MaterializeGoogleStore(i18n.WithLocale(t.Context(), i18n.LocaleDE), placeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(t.Context(), `UPDATE store_stats SET average_rating=3.25,rating_count=4,review_count=4,favorite_count=2,post_count=4 WHERE store_id=$1`, storeID); err != nil {
		t.Fatal(err)
	}
	second, err := searchSvc.Search(i18n.WithLocale(t.Context(), i18n.LocaleRU), &searcher, nil, search.Request{Query: "furniture store External Locale Store"})
	if err != nil {
		t.Fatal(err)
	}
	var enriched *search.Result
	for i := range second.Results {
		if second.Results[i].Google != nil && second.Results[i].Google.PlaceID == placeID {
			enriched = &second.Results[i]
			break
		}
	}
	if enriched == nil || enriched.Source != "google+platform" || enriched.Platform == nil || enriched.Platform.AverageRating != 3.25 || enriched.Platform.ReviewCount != 4 || enriched.Google.Rating != 4.8 || enriched.Google.RatingCount != 321 {
		t.Fatalf("enriched result=%+v", enriched)
	}
}

func TestOutOfScopeSearchReturnsGuidanceWithoutCallingProviders(t *testing.T) {
	db := database(t)
	searcher := user(t, db, "search-scope-"+uuid.NewString()+"@example.test")
	places := &countingPlacesStub{}
	_, _, _, searchSvc, _ := services(t, db, googleStub{}, places)

	response, err := searchSvc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), &searcher, nil, search.Request{Query: "Yakınımda lastikçi lazım"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Intent.Scope != search.ScopeOutOfScope || len(response.Results) != 0 || response.Guidance == nil || response.Guidance.Reason != search.ScopeOutOfScope {
		t.Fatalf("unexpected response: %+v", response)
	}
	if places.calls != 0 {
		t.Fatalf("places calls=%d", places.calls)
	}
	var internalCount, externalCount int
	var googleUsed bool
	if err = db.QueryRow(t.Context(), `SELECT internal_result_count,external_result_count,google_places_used FROM searches WHERE id=$1`, response.SearchID).Scan(&internalCount, &externalCount, &googleUsed); err != nil {
		t.Fatal(err)
	}
	if internalCount != 0 || externalCount != 0 || googleUsed {
		t.Fatalf("internal=%d external=%d google=%t", internalCount, externalCount, googleUsed)
	}
}

func TestLocalMediaUploadFinalizeAndAttach(t *testing.T) {
	db := database(t)
	owner := user(t, db, "media-owner-"+uuid.NewString()+"@example.test")
	other := user(t, db, "media-other-"+uuid.NewString()+"@example.test")
	storeID := store(t, db, 41, 29)
	_, _, socialSvc, _, report := services(t, db, googleStub{}, nil)
	storage, err := media.NewLocalStorage(t.TempDir(), "http://localhost:8080/uploads", time.Minute, []byte(testHashKey))
	if err != nil {
		t.Fatal(err)
	}
	mediaSvc := media.NewService(db, storage, 1<<20, report)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	created, err := mediaSvc.Create(t.Context(), owner, media.CreateRequest{MimeType: "image/png", SizeBytes: int64(len(png))})
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(created.Upload.UploadURL)
	req := httptest.NewRequest(http.MethodPut, target.RequestURI(), bytes.NewReader(png))
	req.Header.Set("Content-Type", "image/png")
	rr := httptest.NewRecorder()
	storage.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err = mediaSvc.Complete(t.Context(), other, created.ID, 1, 1); appCode(err) != "MEDIA_NOT_FOUND" {
		t.Fatalf("unauthorized finalize: %v", err)
	}
	if err = mediaSvc.Complete(t.Context(), owner, created.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err = mediaSvc.Complete(t.Context(), owner, created.ID, 1, 1); appCode(err) != "MEDIA_NOT_FOUND" {
		t.Fatalf("duplicate finalize: %v", err)
	}
	postID, err := socialSvc.CreatePost(t.Context(), owner, social.CreatePost{StoreID: storeID, Text: "Yerel dosya ile gerçek medya akışı", Rating: 5, Latitude: 41, Longitude: 29, MediaIDs: []uuid.UUID{created.ID}})
	if err != nil {
		t.Fatal(err)
	}
	post, err := socialSvc.GetPost(t.Context(), postID, &owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(post.Media) != 1 || post.Media[0].ID != created.ID || post.Media[0].URL != "/media/"+created.ID.String() {
		t.Fatalf("post media=%v", post.Media)
	}
}

func TestSocialRemovalOwnershipAndSoftDelete(t *testing.T) {
	db := database(t)
	author := user(t, db, "social-author-"+uuid.NewString()+"@example.test")
	other := user(t, db, "social-other-"+uuid.NewString()+"@example.test")
	storeID := store(t, db, 41, 29)
	_, stores, socialSvc, _, _ := services(t, db, googleStub{}, nil)
	longGermanPost := strings.Repeat("ä", 5000)
	postID, err := socialSvc.CreatePost(t.Context(), author, social.CreatePost{StoreID: storeID, Text: longGermanPost, Rating: 5, Latitude: 41, Longitude: 29})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if _, err = stores.Favorite(t.Context(), other, storeID, true); err != nil {
			t.Fatal(err)
		}
		if err = socialSvc.Like(t.Context(), other, postID, true); err != nil {
			t.Fatal(err)
		}
		if err = socialSvc.Follow(t.Context(), other, author, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = stores.Favorite(t.Context(), other, storeID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = stores.Favorite(t.Context(), other, storeID, false); err != nil {
		t.Fatal(err)
	}
	if err = socialSvc.Like(t.Context(), other, postID, false); err != nil {
		t.Fatal(err)
	}
	if err = socialSvc.Like(t.Context(), other, postID, false); err != nil {
		t.Fatal(err)
	}
	if err = socialSvc.Follow(t.Context(), other, author, false); err != nil {
		t.Fatal(err)
	}
	if err = socialSvc.Follow(t.Context(), other, author, false); err != nil {
		t.Fatal(err)
	}
	post, err := socialSvc.GetPost(t.Context(), postID, &other)
	if err != nil || post.ViewerLiked || post.ViewerFollows || post.ViewerFavorited || post.LikeCount != 0 {
		t.Fatalf("post after removals=%+v err=%v", post, err)
	}

	longRussianComment := strings.Repeat("Ж", 2000)
	commentID, err := socialSvc.AddComment(t.Context(), author, postID, longRussianComment)
	if err != nil {
		t.Fatal(err)
	}
	if err = socialSvc.DeleteComment(t.Context(), other, commentID); appCode(err) != "COMMENT_NOT_FOUND" {
		t.Fatalf("non-owner comment delete: %v", err)
	}
	if err = socialSvc.DeleteComment(t.Context(), author, commentID); err != nil {
		t.Fatal(err)
	}
	comments, err := socialSvc.Comments(t.Context(), postID, 20)
	if err != nil || len(comments) != 0 {
		t.Fatalf("deleted comments=%+v err=%v", comments, err)
	}

	if err = socialSvc.DeletePost(t.Context(), other, postID); appCode(err) != "POST_NOT_FOUND" {
		t.Fatalf("non-owner post delete: %v", err)
	}
	if err = socialSvc.DeletePost(t.Context(), author, postID); err != nil {
		t.Fatal(err)
	}
	if _, err = socialSvc.GetPost(t.Context(), postID, nil); appCode(err) != "POST_NOT_FOUND" {
		t.Fatalf("soft-deleted post retrieval: %v", err)
	}
	feed, _, err := socialSvc.Feed(t.Context(), nil, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range feed {
		if item.ID == postID {
			t.Fatal("soft-deleted post remained in feed")
		}
	}
}

func TestSearchHistoryOwnershipAndDeletion(t *testing.T) {
	db := database(t)
	owner := user(t, db, "history-owner-"+uuid.NewString()+"@example.test")
	other := user(t, db, "history-other-"+uuid.NewString()+"@example.test")
	_, stores, _, searchSvc, report := services(t, db, googleStub{}, nil)
	users := userpkg.NewService(db, report)
	items, err := stores.Search(t.Context(), "", nil, "", nil, nil, 10000, 20, &owner)
	if err != nil {
		t.Fatal(err)
	}
	searchID, _, err := searchSvc.RecordInternalSearch(t.Context(), &owner, nil, search.Request{Query: "owned history"}, items, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = users.DeleteSearches(t.Context(), other, &searchID); appCode(err) != "SEARCH_NOT_FOUND" {
		t.Fatalf("non-owner search deletion: %v", err)
	}
	if err = users.DeleteSearches(t.Context(), owner, &searchID); err != nil {
		t.Fatal(err)
	}
	history, err := users.Searches(t.Context(), owner, 20)
	if err != nil || len(history) != 0 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestReportingReadModelsExecuteAgainstAggregates(t *testing.T) {
	db := database(t)
	_, _, _, _, report := services(t, db, googleStub{}, nil)
	if err := report.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	from, to := time.Now().AddDate(0, 0, -30), time.Now()
	checks := []struct {
		name string
		call func() error
	}{
		{"daily", func() error { _, err := report.GetDailyMetrics(t.Context(), from, to); return err }},
		{"overview", func() error { _, err := report.GetSearchOverview(t.Context(), from, to); return err }},
		{"queries", func() error { _, err := report.GetTopSearchQueries(t.Context(), from, to, 20, false); return err }},
		{"zero queries", func() error { _, err := report.GetZeroResultQueries(t.Context(), from, to, 20); return err }},
		{"categories", func() error { _, err := report.GetTopSearchCategories(t.Context(), from, to, 20); return err }},
		{"locations", func() error { _, err := report.GetTopSearchLocations(t.Context(), from, to, 20); return err }},
		{"funnel", func() error { _, err := report.GetSearchConversionFunnel(t.Context(), from, to); return err }},
		{"high demand", func() error { _, err := report.GetHighDemandLowReviewStores(t.Context(), from, to, 20); return err }},
		{"impressed", func() error { _, err := report.GetMostImpressedStores(t.Context(), from, to, 20); return err }},
		{"clicked", func() error { _, err := report.GetMostClickedStores(t.Context(), from, to, 20); return err }},
		{"user growth", func() error { _, err := report.GetUserGrowth(t.Context(), from, to); return err }},
		{"review growth", func() error { _, err := report.GetReviewGrowth(t.Context(), from, to); return err }},
		{"search metrics", func() error { _, err := report.GetSearchMetrics(t.Context(), from, to); return err }},
	}
	for _, check := range checks {
		if err := check.call(); err != nil {
			t.Fatalf("%s: %v", check.name, err)
		}
	}
}

func TestAccountDeletionAnonymizesAndRevokes(t *testing.T) {
	db := database(t)
	id := user(t, db, "delete-"+uuid.NewString()+"@example.test")
	_, _, _, _, report := services(t, db, googleStub{}, nil)
	users := userpkg.NewService(db, report)
	if err := users.DeleteAccount(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var status, email, display string
	var deleted bool
	if err := db.QueryRow(t.Context(), `SELECT u.status,u.primary_email::text,p.display_name,u.deleted_at IS NOT NULL FROM users u JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1`, id).Scan(&status, &email, &display, &deleted); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" || !deleted || display != "Deleted user" || email == "" || email[:8] != "deleted+" {
		t.Fatalf("status=%q email=%q display=%q deleted=%v", status, email, display, deleted)
	}
}

func TestPushDeviceReplacementPreferencesAndOutboxClaim(t *testing.T) {
	db := database(t)
	first := user(t, db, "push-first-"+uuid.NewString()+"@example.test")
	second := user(t, db, "push-second-"+uuid.NewString()+"@example.test")
	repo := notification.NewRepository(db, []byte(testHashKey))
	token := "shared-device-token-" + uuid.NewString()
	deviceA, err := repo.RegisterDeviceLocale(t.Context(), first, "ios", token, i18n.LocaleDE)
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := repo.RegisterDeviceLocale(t.Context(), second, "android", token, i18n.LocaleRU)
	if err != nil {
		t.Fatal(err)
	}
	if deviceA != deviceB {
		t.Fatalf("token replacement created a second device: %s != %s", deviceA, deviceB)
	}
	deleted, err := repo.DeleteDevice(t.Context(), first, token)
	if err != nil || deleted {
		t.Fatalf("old owner deleted replacement device: deleted=%v err=%v", deleted, err)
	}
	if err = repo.SetPreferences(t.Context(), second, map[string]bool{"social": true, "marketing": false}); err != nil {
		t.Fatal(err)
	}
	created, err := repo.EnqueueLocalized(t.Context(), second, "social.like", notification.TemplatePostLiked, "push-"+deviceA.String(), i18n.LocaleRU, map[string]any{"actor": "Ada", "post_id": uuid.NewString()})
	if err != nil || !created {
		t.Fatalf("enqueue created=%v err=%v", created, err)
	}
	created, err = repo.EnqueueLocalized(t.Context(), second, "social.like", notification.TemplatePostLiked, "push-"+deviceA.String(), i18n.LocaleRU, map[string]any{"actor": "Ada"})
	if err != nil || created {
		t.Fatalf("duplicate enqueue created=%v err=%v", created, err)
	}
	job, ok, err := repo.Claim(t.Context())
	if err != nil || !ok || job.UserID != second || job.Locale != i18n.LocaleRU || job.TemplateKey != notification.TemplatePostLiked {
		t.Fatalf("claim ok=%v job=%+v err=%v", ok, job, err)
	}
	var deviceLocale string
	if err = db.QueryRow(t.Context(), `SELECT locale::text FROM push_devices WHERE id=$1`, deviceA).Scan(&deviceLocale); err != nil || deviceLocale != "ru" {
		t.Fatalf("replacement device locale=%q err=%v", deviceLocale, err)
	}
	if err = repo.Complete(t.Context(), job.ID, "provider-123"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentEmailWorkersDeliverOutboxOnce(t *testing.T) {
	db := database(t)
	authSvc, _, _, _, _ := services(t, db, googleStub{}, nil)
	recipient := "worker-once-" + uuid.NewString() + "@example.test"
	if err := authSvc.RequestCode(i18n.WithLocale(t.Context(), i18n.LocaleRU), recipient, nil, nil); err != nil {
		t.Fatal(err)
	}
	var outboxID uuid.UUID
	if err := db.QueryRow(t.Context(), `SELECT id FROM email_outbox WHERE recipient=$1 ORDER BY created_at DESC LIMIT 1`, recipient).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	workers := []*email.Worker{
		email.NewWorker(db, email.FileSender{Dir: dir}, "no-reply@example.test", []byte(testHashKey), logger),
		email.NewWorker(db, email.FileSender{Dir: dir}, "no-reply@example.test", []byte(testHashKey), logger),
	}
	done := make(chan error, len(workers))
	for _, worker := range workers {
		go func(worker *email.Worker) { done <- worker.Run(ctx) }(worker)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status string
		if err := db.QueryRow(t.Context(), `SELECT status FROM email_outbox WHERE id=$1`, outboxID).Scan(&status); err != nil {
			cancel()
			t.Fatal(err)
		}
		if status == "sent" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("email outbox was not delivered")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	for range workers {
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	var deliveries int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM email_deliveries WHERE outbox_id=$1 AND success`, outboxID).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("deliveries=%d err=%v", deliveries, err)
	}
}

func TestMultilingualArchitectureMatrix(t *testing.T) {
	db := database(t)
	authSvc, stores, socialSvc, searchSvc, report := services(t, db, googleStub{}, nil)

	// The OTP request locale is persisted independently from the worker context.
	for _, locale := range i18n.Supported() {
		email := "otp-" + string(locale) + "-" + uuid.NewString() + "@example.test"
		visitor := uuid.New()
		ctx := i18n.WithLocale(t.Context(), locale)
		if err := authSvc.RequestCode(ctx, email, &visitor, nil); err != nil {
			t.Fatalf("request code %s: %v", locale, err)
		}
		var verificationLocale, outboxLocale, visitorLocale string
		if err := db.QueryRow(ctx, `SELECT v.locale::text,o.locale::text,s.locale::text FROM email_verification_codes v JOIN email_outbox o ON o.idempotency_key='otp:'||v.id::text JOIN visitor_sessions s ON s.id=v.visitor_session_id WHERE v.normalized_email=$1 ORDER BY v.created_at DESC LIMIT 1`, email).Scan(&verificationLocale, &outboxLocale, &visitorLocale); err != nil {
			t.Fatal(err)
		}
		if verificationLocale != string(locale) || outboxLocale != string(locale) || visitorLocale != string(locale) {
			t.Fatalf("locale=%s verification=%s outbox=%s visitor=%s", locale, verificationLocale, outboxLocale, visitorLocale)
		}
	}

	// Signup takes the persisted verification locale; changing preference affects
	// the very next authenticated request without refreshing the token.
	email := "locale-user-" + uuid.NewString() + "@example.test"
	code := "654321"
	if _, err := db.Exec(t.Context(), `INSERT INTO email_verification_codes(normalized_email,code_hash,max_attempts,expires_at,locale) VALUES($1,$2,5,now()+interval '10 minutes','de')`, email, security.Hash([]byte(testHashKey), code)); err != nil {
		t.Fatal(err)
	}
	pair, err := authSvc.VerifyCode(i18n.WithLocale(t.Context(), i18n.LocaleTR), email, code, auth.Client{Type: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	users := userpkg.NewService(db, report)
	me, err := users.Me(t.Context(), pair.UserID)
	if err != nil || me.PreferredLocale != "de" {
		t.Fatalf("signup locale=%q err=%v", me.PreferredLocale, err)
	}
	ru := "ru"
	bioLanguage := "ru-RU"
	bio := "Люблю современный интерьер"
	if err = users.Update(t.Context(), pair.UserID, userpkg.Update{PreferredLocale: &ru, BioLanguage: &bioLanguage, Bio: &bio}); err != nil {
		t.Fatal(err)
	}
	tokens := security.NewTokenManager("integration-access-secret-more-than-32-bytes", 15*time.Minute, 24*time.Hour)
	resolved := func(explicit string) string {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		request.Header.Set("Accept-Language", "en-US")
		if explicit != "" {
			request.Header.Set("X-Locale", explicit)
		}
		recorder := httptest.NewRecorder()
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(i18n.FromContext(r.Context()))) })
		middleware.RequestLocale(i18n.LocaleTR)(middleware.OptionalAuth(tokens)(middleware.UserLocale(db)(handler))).ServeHTTP(recorder, request)
		return recorder.Body.String()
	}
	if got := resolved(""); got != "ru" {
		t.Fatalf("updated authenticated locale=%q", got)
	}
	if got := resolved("de-DE"); got != "de" {
		t.Fatalf("explicit locale override=%q", got)
	}

	// Canonical category keys stay stable while labels vary by presentation locale.
	storeID := store(t, db, 41, 29)
	if _, err = db.Exec(t.Context(), `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug='curtain' ON CONFLICT DO NOTHING`, storeID); err != nil {
		t.Fatal(err)
	}
	wantLabels := map[i18n.Locale]string{i18n.LocaleTR: "Perde", i18n.LocaleEN: "Curtains", i18n.LocaleDE: "Gardinen", i18n.LocaleRU: "Шторы"}
	for locale, want := range wantLabels {
		item, err := stores.Get(i18n.WithLocale(t.Context(), locale), storeID, nil, nil, nil)
		if err != nil || len(item.Categories) != 1 || item.Categories[0] != "curtain" || len(item.CategoryLabels) != 1 || item.CategoryLabels[0] != want {
			t.Fatalf("locale=%s item=%+v err=%v", locale, item, err)
		}
	}
	var translatedCounts []int
	rows, err := db.Query(t.Context(), `SELECT count(*) FROM store_category_translations GROUP BY locale ORDER BY locale`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var count int
		if err = rows.Scan(&count); err != nil {
			t.Fatal(err)
		}
		translatedCounts = append(translatedCounts, count)
	}
	rows.Close()
	if len(translatedCounts) != 4 {
		t.Fatalf("translation locale counts=%v", translatedCounts)
	}
	for _, count := range translatedCounts {
		if count != 13 {
			t.Fatalf("expected 13 taxonomy translations per locale, counts=%v", translatedCounts)
		}
	}

	// User content is byte-for-byte preserved and only annotated with language.
	russianPost := "Очень хороший магазин штор"
	russian := "ru"
	postID, err := socialSvc.CreatePost(t.Context(), pair.UserID, social.CreatePost{StoreID: storeID, Text: russianPost, Rating: 5, Latitude: 41, Longitude: 29, ContentLanguage: &russian})
	if err != nil {
		t.Fatal(err)
	}
	commentText := "Полностью согласна"
	if _, err = socialSvc.AddCommentLocalized(t.Context(), pair.UserID, postID, commentText, &russian); err != nil {
		t.Fatal(err)
	}
	post, err := socialSvc.GetPost(t.Context(), postID, &pair.UserID)
	comments, commentsErr := socialSvc.Comments(t.Context(), postID, 10)
	if err != nil || commentsErr != nil || post.Text != russianPost || post.ContentLanguage != "ru" || len(comments) != 1 || comments[0].Body != commentText || comments[0].ContentLanguage != "ru" {
		t.Fatalf("post=%+v comments=%+v err=%v commentsErr=%v", post, comments, err, commentsErr)
	}

	// Equivalent Unicode queries persist their detected language and canonical intent.
	queries := map[i18n.Locale]string{
		i18n.LocaleTR: "Antalya'da uygun fiyatlı perde mağazası",
		i18n.LocaleEN: "affordable curtain stores in Antalya",
		i18n.LocaleDE: "günstige Gardinengeschäfte in Antalya",
		i18n.LocaleRU: "недорогие магазины штор в Анталии",
	}
	for locale, query := range queries {
		searchID, _, err := searchSvc.RecordInternalSearch(i18n.WithLocale(t.Context(), locale), &pair.UserID, nil, search.Request{Query: query}, nil, time.Millisecond)
		if err != nil {
			t.Fatalf("record search %s: %v", locale, err)
		}
		var raw, language string
		var categories []string
		if err = db.QueryRow(t.Context(), `SELECT raw_query,query_language::text,ARRAY(SELECT jsonb_array_elements_text(parsed_intent->'categories')) FROM searches WHERE id=$1`, searchID).Scan(&raw, &language, &categories); err != nil {
			t.Fatal(err)
		}
		if raw != query || language != string(locale) || !containsAll(categories, "curtain", "home_textile") {
			t.Fatalf("locale=%s raw=%q language=%q categories=%v", locale, raw, language, categories)
		}
	}
	history, err := users.Searches(t.Context(), pair.UserID, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundCyrillic := false
	for _, entry := range history {
		foundCyrillic = foundCyrillic || strings.Contains(entry.RawQuery, "штор")
	}
	if !foundCyrillic {
		t.Fatalf("Cyrillic query missing from history: %+v", history)
	}
	if err = report.AggregateDay(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	today := time.Now()
	localizedCategories, err := report.GetTopSearchDimensionsByLocale(t.Context(), today, today, "category", "ru", 20)
	if err != nil || len(localizedCategories) == 0 || localizedCategories[0].QueryLanguage != "ru" {
		t.Fatalf("localized category metrics=%+v err=%v", localizedCategories, err)
	}
	localizedQueries, err := report.GetTopSearchQueriesByLocale(t.Context(), today, today, "ru", 20, false)
	if err != nil || len(localizedQueries) == 0 || localizedQueries[0].QueryLanguage != "ru" {
		t.Fatalf("localized query metrics=%+v err=%v", localizedQueries, err)
	}
	var localeMetrics int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM locale_daily_metrics WHERE metric_date=(now() AT TIME ZONE 'Europe/Istanbul')::date AND dimension='search_query' AND locale IN ('tr','en','de','ru')`).Scan(&localeMetrics); err != nil || localeMetrics != 4 {
		t.Fatalf("search locale metrics=%d err=%v", localeMetrics, err)
	}
}

func TestPrivateDiscoveryLocationLifecycle(t *testing.T) {
	db := database(t)
	_, _, _, _, report := services(t, db, googleStub{}, nil)
	userID := user(t, db, "location-"+uuid.NewString()+"@example.test")
	users := userpkg.NewService(db, report)

	manual := userpkg.DiscoveryLocationInput{Source: "manual", Label: "Kadıköy", Address: "Kadıköy, İstanbul", PlaceID: "kadikoy-place", Latitude: 40.9917, Longitude: 29.0277}
	if err := users.SetDiscoveryLocation(t.Context(), userID, manual); err != nil {
		t.Fatal(err)
	}
	me, err := users.Me(t.Context(), userID)
	if err != nil || me.DiscoveryLocation == nil || me.DiscoveryLocation.Source != "manual" || me.DiscoveryLocation.Label != "Kadıköy" {
		t.Fatalf("manual location=%+v err=%v", me.DiscoveryLocation, err)
	}

	accuracy := 20.0
	device := userpkg.DiscoveryLocationInput{Source: "device", Latitude: 41.05, Longitude: 29.10, AccuracyMeters: &accuracy}
	if err = users.SetDiscoveryLocation(t.Context(), userID, device); err != nil {
		t.Fatal(err)
	}
	me, err = users.Me(t.Context(), userID)
	if err != nil || me.DiscoveryLocation == nil || me.DiscoveryLocation.Source != "manual" {
		t.Fatalf("ordinary device update overwrote manual location: %+v err=%v", me.DiscoveryLocation, err)
	}

	device.OverrideManual = true
	if err = users.SetDiscoveryLocation(t.Context(), userID, device); err != nil {
		t.Fatal(err)
	}
	me, err = users.Me(t.Context(), userID)
	if err != nil || me.DiscoveryLocation == nil || me.DiscoveryLocation.Source != "device" || me.DiscoveryLocation.AccuracyMeters == nil {
		t.Fatalf("device location=%+v err=%v", me.DiscoveryLocation, err)
	}

	if err = users.ClearDiscoveryLocation(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	me, err = users.Me(t.Context(), userID)
	if err != nil || me.DiscoveryLocation != nil {
		t.Fatalf("cleared location=%+v err=%v", me.DiscoveryLocation, err)
	}
}

func containsAll(values []string, wanted ...string) bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	for _, value := range wanted {
		if !set[value] {
			return false
		}
	}
	return true
}
