package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Service) dayBounds(day time.Time) (time.Time, time.Time, time.Time) {
	local := day.In(s.location)
	d := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.location)
	return d, d.UTC(), d.AddDate(0, 0, 1).UTC()
}

// aggregateLockKey serialises aggregation across processes. The rollup now runs inside the
// API, which is horizontally scaled, so several instances wake to the same hour and would
// otherwise rewrite the same days at the same time.
const aggregateLockKey = 774_1991

func (s *Service) AggregateDay(ctx context.Context, day time.Time) error {
	d, start, end := s.dayBounds(day)
	metricDate := d.Format("2006-01-02")
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var locked bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, aggregateLockKey).Scan(&locked); e != nil {
		return e
	}
	if !locked {
		// Somebody else holds it and is aggregating the same window. Skipping is correct:
		// the next hourly tick recomputes the day from scratch anyway.
		return nil
	}
	runID := uuid.New()
	_, _ = s.db.Exec(ctx, `INSERT INTO reporting_runs(id,run_type,from_date,to_date,status) VALUES($1,'daily',$2,$2,'running')`, runID, metricDate)
	_, e = tx.Exec(ctx, `INSERT INTO platform_daily_metrics(metric_date,registered_users_total,new_users_count,active_users_count,anonymous_visitors_count,stores_total,new_stores_count,google_imported_stores_total,posts_current_total,posts_created_lifetime,new_posts_count,posts_deleted_count,verified_posts_count,location_rejected_post_attempts,comments_current_total,new_comments_count,likes_current_total,new_likes_count,follows_current_total,new_follows_count,favorites_current_total,new_favorites_count,searches_total,searches_count,searches_with_results_count,zero_result_searches_count,authenticated_searches_count,anonymous_searches_count,ai_searches_count,google_places_searches_count,search_result_impressions_count,search_result_clicks_count,store_opens_from_search_count,favorites_from_search_count,reviews_from_search_count,media_current_total,new_media_count,otp_requests_count,successful_auth_count,failed_auth_count,updated_at)
	SELECT $1,
	 (SELECT count(*) FROM users WHERE created_at<$3 AND (deleted_at IS NULL OR deleted_at>=$3)),(SELECT count(*) FROM users WHERE created_at>=$2 AND created_at<$3),
	 (SELECT count(DISTINCT user_id) FROM platform_events WHERE user_id IS NOT NULL AND created_at>=$2 AND created_at<$3 AND event_type IN ('user_login_succeeded','search_performed','post_created','favorite_created','like_created','comment_created','follow_created')),
	 (SELECT count(DISTINCT visitor_session_id) FROM searches WHERE visitor_session_id IS NOT NULL AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM stores WHERE created_at<$3 AND (deleted_at IS NULL OR deleted_at>=$3)),(SELECT count(*) FROM stores WHERE created_at>=$2 AND created_at<$3),
	 (SELECT count(DISTINCT x.store_id) FROM store_external_sources x JOIN stores st ON st.id=x.store_id WHERE x.provider='google' AND x.created_at<$3 AND (st.deleted_at IS NULL OR st.deleted_at>=$3)),
	 (SELECT count(*) FROM posts WHERE created_at<$3 AND (deleted_at IS NULL OR deleted_at>=$3)),(SELECT count(*) FROM posts WHERE created_at<$3),(SELECT count(*) FROM posts WHERE created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM platform_events WHERE event_type='post_deleted' AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM posts WHERE visit_verified AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM platform_events WHERE event_type='post_location_rejected' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM comments WHERE created_at<$3 AND (deleted_at IS NULL OR deleted_at>=$3)),(SELECT count(*) FROM comments WHERE created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM likes),(SELECT count(*) FROM platform_events WHERE event_type='like_created' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM follows),(SELECT count(*) FROM platform_events WHERE event_type='follow_created' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM favorites),(SELECT count(*) FROM platform_events WHERE event_type='favorite_created' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM searches WHERE created_at<$3),(SELECT count(*) FROM searches WHERE created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM searches WHERE total_result_count>0 AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM searches WHERE total_result_count=0 AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM searches WHERE user_id IS NOT NULL AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM searches WHERE user_id IS NULL AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM searches WHERE ai_used AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM searches WHERE google_places_used AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM search_results r JOIN searches s ON s.id=r.search_id WHERE s.created_at>=$2 AND s.created_at<$3),
	 (SELECT count(*) FROM search_interactions WHERE event_type='result_click' AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM search_interactions WHERE event_type='store_open' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM search_interactions WHERE event_type='favorite' AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM search_interactions WHERE event_type='review_created' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM media WHERE created_at<$3 AND status<>'deleted'),(SELECT count(*) FROM media WHERE created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM platform_events WHERE event_type='otp_requested' AND created_at>=$2 AND created_at<$3),(SELECT count(*) FROM platform_events WHERE event_type='user_login_succeeded' AND created_at>=$2 AND created_at<$3),
	 (SELECT count(*) FROM platform_events WHERE event_type IN ('user_login_failed','otp_verification_failed') AND created_at>=$2 AND created_at<$3),now()
		ON CONFLICT(metric_date) DO UPDATE SET registered_users_total=excluded.registered_users_total,new_users_count=excluded.new_users_count,active_users_count=excluded.active_users_count,anonymous_visitors_count=excluded.anonymous_visitors_count,stores_total=excluded.stores_total,new_stores_count=excluded.new_stores_count,google_imported_stores_total=excluded.google_imported_stores_total,posts_current_total=excluded.posts_current_total,posts_created_lifetime=excluded.posts_created_lifetime,new_posts_count=excluded.new_posts_count,posts_deleted_count=excluded.posts_deleted_count,verified_posts_count=excluded.verified_posts_count,location_rejected_post_attempts=excluded.location_rejected_post_attempts,comments_current_total=excluded.comments_current_total,new_comments_count=excluded.new_comments_count,likes_current_total=excluded.likes_current_total,new_likes_count=excluded.new_likes_count,follows_current_total=excluded.follows_current_total,new_follows_count=excluded.new_follows_count,favorites_current_total=excluded.favorites_current_total,new_favorites_count=excluded.new_favorites_count,searches_total=excluded.searches_total,searches_count=excluded.searches_count,searches_with_results_count=excluded.searches_with_results_count,zero_result_searches_count=excluded.zero_result_searches_count,authenticated_searches_count=excluded.authenticated_searches_count,anonymous_searches_count=excluded.anonymous_searches_count,ai_searches_count=excluded.ai_searches_count,google_places_searches_count=excluded.google_places_searches_count,search_result_impressions_count=excluded.search_result_impressions_count,search_result_clicks_count=excluded.search_result_clicks_count,store_opens_from_search_count=excluded.store_opens_from_search_count,favorites_from_search_count=excluded.favorites_from_search_count,reviews_from_search_count=excluded.reviews_from_search_count,media_current_total=excluded.media_current_total,new_media_count=excluded.new_media_count,otp_requests_count=excluded.otp_requests_count,successful_auth_count=excluded.successful_auth_count,failed_auth_count=excluded.failed_auth_count,updated_at=now()`, metricDate, start, end)
	if e != nil {
		return s.failRun(ctx, runID, e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM search_query_daily_metrics WHERE metric_date=$1`, metricDate); e != nil {
		return s.failRun(ctx, runID, e)
	}
	_, e = tx.Exec(ctx, `WITH ds AS (SELECT * FROM searches WHERE created_at>=$2 AND created_at<$3),ia AS (SELECT i.search_id,count(*) FILTER(WHERE i.event_type='result_click') clicks,count(*) FILTER(WHERE i.event_type='store_open') opens,count(*) FILTER(WHERE i.event_type='favorite') favorites,count(*) FILTER(WHERE i.event_type='review_created') reviews FROM search_interactions i JOIN ds ON ds.id=i.search_id GROUP BY i.search_id) INSERT INTO search_query_daily_metrics(metric_date,query_fingerprint,normalized_query,query_language,search_count,unique_user_count,unique_visitor_count,result_count_total,zero_result_count,result_click_count,store_open_count,favorite_count,review_count,ai_search_count,google_places_search_count) SELECT $1,digest(coalesce(query_language::text,'unknown')||':'||normalized_query,'sha256'),normalized_query,coalesce(query_language,'tr'),count(*),count(DISTINCT user_id),count(DISTINCT visitor_session_id),sum(total_result_count),count(*) FILTER(WHERE total_result_count=0),coalesce(sum(ia.clicks),0),coalesce(sum(ia.opens),0),coalesce(sum(ia.favorites),0),coalesce(sum(ia.reviews),0),count(*) FILTER(WHERE ai_used),count(*) FILTER(WHERE google_places_used) FROM ds LEFT JOIN ia ON ia.search_id=ds.id GROUP BY normalized_query,query_language`, metricDate, start, end)
	if e != nil {
		return s.failRun(ctx, runID, e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM search_intent_daily_metrics WHERE metric_date=$1`, metricDate); e != nil {
		return s.failRun(ctx, runID, e)
	}
	_, e = tx.Exec(ctx, `WITH ds AS (SELECT parsed_intent,coalesce(query_language,'tr') query_language FROM searches WHERE created_at>=$2 AND created_at<$3),dims AS (SELECT 'category' dimension,jsonb_array_elements_text(CASE WHEN jsonb_typeof(parsed_intent->'categories')='array' THEN parsed_intent->'categories' ELSE '[]'::jsonb END) value,query_language FROM ds UNION ALL SELECT 'product',jsonb_array_elements_text(CASE WHEN jsonb_typeof(parsed_intent->'product_terms')='array' THEN parsed_intent->'product_terms' ELSE '[]'::jsonb END),query_language FROM ds UNION ALL SELECT 'style',jsonb_array_elements_text(CASE WHEN jsonb_typeof(parsed_intent->'style_terms')='array' THEN parsed_intent->'style_terms' ELSE '[]'::jsonb END),query_language FROM ds UNION ALL SELECT 'location',nullif(parsed_intent->>'location_text',''),query_language FROM ds UNION ALL SELECT 'price_intent',nullif(parsed_intent->>'price_intent',''),query_language FROM ds) INSERT INTO search_intent_daily_metrics(metric_date,dimension,value,query_language,search_count) SELECT $1,dimension,value,query_language,count(*) FROM dims WHERE value IS NOT NULL GROUP BY dimension,value,query_language`, metricDate, start, end)
	if e != nil {
		return s.failRun(ctx, runID, e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM store_search_daily_metrics WHERE metric_date=$1`, metricDate); e != nil {
		return s.failRun(ctx, runID, e)
	}
	_, e = tx.Exec(ctx, `WITH ri AS (SELECT r.*,coalesce(r.store_id::text,r.external_provider||':'||r.external_place_id) result_key FROM search_results r JOIN searches s ON s.id=r.search_id WHERE s.created_at>=$2 AND s.created_at<$3),ia AS (SELECT i.search_result_id,count(*) FILTER(WHERE i.event_type='result_click') clicks,count(*) FILTER(WHERE i.event_type='store_open') opens,count(*) FILTER(WHERE i.event_type='favorite') favorites,count(*) FILTER(WHERE i.event_type='review_created') reviews FROM search_interactions i JOIN ri ON ri.id=i.search_result_id WHERE i.search_result_id IS NOT NULL GROUP BY i.search_result_id) INSERT INTO store_search_daily_metrics(metric_date,result_key,store_id,external_provider,external_place_id,impression_count,click_count,open_count,favorite_count,review_count,platform_review_count_latest) SELECT $1,ri.result_key,(array_agg(ri.store_id) FILTER(WHERE ri.store_id IS NOT NULL))[1],max(ri.external_provider),max(ri.external_place_id),count(*),coalesce(sum(ia.clicks),0),coalesce(sum(ia.opens),0),coalesce(sum(ia.favorites),0),coalesce(sum(ia.reviews),0),max(ri.platform_review_count_at_time) FROM ri LEFT JOIN ia ON ia.search_result_id=ri.id WHERE ri.result_key IS NOT NULL GROUP BY ri.result_key`, metricDate, start, end)
	if e != nil {
		return s.failRun(ctx, runID, e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM locale_daily_metrics WHERE metric_date=$1`, metricDate); e != nil {
		return s.failRun(ctx, runID, e)
	}
	_, e = tx.Exec(ctx, `INSERT INTO locale_daily_metrics(metric_date,dimension,locale,event_count,zero_result_count,ai_fallback_count)
	SELECT $1::date,'search_query',query_language,count(*),count(*) FILTER(WHERE total_result_count=0),count(*) FILTER(WHERE fallback_state LIKE '%ai_%') FROM searches WHERE created_at>=$2 AND created_at<$3 AND query_language IS NOT NULL GROUP BY query_language
	UNION ALL SELECT $1::date,'user_preference',preferred_locale,count(*),0,0 FROM users WHERE created_at<$3 AND (deleted_at IS NULL OR deleted_at>=$3) GROUP BY preferred_locale
	UNION ALL SELECT $1::date,'anonymous_session',locale,count(*),0,0 FROM visitor_sessions WHERE created_at>=$2 AND created_at<$3 AND locale IS NOT NULL GROUP BY locale
	UNION ALL SELECT $1::date,'email',locale,count(*),0,0 FROM email_outbox WHERE created_at>=$2 AND created_at<$3 GROUP BY locale
	UNION ALL SELECT $1::date,'push_device',locale,count(*),0,0 FROM push_devices WHERE created_at<$3 AND (disabled_at IS NULL OR disabled_at>=$3) GROUP BY locale
	UNION ALL SELECT $1::date,'notification',locale,count(*),0,0 FROM notification_outbox WHERE created_at>=$2 AND created_at<$3 GROUP BY locale`, metricDate, start, end)
	if e != nil {
		return s.failRun(ctx, runID, e)
	}
	if e = tx.Commit(ctx); e != nil {
		return s.failRun(ctx, runID, e)
	}
	_, _ = s.db.Exec(ctx, `UPDATE reporting_runs SET status='completed',completed_at=now() WHERE id=$1`, runID)
	return nil
}
func (s *Service) failRun(ctx context.Context, id uuid.UUID, cause error) error {
	_, _ = s.db.Exec(ctx, `UPDATE reporting_runs SET status='failed',error_message=$2,completed_at=now() WHERE id=$1`, id, "aggregation failed")
	return cause
}

func (s *Service) Rebuild(ctx context.Context) error {
	if e := s.RebuildSnapshot(ctx); e != nil {
		return e
	}
	var earliest *time.Time
	if e := s.db.QueryRow(ctx, `SELECT min(t) FROM (SELECT min(created_at)t FROM users UNION ALL SELECT min(created_at) FROM searches UNION ALL SELECT min(created_at) FROM platform_events)x`).Scan(&earliest); e != nil {
		return e
	}
	if earliest == nil {
		return nil
	}
	start, _, _ := s.dayBounds(*earliest)
	today, _, _ := s.dayBounds(s.now())
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		if e := s.AggregateDay(ctx, d); e != nil {
			return fmt.Errorf("aggregate %s: %w", d.Format("2006-01-02"), e)
		}
	}
	return nil
}

func (s *Service) Run(ctx context.Context) error {
	if e := s.aggregateAttributionWindow(ctx, s.now()); e != nil {
		return e
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			_ = s.aggregateAttributionWindow(ctx, now)
		}
	}
}

func (s *Service) aggregateAttributionWindow(ctx context.Context, now time.Time) error {
	for i := 0; i <= s.attributionDays; i++ {
		if e := s.AggregateDay(ctx, now.AddDate(0, 0, -i)); e != nil {
			return e
		}
	}
	return nil
}
