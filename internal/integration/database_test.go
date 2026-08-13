//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/auth"
	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/media"
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
	socialSvc := social.NewService(db, 500, report)
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
	if !feed[0].ViewerLiked || !feed[0].ViewerFollows || !feed[0].ViewerFavorited || !feed[0].VisitVerified {
		t.Fatalf("incorrect viewer state: %+v", feed[0])
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
		go func() {
			defer wg.Done()
			id, err := searchSvc.MaterializeGoogleStore(context.Background(), placeID)
			ids <- id
			errs <- err
		}()
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
	if len(post.Media) != 1 || post.Media[0] != created.Upload.StorageKey {
		t.Fatalf("post media=%v", post.Media)
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
	deviceA, err := repo.RegisterDevice(t.Context(), first, "ios", "shared-device-token")
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := repo.RegisterDevice(t.Context(), second, "android", "shared-device-token")
	if err != nil {
		t.Fatal(err)
	}
	if deviceA != deviceB {
		t.Fatalf("token replacement created a second device: %s != %s", deviceA, deviceB)
	}
	deleted, err := repo.DeleteDevice(t.Context(), first, "shared-device-token")
	if err != nil || deleted {
		t.Fatalf("old owner deleted replacement device: deleted=%v err=%v", deleted, err)
	}
	if err = repo.SetPreferences(t.Context(), second, map[string]bool{"social": true, "marketing": false}); err != nil {
		t.Fatal(err)
	}
	created, err := repo.Enqueue(t.Context(), second, "social.like", "push-"+deviceA.String(), map[string]any{"post_id": uuid.NewString()})
	if err != nil || !created {
		t.Fatalf("enqueue created=%v err=%v", created, err)
	}
	created, err = repo.Enqueue(t.Context(), second, "social.like", "push-"+deviceA.String(), map[string]any{})
	if err != nil || created {
		t.Fatalf("duplicate enqueue created=%v err=%v", created, err)
	}
	job, ok, err := repo.Claim(t.Context())
	if err != nil || !ok || job.UserID != second {
		t.Fatalf("claim ok=%v job=%+v err=%v", ok, job, err)
	}
	if err = repo.Complete(t.Context(), job.ID, "provider-123"); err != nil {
		t.Fatal(err)
	}
}
