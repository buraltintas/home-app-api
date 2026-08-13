CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  primary_email citext NOT NULL,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE UNIQUE INDEX users_active_email_uidx ON users (primary_email) WHERE deleted_at IS NULL;

CREATE TABLE user_profiles (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  username citext UNIQUE, display_name text, avatar_url text, bio text, city text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (username IS NULL OR username::text ~ '^[a-zA-Z0-9_]{3,30}$')
);
CREATE TABLE user_private_profiles (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  relationship_status text, has_children boolean, children_age_ranges text[], housing_status text,
  occupation text, age_range text, home_style_interests text[],
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (housing_status IS NULL OR housing_status IN ('owner','renter','living_with_family','other'))
);
CREATE TABLE auth_identities (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider text NOT NULL CHECK (provider IN ('email','google')), provider_subject text NOT NULL,
  normalized_email citext, email_verified boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider, provider_subject)
);
CREATE UNIQUE INDEX auth_verified_email_provider_uidx ON auth_identities(provider, normalized_email) WHERE email_verified;
CREATE INDEX auth_identity_user_idx ON auth_identities(user_id);

CREATE TABLE auth_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  family_id uuid NOT NULL, refresh_token_hash bytea NOT NULL UNIQUE, client_type text NOT NULL DEFAULT 'unknown',
  device_metadata jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL, last_used_at timestamptz, revoked_at timestamptz,
  revoke_reason text, replaced_by uuid REFERENCES auth_sessions(id)
);
CREATE INDEX auth_sessions_user_active_idx ON auth_sessions(user_id, expires_at) WHERE revoked_at IS NULL;
CREATE INDEX auth_sessions_family_idx ON auth_sessions(family_id);

CREATE TABLE visitor_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), linked_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(), last_seen_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL
);

CREATE TABLE email_verification_codes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), normalized_email citext NOT NULL, code_hash bytea NOT NULL,
  visitor_session_id uuid REFERENCES visitor_sessions(id) ON DELETE SET NULL, request_ip_hash bytea,
  attempts int NOT NULL DEFAULT 0, max_attempts int NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL, consumed_at timestamptz, invalidated_at timestamptz
);
CREATE INDEX email_codes_lookup_idx ON email_verification_codes(normalized_email, created_at DESC);

CREATE TABLE store_categories (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), slug text NOT NULL UNIQUE, name_tr text NOT NULL, active boolean NOT NULL DEFAULT true
);
CREATE TABLE stores (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, slug text NOT NULL UNIQUE, brand_name text,
  description text, address text, city text NOT NULL, district text, country_code char(2) NOT NULL DEFAULT 'TR',
  location geography(Point,4326) NOT NULL, website text, phone text, cover_image_url text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE INDEX stores_location_gix ON stores USING gist(location);
CREATE INDEX stores_text_idx ON stores USING gin(to_tsvector('simple', coalesce(name,'') || ' ' || coalesce(brand_name,'') || ' ' || coalesce(city,'') || ' ' || coalesce(district,'')));
CREATE TABLE store_category_links (
  store_id uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE, category_id uuid NOT NULL REFERENCES store_categories(id) ON DELETE RESTRICT,
  PRIMARY KEY(store_id, category_id)
);
CREATE TABLE store_external_sources (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), store_id uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  provider text NOT NULL, external_id text NOT NULL, attribution jsonb NOT NULL DEFAULT '{}',
  refreshed_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(provider, external_id)
);
CREATE INDEX store_sources_store_idx ON store_external_sources(store_id);
CREATE TABLE store_stats (
  store_id uuid PRIMARY KEY REFERENCES stores(id) ON DELETE CASCADE,
  average_rating numeric(3,2) NOT NULL DEFAULT 0, rating_count int NOT NULL DEFAULT 0,
  review_count int NOT NULL DEFAULT 0, favorite_count int NOT NULL DEFAULT 0, post_count int NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (average_rating BETWEEN 0 AND 5), CHECK (rating_count >= 0 AND review_count >= 0 AND favorite_count >= 0 AND post_count >= 0)
);

CREATE TABLE media (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  storage_key text NOT NULL UNIQUE, mime_type text NOT NULL, width int, height int, size_bytes bigint,
  status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','ready','deleted')), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE posts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  store_id uuid NOT NULL REFERENCES stores(id) ON DELETE RESTRICT, body text NOT NULL, rating smallint NOT NULL CHECK(rating BETWEEN 1 AND 5),
  visit_verified boolean NOT NULL DEFAULT true, verification_distance_meters numeric(10,2) NOT NULL,
  verified_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE INDEX posts_feed_idx ON posts(created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_user_idx ON posts(user_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_store_idx ON posts(store_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE TABLE post_media (
  post_id uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE, media_id uuid NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
  position smallint NOT NULL CHECK(position BETWEEN 0 AND 9), PRIMARY KEY(post_id,media_id), UNIQUE(post_id,position)
);
CREATE TABLE favorites (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, store_id uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,store_id)
);
CREATE INDEX favorites_store_idx ON favorites(store_id);
CREATE TABLE likes (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, post_id uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,post_id)
);
CREATE INDEX likes_post_idx ON likes(post_id);
CREATE TABLE comments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), post_id uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, body text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE INDEX comments_post_idx ON comments(post_id,created_at,id) WHERE deleted_at IS NULL;
CREATE TABLE follows (
  follower_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, following_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(follower_id,following_id), CHECK(follower_id <> following_id)
);
CREATE INDEX follows_following_idx ON follows(following_id);

CREATE TABLE searches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  visitor_session_id uuid REFERENCES visitor_sessions(id) ON DELETE SET NULL,
  raw_query text NOT NULL, normalized_query text NOT NULL, parsed_intent jsonb NOT NULL,
  search_mode text NOT NULL, ai_used boolean NOT NULL DEFAULT false, ai_provider text, ai_model text,
  request_latitude numeric(8,5), request_longitude numeric(8,5), requested_radius_meters int,
  duration_ms int, internal_result_count int NOT NULL DEFAULT 0, external_result_count int NOT NULL DEFAULT 0,
  total_result_count int NOT NULL DEFAULT 0, fallback_state text, created_at timestamptz NOT NULL DEFAULT now(),
  CHECK(user_id IS NOT NULL OR visitor_session_id IS NOT NULL)
);
CREATE INDEX searches_user_idx ON searches(user_id,created_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX searches_visitor_idx ON searches(visitor_session_id,created_at DESC) WHERE visitor_session_id IS NOT NULL;
CREATE TABLE search_results (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), search_id uuid NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
  rank int NOT NULL CHECK(rank > 0), store_id uuid REFERENCES stores(id) ON DELETE SET NULL, source text NOT NULL,
  external_provider text, external_place_id text, platform_rating_at_time numeric(3,2), platform_review_count_at_time int,
  favorite_count_at_time int, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(search_id,rank)
);
CREATE INDEX search_results_store_idx ON search_results(store_id);
CREATE TABLE search_interactions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), search_id uuid NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
  search_result_id uuid REFERENCES search_results(id) ON DELETE SET NULL, user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  visitor_session_id uuid REFERENCES visitor_sessions(id) ON DELETE SET NULL,
  event_type text NOT NULL CHECK(event_type IN ('impression','click','store_open','favorite','review','share')),
  idempotency_key text, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX search_interaction_idempotency_uidx ON search_interactions(search_id,idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE email_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), idempotency_key text NOT NULL UNIQUE, template text NOT NULL,
  recipient citext NOT NULL, payload jsonb NOT NULL, status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','sent','failed')),
  attempts int NOT NULL DEFAULT 0, available_at timestamptz NOT NULL DEFAULT now(), locked_at timestamptz,
  provider_message_id text, last_error text, created_at timestamptz NOT NULL DEFAULT now(), sent_at timestamptz
);
CREATE INDEX email_outbox_available_idx ON email_outbox(available_at) WHERE status IN ('pending','failed');
CREATE TABLE email_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), outbox_id uuid NOT NULL REFERENCES email_outbox(id) ON DELETE CASCADE,
  provider text NOT NULL, provider_message_id text, success boolean NOT NULL, error_code text, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE push_devices (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform text NOT NULL CHECK(platform IN ('ios','android','web')), token_hash bytea NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(), disabled_at timestamptz
);
CREATE TABLE notification_preferences (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, preferences jsonb NOT NULL DEFAULT '{}', updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE notification_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_type text NOT NULL, payload jsonb NOT NULL, status text NOT NULL DEFAULT 'pending',
  available_at timestamptz NOT NULL DEFAULT now(), attempts int NOT NULL DEFAULT 0, created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO store_categories(slug,name_tr) VALUES
 ('furniture','Mobilya'),('home_textile','Ev Tekstili'),('lighting','Aydınlatma'),('decoration','Dekorasyon'),
 ('kitchenware','Mutfak'),('bathroom','Banyo'),('carpet','Halı'),('curtain','Perde'),('bedding','Yatak'),
 ('tableware','Sofra'),('storage','Depolama'),('home_accessories','Ev Aksesuarları'),('household','Ev Gereçleri');
