-- "Tamirhane" names a workshop, not a shop selling the products it repairs. Remove stale
-- retail links from every such catalogue row using only the stored business name; this
-- makes no provider request and contains no per-store exception.
DELETE FROM store_category_links link
USING stores store
WHERE link.store_id=store.id
  AND lower(store.name) ~ '(^|[^[:alpha:]])tamirhane';

-- Punctuation-insensitive name lookup uses a leading wildcard because the remembered
-- fragment can begin in the middle of a sign. A trigram expression index keeps that
-- fallback out of a catalogue-wide scan as the catalogue grows.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX store_compact_name_trgm_idx ON stores USING gin (
  regexp_replace(lower(coalesce(name,'')||coalesce(brand_name,'')),'[^[:alnum:]]','','g') gin_trgm_ops
) WHERE deleted_at IS NULL;
