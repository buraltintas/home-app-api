-- Search results deliberately use a cheaper Google Places field mask. This marker records
-- that the full detail payload has already been bought for a store, including the valid
-- case where Google returned none of its optional rating/contact/hour fields.
ALTER TABLE store_external_sources
  ADD COLUMN details_fetched_at timestamptz;

-- Before this split every search used the full field mask. Any stored field that only that
-- mask could have returned proves the old row is already enriched; preserve it and avoid
-- buying the same answer again on its first post-deploy detail view.
UPDATE store_external_sources source
SET details_fetched_at = source.refreshed_at
FROM stores store
WHERE source.store_id = store.id
  AND source.provider = 'google'
  AND source.details_fetched_at IS NULL
  AND (
    source.attribution ?| ARRAY['rating', 'rating_count', 'opening_hours', 'phone']
    OR coalesce(store.phone, '') <> ''
    OR coalesce(store.website, '') <> ''
  );

CREATE INDEX store_sources_missing_details_idx
  ON store_external_sources (store_id)
  WHERE provider = 'google' AND details_fetched_at IS NULL;
