DROP TABLE IF EXISTS reporting_runs,store_search_daily_metrics,search_intent_daily_metrics,search_query_daily_metrics,platform_daily_metrics,platform_stats,platform_events;
DROP INDEX IF EXISTS search_interactions_store_time_idx,search_interactions_type_time_idx,search_results_external_idx,searches_normalized_time_idx,searches_created_status_idx;
ALTER TABLE search_interactions DROP CONSTRAINT IF EXISTS search_interactions_event_type_check;
UPDATE search_interactions SET event_type=CASE event_type WHEN 'result_impression' THEN 'impression' WHEN 'result_click' THEN 'click' WHEN 'review_created' THEN 'review' WHEN 'review_started' THEN 'store_open' WHEN 'unfavorite' THEN 'favorite' ELSE event_type END;
ALTER TABLE search_interactions DROP COLUMN IF EXISTS metadata,DROP COLUMN IF EXISTS store_id;
ALTER TABLE search_interactions ADD CONSTRAINT search_interactions_event_type_check CHECK(event_type IN ('impression','click','store_open','favorite','review','share'));
ALTER TABLE search_results DROP COLUMN IF EXISTS ranking_reason,DROP COLUMN IF EXISTS ranking_score,DROP COLUMN IF EXISTS distance_meters,DROP COLUMN IF EXISTS platform_post_count_at_time;
ALTER TABLE searches DROP COLUMN IF EXISTS error_code,DROP COLUMN IF EXISTS status,DROP COLUMN IF EXISTS google_places_used,DROP COLUMN IF EXISTS location_text;
