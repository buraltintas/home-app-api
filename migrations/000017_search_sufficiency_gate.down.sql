DROP TABLE IF EXISTS search_shadow_measurements;
DROP INDEX IF EXISTS searches_gate_idx;
ALTER TABLE searches
  DROP COLUMN IF EXISTS local_only,
  DROP COLUMN IF EXISTS gate_reason,
  DROP COLUMN IF EXISTS local_relevance,
  DROP COLUMN IF EXISTS catalogue_coverage,
  DROP COLUMN IF EXISTS local_duration_ms,
  DROP COLUMN IF EXISTS places_duration_ms,
  DROP COLUMN IF EXISTS search_city,
  DROP COLUMN IF EXISTS search_district;
