package social

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/google/uuid"
)

func TestCreatePostRejectsDuplicateMediaBeforeDatabaseWrite(t *testing.T) {
	id := uuid.New()
	_, err := (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: "Geçerli yorum", Rating: 5, Latitude: 41, Longitude: 29, MediaIDs: []uuid.UUID{id, id}})
	if err == nil {
		t.Fatal("duplicate media accepted")
	}
}

func TestCreatePostRequiresMobileHorizontalAccuracy(t *testing.T) {
	_, err := (&Service{cfg: Config{MaxLocationAccuracyMeters: 100}}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: "Geçerli yorum", Rating: 5, Latitude: 41, Longitude: 29})
	if err == nil {
		t.Fatal("current location without horizontal accuracy accepted")
	}
}

func TestFeedRejectsCursorModeMismatchBeforeDatabaseRead(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"m": "recent", "t": time.Now(), "id": uuid.New()})
	cursor := base64.RawURLEncoding.EncodeToString(raw)
	lat, lon := 41.0, 29.0
	if _, _, err := (&Service{}).Feed(context.Background(), nil, cursor, 20, FeedContext{Latitude: &lat, Longitude: &lon}); err == nil {
		t.Fatal("recent cursor accepted for nearby feed")
	}
	if _, _, err := (&Service{}).Feed(context.Background(), nil, "", 20, FeedContext{Latitude: &lat}); err == nil {
		t.Fatal("unpaired feed coordinates accepted")
	}
}

func TestSocialTextLimitsCountUnicodeCharactersBeforeDatabaseWrite(t *testing.T) {
	if _, err := (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: strings.Repeat("ä", 5001), Rating: 5, Latitude: 41, Longitude: 29}); err == nil {
		t.Fatal("5001-character post accepted")
	}
	if _, err := (&Service{}).AddComment(context.Background(), uuid.New(), uuid.New(), strings.Repeat("Ж", 2001)); err == nil {
		t.Fatal("2001-character comment accepted")
	}
}

func TestFollowRejectsSelfBeforeDatabaseWrite(t *testing.T) {
	id := uuid.New()
	if err := (&Service{}).Follow(context.Background(), id, id, true); err == nil {
		t.Fatal("self follow accepted")
	}
}

func TestVisitVerificationRejectsUnusableLocationBeforeDatabaseWrite(t *testing.T) {
	svc := &Service{cfg: Config{MaxLocationAccuracyMeters: 100}}
	if _, err := svc.VerifyVisit(context.Background(), uuid.New(), uuid.New(), 41, 29, 0); err == nil {
		t.Fatal("zero accuracy accepted")
	}
	if _, err := svc.VerifyVisit(context.Background(), uuid.New(), uuid.New(), 41, 29, 101); err == nil {
		t.Fatal("inaccurate location accepted")
	} else if app, ok := err.(*httpapi.Error); !ok || app.Code != "LOCATION_ACCURACY_TOO_LOW" {
		t.Fatalf("inaccurate location returned the wrong error: %v", err)
	}
}

func TestCreatePostAcceptsStoredVisitContractWithoutCurrentCoordinates(t *testing.T) {
	proofID := uuid.New()
	mediaID := uuid.New()
	criteria := ReviewCriteria{Availability: 5, Value: 4, Layout: 5, StaffCare: 4, StaffKnowledge: 5, Checkout: 4, Returns: 5, Cleanliness: 4}
	_, err := (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Criteria: &criteria, VisitVerificationID: &proofID, MediaIDs: []uuid.UUID{mediaID, mediaID}})
	app, ok := err.(*httpapi.Error)
	if !ok || app.Code != "DUPLICATE_MEDIA" {
		t.Fatalf("stored visit did not pass location contract: %v", err)
	}
}

func TestDecodeStorePhotoPreservesEffectiveSource(t *testing.T) {
	mediaID := uuid.NewString()
	photo, err := decodeStorePhoto([]byte(`{"source":"admin","media_id":"` + mediaID + `"}`))
	if err != nil || photo == nil || photo.Source != "admin" || photo.MediaID != mediaID {
		t.Fatalf("admin cover did not survive feed decoding: photo=%+v err=%v", photo, err)
	}
	photo, err = decodeStorePhoto([]byte(`{"source":"google","name":"places/a/photos/b","attributions":["A"]}`))
	if err != nil || photo == nil || photo.Source != "google" || photo.Name != "places/a/photos/b" || len(photo.Attributions) != 1 {
		t.Fatalf("Google fallback did not survive feed decoding: photo=%+v err=%v", photo, err)
	}
	photo, err = decodeStorePhoto(nil)
	if err != nil || photo != nil {
		t.Fatalf("missing cover should remain absent: photo=%+v err=%v", photo, err)
	}
}

// The overall rating is derived, so it has to be derived correctly at the edges: eight
// identical scores return that score, and a half-way total rounds up rather than sitting
// between two stars.
func TestReviewCriteriaOverall(t *testing.T) {
	for score := 1; score <= 5; score++ {
		c := ReviewCriteria{score, score, score, score, score, score, score, score}
		if got := c.overall(); got != score {
			t.Fatalf("eight %ds averaged to %d", score, got)
		}
	}
	// 4,4,4,4,5,5,5,5 -> 36/8 = 4.5, which rounds to 5.
	if got := (ReviewCriteria{4, 4, 4, 4, 5, 5, 5, 5}).overall(); got != 5 {
		t.Fatalf("4.5 rounded to %d", got)
	}
	// 4,4,4,4,4,4,4,5 -> 33/8 = 4.125, which rounds to 4.
	if got := (ReviewCriteria{4, 4, 4, 4, 4, 4, 4, 5}).overall(); got != 4 {
		t.Fatalf("4.125 rounded to %d", got)
	}
	// A missing criterion is not a shorter review.
	if (ReviewCriteria{5, 5, 5, 5, 5, 5, 5, 0}).valid() {
		t.Fatal("a criterion left unanswered was accepted")
	}
}

// The phone app still posts a paragraph and one star. It keeps working, and its reviews
// arrive without the eight rather than being refused.
func TestCreatePostStillAcceptsTheOlderContract(t *testing.T) {
	proofID := uuid.New()
	mediaID := uuid.New()
	_, err := (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: "Geçerli yorum", Rating: 5, VisitVerificationID: &proofID, MediaIDs: []uuid.UUID{mediaID, mediaID}})
	if app, ok := err.(*httpapi.Error); !ok || app.Code != "DUPLICATE_MEDIA" {
		t.Fatalf("the older contract was refused: %v", err)
	}
	// Half the criteria is not a third contract.
	half := ReviewCriteria{Availability: 5, Value: 4}
	_, err = (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Criteria: &half, VisitVerificationID: &proofID})
	if app, ok := err.(*httpapi.Error); !ok || app.Code != "REVIEW_CRITERIA_INCOMPLETE" {
		t.Fatalf("an incomplete review was not refused: %v", err)
	}
}
