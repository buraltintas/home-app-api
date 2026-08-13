package social

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCreatePostRejectsDuplicateMediaBeforeDatabaseWrite(t *testing.T) {
	id := uuid.New()
	_, err := (&Service{}).CreatePost(context.Background(), uuid.New(), CreatePost{StoreID: uuid.New(), Text: "Geçerli yorum", Rating: 5, Latitude: 41, Longitude: 29, MediaIDs: []uuid.UUID{id, id}})
	if err == nil {
		t.Fatal("duplicate media accepted")
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
