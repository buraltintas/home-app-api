-- Where a store's point came from, in the store's own row.
--
-- Not every chain publishes a coordinate. Özdilek prints its whole network -- name, address,
-- telephone -- and no point at all, so each of its 155 shops is placed at the centre of the
-- town its address names: a few kilometres out rather than absent, which is the right trade
-- but only if it is visible. A catalogue that sorts by distance and cannot say which of its
-- distances are measured and which are inferred is quietly lying to whoever reads it.
--
-- The importer already decided this per row and wrote the reason into its run record, where
-- nothing on the request path can see it. This puts it where the store is.
ALTER TABLE stores ADD COLUMN IF NOT EXISTS location_from text NOT NULL DEFAULT '';
COMMENT ON COLUMN stores.location_from IS
  'published | placed at the centre of <town> ... — empty for rows predating this column';
