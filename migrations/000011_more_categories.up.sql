-- Garden, major appliances and small appliances were missing from a catalogue that calls
-- itself home and living. Somebody searching for a balcony set or a kettle had no category
-- to land in, and the classifier had nothing to map those stores to.
INSERT INTO store_categories(slug,name_tr) VALUES
 ('garden','Bahçe'),('major_appliances','Beyaz Eşya'),('small_appliances','Küçük Ev Aletleri')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO store_category_translations(category_id,locale,name)
SELECT c.id, l.locale::supported_locale,
  CASE c.slug
    WHEN 'garden' THEN CASE l.locale WHEN 'tr' THEN 'Bahçe' WHEN 'en' THEN 'Garden' WHEN 'de' THEN 'Garten' ELSE 'Сад' END
    WHEN 'major_appliances' THEN CASE l.locale WHEN 'tr' THEN 'Beyaz Eşya' WHEN 'en' THEN 'Major Appliances' WHEN 'de' THEN 'Haushaltsgroßgeräte' ELSE 'Крупная бытовая техника' END
    ELSE CASE l.locale WHEN 'tr' THEN 'Küçük Ev Aletleri' WHEN 'en' THEN 'Small Appliances' WHEN 'de' THEN 'Kleingeräte' ELSE 'Мелкая бытовая техника' END
  END
FROM store_categories c
CROSS JOIN (VALUES ('tr'),('en'),('de'),('ru')) AS l(locale)
WHERE c.slug IN ('garden','major_appliances','small_appliances')
ON CONFLICT DO NOTHING;
