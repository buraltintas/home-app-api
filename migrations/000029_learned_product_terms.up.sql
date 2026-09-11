-- Product vocabulary cannot be a list somebody maintains. Measured against 108 ordinary
-- Turkish words for things these shops sell, the seeded list understood 68 and missed 40 --
-- çekyat, ayakkabılık, supla, parke, ankastre, fayans -- and whatever nobody thought to add
-- fails silently, which is the worst way for it to fail.
--
-- The catalogue cannot teach it either: our stores are chain branches now, and "English Home
-- - Mersin Yenişehir Forum AVM" contains no product word. Derived from store names, only
-- eight words cleared a 60% threshold and two of those were appliance brands.
--
-- So the model teaches it, once per word. A query the model does place is written back here,
-- and the next person to use that word is answered from our own table without a call. The
-- first person to type "ankastre" waits; nobody after them does.
ALTER TABLE product_terms DROP CONSTRAINT IF EXISTS product_terms_source_check;
ALTER TABLE product_terms ADD CONSTRAINT product_terms_source_check
  CHECK (source IN ('seed','admin','user','learned'));
