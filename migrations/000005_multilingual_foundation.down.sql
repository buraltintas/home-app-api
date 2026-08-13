DROP INDEX IF EXISTS push_devices_locale_idx;
DROP INDEX IF EXISTS users_preferred_locale_idx;
DROP INDEX IF EXISTS searches_language_time_idx;
DROP TABLE IF EXISTS locale_daily_metrics;
DROP TABLE IF EXISTS store_translations;
DROP TABLE IF EXISTS store_category_translations;
CREATE TEMP TABLE search_intent_rollback AS
SELECT metric_date,dimension,value,sum(search_count)::bigint search_count,max(updated_at) updated_at
FROM search_intent_daily_metrics GROUP BY metric_date,dimension,value;
TRUNCATE search_intent_daily_metrics;
INSERT INTO search_intent_daily_metrics(metric_date,dimension,value,query_language,search_count,updated_at)
SELECT metric_date,dimension,value,'tr',search_count,updated_at FROM search_intent_rollback;
ALTER TABLE search_intent_daily_metrics DROP CONSTRAINT IF EXISTS search_intent_daily_metrics_pkey;
ALTER TABLE search_intent_daily_metrics DROP COLUMN IF EXISTS query_language;
ALTER TABLE search_intent_daily_metrics ADD PRIMARY KEY(metric_date,dimension,value);
DROP TABLE search_intent_rollback;
ALTER TABLE search_query_daily_metrics DROP COLUMN IF EXISTS query_language;
ALTER TABLE notification_outbox DROP COLUMN IF EXISTS template_params,DROP COLUMN IF EXISTS template_key,DROP COLUMN IF EXISTS locale;
ALTER TABLE push_devices DROP COLUMN IF EXISTS locale;
ALTER TABLE email_outbox DROP COLUMN IF EXISTS locale;
ALTER TABLE email_verification_codes DROP COLUMN IF EXISTS locale;
ALTER TABLE searches DROP COLUMN IF EXISTS query_language;
ALTER TABLE visitor_sessions DROP COLUMN IF EXISTS locale;
ALTER TABLE comments DROP COLUMN IF EXISTS content_language;
ALTER TABLE posts DROP COLUMN IF EXISTS content_language;
ALTER TABLE user_profiles DROP COLUMN IF EXISTS bio_language;
ALTER TABLE users DROP COLUMN IF EXISTS preferred_locale;
DROP TYPE IF EXISTS supported_locale;
