-- Shops that no chain publishes.
--
-- The catalogue was built from brands' own store lists, and that is why every import run
-- belongs to a brand and why a store's provenance is one of four words. Neither holds for an
-- independent shop taken from the public map: nobody in particular published it, and there
-- is no brand behind the run that read it.
--
-- 'osm' is a fifth provenance rather than a stretch of an existing one. It is not
-- 'brand_locator' (no chain vouches for it), not 'admin' (nobody here typed it), not 'user'
-- (no visitor added it) and not 'legacy' (it is not left over from the provider we dropped).
-- Search already ranks unverified rows behind verified ones, so this lands in the right place
-- without a second rule.
ALTER TABLE store_import_runs ALTER COLUMN brand_id DROP NOT NULL;
COMMENT ON COLUMN store_import_runs.brand_id IS
  'The brand whose list was read, or null for a source that is not a brand -- the public map.';

ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_source_kind_check;
ALTER TABLE stores ADD CONSTRAINT stores_source_kind_check
  CHECK (source_kind = ANY (ARRAY['brand_locator','admin','user','legacy','osm']));
