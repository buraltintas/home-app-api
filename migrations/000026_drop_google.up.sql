-- Google leaves. What it gave us that we can still stand behind -- a name, an address, a
-- point, a district -- stays; everything that was theirs goes, along with the tables that
-- existed only to make asking them cheaper.
--
-- No store row is deleted. A review, a favourite and a rating belong to the shop, not to
-- whoever first told us the shop existed, and a catalogue that forgot the shop would take
-- what people wrote with it.

-- Their ratings, their photographs, their opening hours, their place ids.
DELETE FROM store_external_sources WHERE provider='google';

-- Rows that had nothing but Google behind them are now unverified: still listed, still
-- searchable, but ranked behind anything a brand's own list confirms, and waiting for one.
UPDATE stores SET source_kind='legacy', data_verified_at=NULL
WHERE deleted_at IS NULL AND source_kind='legacy'
  AND NOT EXISTS(SELECT 1 FROM store_external_sources x WHERE x.store_id=stores.id);

-- A cache of provider answers, and a sampler that existed only to compare our results
-- against theirs. Neither has a question left to answer.
DROP TABLE IF EXISTS places_search_cache;
DROP TABLE IF EXISTS search_shadow_measurements;

-- The gate columns on searches stay: they are history, and dropping them would rewrite
-- what past searches recorded about themselves. Nothing writes them from now on.

-- A review no longer requires proof of being there, so the distance it was written from is
-- something we may simply not know. Unknown is not zero: zero would claim the writer stood
-- in the shop.
ALTER TABLE posts ALTER COLUMN verification_distance_meters DROP NOT NULL;
ALTER TABLE posts ALTER COLUMN visit_verified SET DEFAULT false;
