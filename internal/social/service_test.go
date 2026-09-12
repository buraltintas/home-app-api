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

func TestCreatePostAcceptsCoordinatesThatCannotProveAVisit(t *testing.T) {
	// Being near the shop earns the badge; it is no longer the price of writing at all.
	// A reading with no stated accuracy cannot prove anything, and that is a reason to
	// withhold the badge rather than to refuse the review -- so this gets past validation
	// and fails later, on the nil database this fixture has instead of one.
	defer func() { _ = recover() }()
	_, err := (&Service{cfg: Config{MaxLocationAccuracyMeters: 100}}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: "Geçerli yorum", Rating: 5, Latitude: 41, Longitude: 29})
	if err == httpapi.ErrInvalidInput {
		t.Fatal("a location that cannot prove a visit should not refuse the review")
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

// The overall rating is the average of the eight, kept as an average. It used to round to a
// whole number, which threw away what the criteria were for: a reader who adds their own
// answers up gets 1.25 and was shown 1.0.
func TestReviewCriteriaOverall(t *testing.T) {
	for score := 1; score <= 5; score++ {
		c := ReviewCriteria{score, score, score, score, score, score, score, score}
		if got := c.overall(); got != float64(score) {
			t.Fatalf("eight %ds averaged to %v", score, got)
		}
	}
	// The case that was reported: ten stars spread over eight answers.
	if got := (ReviewCriteria{1, 1, 1, 1, 1, 1, 2, 2}).overall(); got != 1.25 {
		t.Fatalf("ten stars over eight criteria came to %v, want 1.25", got)
	}
	// 4,4,4,4,5,5,5,5 -> 36/8 = 4.5, and stays 4.5.
	if got := (ReviewCriteria{4, 4, 4, 4, 5, 5, 5, 5}).overall(); got != 4.5 {
		t.Fatalf("4.5 became %v", got)
	}
	// 33/8 = 4.125, which two decimals name as 4.13 -- eighths need no more than that.
	if got := (ReviewCriteria{4, 4, 4, 4, 4, 4, 4, 5}).overall(); got != 4.13 {
		t.Fatalf("4.125 became %v", got)
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
