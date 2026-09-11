-- The panel's "imported from Google" figure outlived Google. The provider rows it counted
-- were deleted in 000026, but this is an incrementally maintained counter and nothing
-- recounted it, so it kept showing the last number it had -- which happened to equal the
-- whole catalogue, and read as though every store still came from Google.
--
-- The useful figure now is how much of the catalogue a brand's own published list stands
-- behind. Same column, renamed to what it will actually hold, and recounted below.
ALTER TABLE platform_stats RENAME COLUMN google_imported_stores_total TO brand_verified_stores_total;

UPDATE platform_stats SET
  brand_verified_stores_total=(SELECT count(*) FROM stores WHERE deleted_at IS NULL AND source_kind='brand_locator'),
  stores_total=(SELECT count(*) FROM stores WHERE deleted_at IS NULL),
  updated_at=now()
WHERE id=1;

-- The daily table is history and keeps its column under its own name; what it recorded on
-- the days it recorded it was true. It simply stops being written.
ALTER TABLE platform_daily_metrics ADD COLUMN IF NOT EXISTS brand_verified_stores_total bigint NOT NULL DEFAULT 0;
