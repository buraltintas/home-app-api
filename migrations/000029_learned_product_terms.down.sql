DELETE FROM product_terms WHERE source='learned';
ALTER TABLE product_terms DROP CONSTRAINT IF EXISTS product_terms_source_check;
ALTER TABLE product_terms ADD CONSTRAINT product_terms_source_check
  CHECK (source IN ('seed','admin','user'));
