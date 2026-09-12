-- Where a merged shop went.
--
-- Two rows for one shop happen: a chain lists the same dealer twice, or a shop we already
-- held arrives again under a name too different to match. Merging them has to keep the
-- survivor's id -- reviews, favourites and ratings hang off it -- and must not leave the
-- other id answering nothing. A link kept here lets a request for the old one be answered
-- with the new one, so a review somebody shared years ago still opens a shop.
ALTER TABLE stores ADD COLUMN IF NOT EXISTS merged_into uuid REFERENCES stores(id);
COMMENT ON COLUMN stores.merged_into IS
  'The shop this row was merged into. Set together with deleted_at; a request for this row is answered with that one.';
CREATE INDEX IF NOT EXISTS stores_merged_into_idx ON stores(merged_into) WHERE merged_into IS NOT NULL;
