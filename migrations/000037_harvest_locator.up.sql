-- A brand whose own store finder a plain client cannot read.
--
-- The pages are permitted -- robots.txt disallows carts, accounts and password resets and
-- says nothing about dealers -- but the CDN in front of the site answers anything that is
-- not a browser with 403. So the finder is read in a browser and what it gave up is
-- committed to the repository as a file with its date on it. 'harvest' is that: a locator
-- whose source is a file we hold rather than a request we make.
--
-- It is a distinct kind rather than a flavour of 'html' because the difference is the one
-- thing anybody needs to know about such a brand: it does not refresh itself, and it
-- cannot join the monthly unattended run until it can be fetched plainly.
ALTER TABLE brands DROP CONSTRAINT IF EXISTS brands_locator_kind_check;
ALTER TABLE brands ADD CONSTRAINT brands_locator_kind_check
  CHECK (locator_kind = ANY (ARRAY['none'::text, 'json'::text, 'html'::text, 'harvest'::text]));
