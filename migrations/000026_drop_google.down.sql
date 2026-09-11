-- The provider rows cannot be restored: they were bought, not derived. This only puts the
-- tables back so an older binary can start.
CREATE TABLE IF NOT EXISTS places_search_cache(
  cache_key text PRIMARY KEY, places jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS places_search_cache_created_idx ON places_search_cache(created_at);
