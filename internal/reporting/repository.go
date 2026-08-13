package reporting

import (
	"context"
	"fmt"
	"time"
)

func (s *Service) GetPlatformSnapshot(ctx context.Context) (Snapshot, error) {
	var x Snapshot
	e := s.db.QueryRow(ctx, `SELECT registered_users_total,stores_total,google_imported_stores_total,posts_current_total,posts_created_lifetime,posts_deleted_lifetime,comments_current_total,likes_current_total,follows_current_total,favorites_current_total,searches_lifetime,media_current_total,updated_at FROM platform_stats WHERE id=1`).Scan(&x.RegisteredUsersTotal, &x.StoresTotal, &x.GoogleImportedStoresTotal, &x.PostsCurrentTotal, &x.PostsCreatedLifetime, &x.PostsDeletedLifetime, &x.CommentsCurrentTotal, &x.LikesCurrentTotal, &x.FollowsCurrentTotal, &x.FavoritesCurrentTotal, &x.SearchesLifetime, &x.MediaCurrentTotal, &x.UpdatedAt)
	return x, e
}
func (s *Service) GetDailyMetrics(ctx context.Context, from, to time.Time) ([]DailyMetrics, error) {
	rows, e := s.db.Query(ctx, `SELECT metric_date,new_users_count,active_users_count,anonymous_visitors_count,new_stores_count,new_posts_count,verified_posts_count,location_rejected_post_attempts,new_comments_count,new_likes_count,new_follows_count,new_favorites_count,searches_count,searches_with_results_count,zero_result_searches_count,authenticated_searches_count,anonymous_searches_count,ai_searches_count,google_places_searches_count,search_result_impressions_count,search_result_clicks_count,store_opens_from_search_count,favorites_from_search_count,reviews_from_search_count,otp_requests_count,successful_auth_count,failed_auth_count FROM platform_daily_metrics WHERE metric_date BETWEEN $1 AND $2 ORDER BY metric_date`, from, to)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []DailyMetrics
	for rows.Next() {
		var x DailyMetrics
		if e = rows.Scan(&x.Date, &x.NewUsers, &x.ActiveUsers, &x.AnonymousVisitors, &x.NewStores, &x.NewPosts, &x.VerifiedPosts, &x.LocationRejected, &x.NewComments, &x.NewLikes, &x.NewFollows, &x.NewFavorites, &x.Searches, &x.SearchesWithResults, &x.ZeroResultSearches, &x.AuthenticatedSearches, &x.AnonymousSearches, &x.AISearches, &x.GooglePlacesSearches, &x.Impressions, &x.Clicks, &x.StoreOpens, &x.FavoritesFromSearch, &x.ReviewsFromSearch, &x.OTPRequests, &x.SuccessfulAuth, &x.FailedAuth); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) GetSearchOverview(ctx context.Context, from, to time.Time) (SearchOverview, error) {
	var x SearchOverview
	e := s.db.QueryRow(ctx, `SELECT coalesce(sum(searches_count),0),coalesce(sum(searches_with_results_count),0),coalesce(sum(zero_result_searches_count),0),coalesce(sum(authenticated_searches_count),0),coalesce(sum(anonymous_searches_count),0),coalesce(sum(ai_searches_count),0),coalesce(sum(google_places_searches_count),0),coalesce(sum(search_result_impressions_count),0),coalesce(sum(search_result_clicks_count),0),coalesce(sum(store_opens_from_search_count),0),coalesce(sum(favorites_from_search_count),0),coalesce(sum(reviews_from_search_count),0) FROM platform_daily_metrics WHERE metric_date BETWEEN $1 AND $2`, from, to).Scan(&x.Searches, &x.SearchesWithResults, &x.ZeroResultSearches, &x.AuthenticatedSearches, &x.AnonymousSearches, &x.AISearches, &x.GooglePlacesSearches, &x.Impressions, &x.Clicks, &x.StoreOpens, &x.Favorites, &x.Reviews)
	return x, e
}
func (s *Service) GetTopSearchQueries(ctx context.Context, from, to time.Time, limit int, zeroOnly bool) ([]QueryMetric, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	filter := ""
	if zeroOnly {
		filter = " AND zero_result_count>0"
	}
	rows, e := s.db.Query(ctx, `SELECT normalized_query,sum(search_count),sum(unique_user_count),sum(unique_visitor_count),sum(result_count_total),sum(zero_result_count),sum(result_click_count),sum(store_open_count),sum(favorite_count),sum(review_count) FROM search_query_daily_metrics WHERE metric_date BETWEEN $1 AND $2`+filter+` GROUP BY normalized_query ORDER BY `+map[bool]string{true: "sum(zero_result_count)", false: "sum(search_count)"}[zeroOnly]+` DESC LIMIT $3`, from, to, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []QueryMetric
	for rows.Next() {
		var x QueryMetric
		if e = rows.Scan(&x.NormalizedQuery, &x.SearchCount, &x.UniqueUserCount, &x.UniqueVisitorCount, &x.ResultCountTotal, &x.ZeroResultCount, &x.ClickCount, &x.OpenCount, &x.FavoriteCount, &x.ReviewCount); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) GetZeroResultQueries(ctx context.Context, from, to time.Time, limit int) ([]QueryMetric, error) {
	return s.GetTopSearchQueries(ctx, from, to, limit, true)
}
func (s *Service) GetTopSearchCategories(ctx context.Context, from, to time.Time, limit int) ([]DimensionMetric, error) {
	return s.GetTopSearchDimensions(ctx, from, to, "category", limit)
}
func (s *Service) GetTopSearchLocations(ctx context.Context, from, to time.Time, limit int) ([]DimensionMetric, error) {
	return s.GetTopSearchDimensions(ctx, from, to, "location", limit)
}
func (s *Service) GetSearchConversionFunnel(ctx context.Context, from, to time.Time) (SearchFunnel, error) {
	var x SearchFunnel
	e := s.db.QueryRow(ctx, `SELECT coalesce(sum(searches_count),0),coalesce(sum(searches_with_results_count),0),coalesce(sum(store_opens_from_search_count),0),coalesce(sum(favorites_from_search_count),0),coalesce(sum(reviews_from_search_count),0) FROM platform_daily_metrics WHERE metric_date BETWEEN $1 AND $2`, from, to).Scan(&x.Searches, &x.SearchesWithResults, &x.StoreOpens, &x.Favorites, &x.Reviews)
	return x, e
}
func (s *Service) GetTopSearchDimensions(ctx context.Context, from, to time.Time, dimension string, limit int) ([]DimensionMetric, error) {
	allowed := map[string]bool{"category": true, "product": true, "style": true, "location": true, "price_intent": true}
	if !allowed[dimension] {
		return nil, fmt.Errorf("invalid dimension")
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, e := s.db.Query(ctx, `SELECT dimension,value,sum(search_count) FROM search_intent_daily_metrics WHERE metric_date BETWEEN $1 AND $2 AND dimension=$3 GROUP BY dimension,value ORDER BY sum(search_count) DESC LIMIT $4`, from, to, dimension, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []DimensionMetric
	for rows.Next() {
		var x DimensionMetric
		if e = rows.Scan(&x.Dimension, &x.Value, &x.SearchCount); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) GetHighDemandLowReviewStores(ctx context.Context, from, to time.Time, limit int) ([]StoreSearchMetric, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, e := s.db.Query(ctx, `SELECT result_key,max(store_id),max(external_provider),max(external_place_id),sum(impression_count),sum(click_count),sum(open_count),sum(favorite_count),sum(review_count),max(platform_review_count_latest) FROM store_search_daily_metrics WHERE metric_date BETWEEN $1 AND $2 GROUP BY result_key HAVING coalesce(max(platform_review_count_latest),0)<3 ORDER BY sum(impression_count) DESC LIMIT $3`, from, to, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []StoreSearchMetric
	for rows.Next() {
		var x StoreSearchMetric
		if e = rows.Scan(&x.ResultKey, &x.StoreID, &x.ExternalProvider, &x.ExternalPlaceID, &x.Impressions, &x.Clicks, &x.Opens, &x.Favorites, &x.Reviews, &x.PlatformReviewCount); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) GetMostImpressedStores(ctx context.Context, from, to time.Time, limit int) ([]StoreSearchMetric, error) {
	return s.getStoreSearchMetrics(ctx, from, to, limit, "impressions")
}
func (s *Service) GetMostClickedStores(ctx context.Context, from, to time.Time, limit int) ([]StoreSearchMetric, error) {
	return s.getStoreSearchMetrics(ctx, from, to, limit, "clicks")
}
func (s *Service) getStoreSearchMetrics(ctx context.Context, from, to time.Time, limit int, order string) ([]StoreSearchMetric, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	orderSQL := "sum(impression_count)"
	if order == "clicks" {
		orderSQL = "sum(click_count)"
	}
	rows, e := s.db.Query(ctx, `SELECT result_key,(array_agg(store_id) FILTER(WHERE store_id IS NOT NULL))[1],max(external_provider),max(external_place_id),sum(impression_count),sum(click_count),sum(open_count),sum(favorite_count),sum(review_count),max(platform_review_count_latest) FROM store_search_daily_metrics WHERE metric_date BETWEEN $1 AND $2 GROUP BY result_key ORDER BY `+orderSQL+` DESC LIMIT $3`, from, to, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []StoreSearchMetric
	for rows.Next() {
		var x StoreSearchMetric
		if e = rows.Scan(&x.ResultKey, &x.StoreID, &x.ExternalProvider, &x.ExternalPlaceID, &x.Impressions, &x.Clicks, &x.Opens, &x.Favorites, &x.Reviews, &x.PlatformReviewCount); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) GetUserGrowth(ctx context.Context, from, to time.Time) ([]DailyMetrics, error) {
	return s.GetDailyMetrics(ctx, from, to)
}
func (s *Service) GetReviewGrowth(ctx context.Context, from, to time.Time) ([]DailyMetrics, error) {
	return s.GetDailyMetrics(ctx, from, to)
}
func (s *Service) GetSearchMetrics(ctx context.Context, from, to time.Time) (SearchOverview, error) {
	return s.GetSearchOverview(ctx, from, to)
}
