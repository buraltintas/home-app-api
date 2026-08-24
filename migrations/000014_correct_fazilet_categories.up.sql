-- This Google store was imported while a generic "çeyiz" word in a store name expanded
-- to bedding, kitchenware and tableware. Its explicit name only establishes curtain/home
-- textile. Keep the correction scoped to the provider identity so no administrator-owned
-- category choices on other stores are rewritten without provenance.
DELETE FROM store_category_links link
USING store_external_sources source, store_categories category
WHERE source.store_id=link.store_id
  AND category.id=link.category_id
  AND source.provider='google'
  AND source.external_id='ChIJOxqulheRwxQRTbR8NOWvePc'
  AND category.slug IN ('bedding','kitchenware','tableware');
