UPDATE stores SET source_kind='legacy' WHERE source_kind='osm';
ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_source_kind_check;
ALTER TABLE stores ADD CONSTRAINT stores_source_kind_check
  CHECK (source_kind = ANY (ARRAY['brand_locator','admin','user','legacy']));
DELETE FROM store_import_records WHERE run_id IN (SELECT id FROM store_import_runs WHERE brand_id IS NULL);
DELETE FROM store_import_runs WHERE brand_id IS NULL;
ALTER TABLE store_import_runs ALTER COLUMN brand_id SET NOT NULL;
