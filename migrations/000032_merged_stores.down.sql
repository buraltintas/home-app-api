DROP INDEX IF EXISTS stores_merged_into_idx;
ALTER TABLE stores DROP COLUMN IF EXISTS merged_into;
