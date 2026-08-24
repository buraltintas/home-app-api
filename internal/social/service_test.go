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
	_, err := (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: "Geçerli yorum", Rating: 5, VisitVerificationID: &proofID, MediaIDs: []uuid.UUID{mediaID, mediaID}})
	app, ok := err.(*httpapi.Error)
	if !ok || app.Code != "DUPLICATE_MEDIA" {
		t.Fatalf("stored visit did not pass location contract: %v", err)
	}
}
