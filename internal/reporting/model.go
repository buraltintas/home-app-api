package reporting

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	UserRegistered        = "user_registered"
	UserLoginSucceeded    = "user_login_succeeded"
	UserLoginFailed       = "user_login_failed"
	OTPRequested          = "otp_requested"
	OTPVerificationFailed = "otp_verification_failed"
	StoreCreated          = "store_created"
	StoreImportedGoogle   = "store_imported_from_google"
	PostCreated           = "post_created"
	PostDeleted           = "post_deleted"
	PostVisitVerified     = "post_visit_verified"
	PostLocationRejected  = "post_location_rejected"
	FavoriteCreated       = "favorite_created"
	FavoriteRemoved       = "favorite_removed"
	FollowCreated         = "follow_created"
	FollowRemoved         = "follow_removed"
	LikeCreated           = "like_created"
	LikeRemoved           = "like_removed"
	CommentCreated        = "comment_created"
	CommentDeleted        = "comment_deleted"
	SearchPerformed       = "search_performed"
	MediaCreated          = "media_created"
	MediaDeleted          = "media_deleted"
)

type Event struct {
	Type, IdempotencyKey                                string
	UserID, VisitorSessionID, StoreID, PostID, SearchID *uuid.UUID
	Metadata                                            map[string]any
	CreatedAt                                           time.Time
}
type Snapshot struct {
	RegisteredUsersTotal, StoresTotal, GoogleImportedStoresTotal, PostsCurrentTotal, PostsCreatedLifetime, PostsDeletedLifetime, CommentsCurrentTotal, LikesCurrentTotal, FollowsCurrentTotal, FavoritesCurrentTotal, SearchesLifetime, MediaCurrentTotal int64
	UpdatedAt                                                                                                                                                                                                                                             time.Time
}
type DailyMetrics struct {
	Date                                                                                                                                                                                                                                                                                                                                                                                             time.Time `json:"date"`
	NewUsers, ActiveUsers, AnonymousVisitors, NewStores, NewPosts, VerifiedPosts, LocationRejected, NewComments, NewLikes, NewFollows, NewFavorites, Searches, SearchesWithResults, ZeroResultSearches, AuthenticatedSearches, AnonymousSearches, AISearches, GooglePlacesSearches, Impressions, Clicks, StoreOpens, FavoritesFromSearch, ReviewsFromSearch, OTPRequests, SuccessfulAuth, FailedAuth int64
}
type SearchOverview struct{ Searches, SearchesWithResults, ZeroResultSearches, AuthenticatedSearches, AnonymousSearches, AISearches, GooglePlacesSearches, Impressions, Clicks, StoreOpens, Favorites, Reviews int64 }
type QueryMetric struct {
	NormalizedQuery                                                                                                                        string
	SearchCount, UniqueUserCount, UniqueVisitorCount, ResultCountTotal, ZeroResultCount, ClickCount, OpenCount, FavoriteCount, ReviewCount int64
}
type DimensionMetric struct {
	Dimension, Value string
	SearchCount      int64
}
type StoreSearchMetric struct {
	ResultKey                                      string
	StoreID                                        *uuid.UUID
	ExternalProvider, ExternalPlaceID              *string
	Impressions, Clicks, Opens, Favorites, Reviews int64
	PlatformReviewCount                            *int
}

func metadata(v map[string]any) []byte {
	if v == nil {
		return []byte(`{}`)
	}
	b, _ := json.Marshal(v)
	return b
}
