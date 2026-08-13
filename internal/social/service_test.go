package social

import (
	"context"
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

func TestFollowRejectsSelfBeforeDatabaseWrite(t *testing.T) {
	id := uuid.New()
	if err := (&Service{}).Follow(context.Background(), id, id, true); err == nil {
		t.Fatal("self follow accepted")
	}
}
