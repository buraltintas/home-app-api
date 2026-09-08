DROP INDEX IF EXISTS store_sources_missing_details_idx;
ALTER TABLE store_external_sources DROP COLUMN IF EXISTS details_fetched_at;
