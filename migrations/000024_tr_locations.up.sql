-- Turkey's administrative places, so that choosing where to search stops being a
-- purchased request. Every province, district and neighbourhood with a point, shipped in
-- the binary and loaded by cmd/seed-locations; there is no provider behind this table and
-- no per-query cost.
--
-- search_key is the name already folded the way internal/textnorm folds it: lowercased,
-- stripped of diacritics and punctuation. Folding at write time rather than at read time
-- is what lets an index answer "unca" for "Uncalı".
CREATE TABLE tr_locations(
  id            text PRIMARY KEY,
  kind          text NOT NULL CHECK (kind IN ('il','ilce','mahalle')),
  name          text NOT NULL,
  search_key    text NOT NULL,
  parent_id     text REFERENCES tr_locations(id) ON DELETE CASCADE,
  province_name text NOT NULL,
  district_name text,
  location      geography(Point,4326) NOT NULL,
  population    integer NOT NULL DEFAULT 0 CHECK (population >= 0)
);

-- Three ways the picker reads this table, three indexes.
--
-- A person types the beginning of a name far more often than the middle, and a prefix on
-- a plain btree is the cheapest answer there is; text_pattern_ops is what makes LIKE
-- 'x%' use it regardless of the database's collation.
CREATE INDEX tr_locations_prefix_idx ON tr_locations(search_key text_pattern_ops);
-- ...but the fragment can also begin mid-name ("kadi" inside "yenikadikoy"), and only a
-- trigram index keeps that out of a 70,000-row scan.
CREATE INDEX tr_locations_key_trgm_idx ON tr_locations USING gin (search_key gin_trgm_ops);
-- Ties between identically named places are broken by kind and then by size, so the
-- ordering columns are worth an index of their own.
CREATE INDEX tr_locations_rank_idx ON tr_locations(kind, population DESC);
CREATE INDEX tr_locations_location_gix ON tr_locations USING gist(location);
CREATE INDEX tr_locations_parent_idx ON tr_locations(parent_id);
