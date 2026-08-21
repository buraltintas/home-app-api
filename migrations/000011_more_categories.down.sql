DELETE FROM store_category_translations WHERE category_id IN (SELECT id FROM store_categories WHERE slug IN ('garden','major_appliances','small_appliances'));
DELETE FROM store_categories WHERE slug IN ('garden','major_appliances','small_appliances');
