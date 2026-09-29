UPDATE brands SET locator_kind='none', locator_config=NULL WHERE locator_kind='harvest';
ALTER TABLE brands DROP CONSTRAINT IF EXISTS brands_locator_kind_check;
ALTER TABLE brands ADD CONSTRAINT brands_locator_kind_check
  CHECK (locator_kind = ANY (ARRAY['none'::text, 'json'::text, 'html'::text]));
