-- Identical searches were being paid for over and over. Two thirds of the searches on
-- record repeat a query already asked in the same place, and each repeat was a fresh call
-- to the provider for an answer that had not changed. The response is kept here for a
-- short while and served from memory of it instead.
--
-- Deliberately not a store cache. What is cached is one question to the provider, so a
-- catalogue write still happens on the way through and nothing about freshness of an
-- individual store changes.
CREATE TABLE places_search_cache (
  cache_key   text PRIMARY KEY,
  places      jsonb NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX places_search_cache_created_at_idx ON places_search_cache (created_at);
