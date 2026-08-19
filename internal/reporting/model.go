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
	StoreVisitVerified    = "store_visit_verified"
	StoreVisitRejected    = "store_visit_rejected"
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

// These cross the wire to the admin panel, so every field states its own name. Without a
// tag Go marshals the field name verbatim and the panel received RegisteredUsersTotal and
// NormalizedQuery, which no reader expects and no client had matched: the query tables
// rendered a column of dashes over data that was there all along.
type Snapshot struct {
	RegisteredUsersTotal      int64     `json:"registered_users_total"`
	StoresTotal               int64     `json:"stores_total"`
	GoogleImportedStoresTotal int64     `json:"google_imported_stores_total"`
	PostsCurrentTotal         int64     `json:"posts_current_total"`
	PostsCreatedLifetime      int64     `json:"posts_created_lifetime"`
	PostsDeletedLifetime      int64     `json:"posts_deleted_lifetime"`
	CommentsCurrentTotal      int64     `json:"comments_current_total"`
	LikesCurrentTotal         int64     `json:"likes_current_total"`
	FollowsCurrentTotal       int64     `json:"follows_current_total"`
	FavoritesCurrentTotal     int64     `json:"favorites_current_total"`
	SearchesLifetime          int64     `json:"searches_lifetime"`
	MediaCurrentTotal         int64     `json:"media_current_total"`
	UpdatedAt                 time.Time `json:"updated_at"`
}
type DailyMetrics struct {
	Date                  time.Time `json:"date"`
	NewUsers              int64     `json:"new_users"`
	ActiveUsers           int64     `json:"active_users"`
	AnonymousVisitors     int64     `json:"anonymous_visitors"`
	NewStores             int64     `json:"new_stores"`
	NewPosts              int64     `json:"new_posts"`
	VerifiedPosts         int64     `json:"verified_posts"`
	LocationRejected      int64     `json:"location_rejected"`
	NewComments           int64     `json:"new_comments"`
	NewLikes              int64     `json:"new_likes"`
	NewFollows            int64     `json:"new_follows"`
	NewFavorites          int64     `json:"new_favorites"`
	Searches              int64     `json:"searches"`
	SearchesWithResults   int64     `json:"searches_with_results"`
	ZeroResultSearches    int64     `json:"zero_result_searches"`
	AuthenticatedSearches int64     `json:"authenticated_searches"`
	AnonymousSearches     int64     `json:"anonymous_searches"`
	AISearches            int64     `json:"ai_searches"`
	GooglePlacesSearches  int64     `json:"google_places_searches"`
	Impressions           int64     `json:"impressions"`
	Clicks                int64     `json:"clicks"`
	StoreOpens            int64     `json:"store_opens"`
	FavoritesFromSearch   int64     `json:"favorites_from_search"`
	ReviewsFromSearch     int64     `json:"reviews_from_search"`
	OTPRequests           int64     `json:"otp_requests"`
	SuccessfulAuth        int64     `json:"successful_auth"`
	FailedAuth            int64     `json:"failed_auth"`
}
type SearchOverview struct {
	Searches              int64 `json:"searches"`
	SearchesWithResults   int64 `json:"searches_with_results"`
	ZeroResultSearches    int64 `json:"zero_result_searches"`
	AuthenticatedSearches int64 `json:"authenticated_searches"`
	AnonymousSearches     int64 `json:"anonymous_searches"`
	AISearches            int64 `json:"ai_searches"`
	GooglePlacesSearches  int64 `json:"google_places_searches"`
	Impressions           int64 `json:"impressions"`
	Clicks                int64 `json:"clicks"`
	StoreOpens            int64 `json:"store_opens"`
	Favorites             int64 `json:"favorites"`
	Reviews               int64 `json:"reviews"`
}
type SearchFunnel struct {
	Searches            int64 `json:"searches"`
	SearchesWithResults int64 `json:"searches_with_results"`
	StoreOpens          int64 `json:"store_opens"`
	Favorites           int64 `json:"favorites"`
	Reviews             int64 `json:"reviews"`
}
type QueryMetric struct {
	NormalizedQuery    string `json:"normalized_query"`
	QueryLanguage      string `json:"query_language"`
	SearchCount        int64  `json:"search_count"`
	UniqueUserCount    int64  `json:"unique_user_count"`
	UniqueVisitorCount int64  `json:"unique_visitor_count"`
	ResultCountTotal   int64  `json:"result_count_total"`
	ZeroResultCount    int64  `json:"zero_result_count"`
	ClickCount         int64  `json:"click_count"`
	OpenCount          int64  `json:"open_count"`
	FavoriteCount      int64  `json:"favorite_count"`
	ReviewCount        int64  `json:"review_count"`
}
type DimensionMetric struct {
	Dimension     string `json:"dimension"`
	Value         string `json:"value"`
	QueryLanguage string `json:"query_language"`
	SearchCount   int64  `json:"search_count"`
}
type StoreSearchMetric struct {
	ResultKey           string     `json:"result_key"`
	StoreID             *uuid.UUID `json:"store_id,omitempty"`
	ExternalProvider    *string    `json:"external_provider,omitempty"`
	ExternalPlaceID     *string    `json:"external_place_id,omitempty"`
	Impressions         int64      `json:"impressions"`
	Clicks              int64      `json:"clicks"`
	Opens               int64      `json:"opens"`
	Favorites           int64      `json:"favorites"`
	Reviews             int64      `json:"reviews"`
	PlatformReviewCount *int       `json:"platform_review_count,omitempty"`
}

func metadata(v map[string]any) []byte {
	if v == nil {
		return []byte(`{}`)
	}
	b, _ := json.Marshal(v)
	return b
}
