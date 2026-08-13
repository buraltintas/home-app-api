CREATE TABLE platform_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type text NOT NULL,
  idempotency_key text UNIQUE,
  user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  visitor_session_id uuid REFERENCES visitor_sessions(id) ON DELETE SET NULL,
  store_id uuid REFERENCES stores(id) ON DELETE SET NULL,
  post_id uuid REFERENCES posts(id) ON DELETE SET NULL,
  search_id uuid REFERENCES searches(id) ON DELETE SET NULL,
  metadata jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_events_type_time_idx ON platform_events(event_type,created_at);
CREATE INDEX platform_events_user_time_idx ON platform_events(user_id,created_at) WHERE user_id IS NOT NULL;
CREATE INDEX platform_events_search_idx ON platform_events(search_id) WHERE search_id IS NOT NULL;

CREATE TABLE platform_stats (
  id smallint PRIMARY KEY DEFAULT 1 CHECK(id=1),
  registered_users_total bigint NOT NULL DEFAULT 0 CHECK(registered_users_total>=0),
  stores_total bigint NOT NULL DEFAULT 0 CHECK(stores_total>=0),
  google_imported_stores_total bigint NOT NULL DEFAULT 0 CHECK(google_imported_stores_total>=0),
  posts_current_total bigint NOT NULL DEFAULT 0 CHECK(posts_current_total>=0),
  posts_created_lifetime bigint NOT NULL DEFAULT 0 CHECK(posts_created_lifetime>=0),
  posts_deleted_lifetime bigint NOT NULL DEFAULT 0 CHECK(posts_deleted_lifetime>=0),
  comments_current_total bigint NOT NULL DEFAULT 0 CHECK(comments_current_total>=0),
  likes_current_total bigint NOT NULL DEFAULT 0 CHECK(likes_current_total>=0),
  follows_current_total bigint NOT NULL DEFAULT 0 CHECK(follows_current_total>=0),
  favorites_current_total bigint NOT NULL DEFAULT 0 CHECK(favorites_current_total>=0),
  searches_lifetime bigint NOT NULL DEFAULT 0 CHECK(searches_lifetime>=0),
  media_current_total bigint NOT NULL DEFAULT 0 CHECK(media_current_total>=0),
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_stats(id) VALUES(1);

CREATE TABLE platform_daily_metrics (
  metric_date date PRIMARY KEY,
  registered_users_total bigint NOT NULL DEFAULT 0,
  new_users_count bigint NOT NULL DEFAULT 0,
  active_users_count bigint NOT NULL DEFAULT 0,
  anonymous_visitors_count bigint NOT NULL DEFAULT 0,
  stores_total bigint NOT NULL DEFAULT 0,
  new_stores_count bigint NOT NULL DEFAULT 0,
  google_imported_stores_total bigint NOT NULL DEFAULT 0,
  posts_current_total bigint NOT NULL DEFAULT 0,
  posts_created_lifetime bigint NOT NULL DEFAULT 0,
  new_posts_count bigint NOT NULL DEFAULT 0,
  posts_deleted_count bigint NOT NULL DEFAULT 0,
  verified_posts_count bigint NOT NULL DEFAULT 0,
  location_rejected_post_attempts bigint NOT NULL DEFAULT 0,
  comments_current_total bigint NOT NULL DEFAULT 0,
  new_comments_count bigint NOT NULL DEFAULT 0,
  likes_current_total bigint NOT NULL DEFAULT 0,
  new_likes_count bigint NOT NULL DEFAULT 0,
  follows_current_total bigint NOT NULL DEFAULT 0,
  new_follows_count bigint NOT NULL DEFAULT 0,
  favorites_current_total bigint NOT NULL DEFAULT 0,
  new_favorites_count bigint NOT NULL DEFAULT 0,
  searches_total bigint NOT NULL DEFAULT 0,
  searches_count bigint NOT NULL DEFAULT 0,
  searches_with_results_count bigint NOT NULL DEFAULT 0,
  zero_result_searches_count bigint NOT NULL DEFAULT 0,
  authenticated_searches_count bigint NOT NULL DEFAULT 0,
  anonymous_searches_count bigint NOT NULL DEFAULT 0,
  ai_searches_count bigint NOT NULL DEFAULT 0,
  google_places_searches_count bigint NOT NULL DEFAULT 0,
  search_result_impressions_count bigint NOT NULL DEFAULT 0,
  search_result_clicks_count bigint NOT NULL DEFAULT 0,
  store_opens_from_search_count bigint NOT NULL DEFAULT 0,
  favorites_from_search_count bigint NOT NULL DEFAULT 0,
  reviews_from_search_count bigint NOT NULL DEFAULT 0,
  media_current_total bigint NOT NULL DEFAULT 0,
  new_media_count bigint NOT NULL DEFAULT 0,
  otp_requests_count bigint NOT NULL DEFAULT 0,
  successful_auth_count bigint NOT NULL DEFAULT 0,
  failed_auth_count bigint NOT NULL DEFAULT 0,
  extra_metrics jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE searches
  ADD COLUMN location_text text,
  ADD COLUMN google_places_used boolean NOT NULL DEFAULT false,
  ADD COLUMN status text NOT NULL DEFAULT 'completed' CHECK(status IN ('completed','failed')),
  ADD COLUMN error_code text;
CREATE INDEX searches_created_status_idx ON searches(created_at,status);
CREATE INDEX searches_normalized_time_idx ON searches(normalized_query,created_at);

ALTER TABLE search_results
  ADD COLUMN platform_post_count_at_time int,
  ADD COLUMN distance_meters int,
  ADD COLUMN ranking_score numeric(12,5),
  ADD COLUMN ranking_reason text;
CREATE INDEX search_results_external_idx ON search_results(external_provider,external_place_id) WHERE external_place_id IS NOT NULL;

ALTER TABLE search_interactions DROP CONSTRAINT search_interactions_event_type_check;
ALTER TABLE search_interactions
  ADD COLUMN store_id uuid REFERENCES stores(id) ON DELETE SET NULL,
  ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}',
  ADD CONSTRAINT search_interactions_event_type_check CHECK(event_type IN ('result_impression','result_click','store_open','favorite','unfavorite','review_started','review_created','share'));
CREATE INDEX search_interactions_type_time_idx ON search_interactions(event_type,created_at);
CREATE INDEX search_interactions_store_time_idx ON search_interactions(store_id,created_at) WHERE store_id IS NOT NULL;

CREATE TABLE search_query_daily_metrics (
  metric_date date NOT NULL,
  query_fingerprint bytea NOT NULL,
  normalized_query text NOT NULL,
  search_count bigint NOT NULL DEFAULT 0,
  unique_user_count bigint NOT NULL DEFAULT 0,
  unique_visitor_count bigint NOT NULL DEFAULT 0,
  result_count_total bigint NOT NULL DEFAULT 0,
  zero_result_count bigint NOT NULL DEFAULT 0,
  result_click_count bigint NOT NULL DEFAULT 0,
  store_open_count bigint NOT NULL DEFAULT 0,
  favorite_count bigint NOT NULL DEFAULT 0,
  review_count bigint NOT NULL DEFAULT 0,
  ai_search_count bigint NOT NULL DEFAULT 0,
  google_places_search_count bigint NOT NULL DEFAULT 0,
  parsed_categories jsonb NOT NULL DEFAULT '{}',
  parsed_products jsonb NOT NULL DEFAULT '{}',
  parsed_styles jsonb NOT NULL DEFAULT '{}',
  parsed_locations jsonb NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(metric_date,query_fingerprint)
);
CREATE INDEX search_query_daily_zero_idx ON search_query_daily_metrics(metric_date,zero_result_count DESC);
CREATE INDEX search_query_daily_volume_idx ON search_query_daily_metrics(metric_date,search_count DESC);

CREATE TABLE search_intent_daily_metrics (
  metric_date date NOT NULL,
  dimension text NOT NULL CHECK(dimension IN ('category','product','style','location','price_intent')),
  value text NOT NULL,
  search_count bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(metric_date,dimension,value)
);
CREATE INDEX search_intent_daily_top_idx ON search_intent_daily_metrics(metric_date,dimension,search_count DESC);

CREATE TABLE store_search_daily_metrics (
  metric_date date NOT NULL,
  result_key text NOT NULL,
  store_id uuid REFERENCES stores(id) ON DELETE SET NULL,
  external_provider text,
  external_place_id text,
  impression_count bigint NOT NULL DEFAULT 0,
  click_count bigint NOT NULL DEFAULT 0,
  open_count bigint NOT NULL DEFAULT 0,
  favorite_count bigint NOT NULL DEFAULT 0,
  review_count bigint NOT NULL DEFAULT 0,
  platform_review_count_latest int,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(metric_date,result_key)
);
CREATE INDEX store_search_daily_store_idx ON store_search_daily_metrics(store_id,metric_date) WHERE store_id IS NOT NULL;
CREATE INDEX store_search_daily_opportunity_idx ON store_search_daily_metrics(metric_date,impression_count DESC,platform_review_count_latest);

CREATE TABLE reporting_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), run_type text NOT NULL, from_date date, to_date date,
  status text NOT NULL CHECK(status IN ('running','completed','failed')), error_message text,
  started_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);

UPDATE platform_stats SET
 registered_users_total=(SELECT count(*) FROM users WHERE deleted_at IS NULL),
 stores_total=(SELECT count(*) FROM stores WHERE deleted_at IS NULL),
 google_imported_stores_total=(SELECT count(DISTINCT store_id) FROM store_external_sources WHERE provider='google'),
 posts_current_total=(SELECT count(*) FROM posts WHERE deleted_at IS NULL),
 posts_created_lifetime=(SELECT count(*) FROM posts),posts_deleted_lifetime=(SELECT count(*) FROM posts WHERE deleted_at IS NOT NULL),
 comments_current_total=(SELECT count(*) FROM comments WHERE deleted_at IS NULL),likes_current_total=(SELECT count(*) FROM likes),
 follows_current_total=(SELECT count(*) FROM follows),favorites_current_total=(SELECT count(*) FROM favorites),
 searches_lifetime=(SELECT count(*) FROM searches),media_current_total=(SELECT count(*) FROM media WHERE status<>'deleted'),updated_at=now()
WHERE id=1;
