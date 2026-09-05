-- Storage is no longer a browsable product category. Keeping the reference row inactive
-- preserves existing reporting dimensions and makes the change reversible without
-- rewriting search history.
UPDATE store_categories SET active=false WHERE slug='storage';

-- Re-run the catalogue boundary locally for two classes learned after their rows were
-- imported. This uses the provider types already stored in JSON and trade-wide wording;
-- it performs no paid provider request and contains no per-store exception.
DELETE FROM store_category_links link
USING stores store
WHERE link.store_id=store.id
  AND (
    lower(store.name) ~ '(halı|hali|carpet).*(yıkama|yikama|temizleme|cleaning)'
    OR lower(store.name) ~ '(fotoğraf|fotograf|photo(graphy)?).*(stüdyo|studyo|studio|çı|ci|çılık|cilik)'
    OR EXISTS (
      SELECT 1
      FROM store_external_sources source,
           jsonb_array_elements_text(
             CASE WHEN jsonb_typeof(source.attribution->'types')='array'
                  THEN source.attribution->'types' ELSE '[]'::jsonb END
           ) AS provider_type(value)
      WHERE source.store_id=store.id
        AND source.provider='google'
        AND provider_type.value IN ('photography_studio','photographer')
    )
  );
