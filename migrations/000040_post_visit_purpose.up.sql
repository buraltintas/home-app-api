-- Why somebody went to the shop, chosen from a short fixed list rather than typed: buying a
-- gift, shopping for a trousseau, furnishing a new home, or an ordinary trip. "routine" is
-- the answer for every other reason, so the list is complete without growing.
--
-- A trousseau shopper and somebody replacing a kettle judge the same shop by different
-- things, and a reader planning one of those trips wants the reviews written on one.
--
-- Optional, and NULL means "not asked" -- every review written before the form asked, and
-- every one written by a client that does not ask -- which is not the same as "routine".
-- The values are the API's own enum, so the check names them here too: a value the service
-- does not know cannot reach the column by some other route.
ALTER TABLE posts
  ADD COLUMN IF NOT EXISTS visit_purpose text;

ALTER TABLE posts
  DROP CONSTRAINT IF EXISTS posts_visit_purpose_check;
ALTER TABLE posts
  ADD CONSTRAINT posts_visit_purpose_check
  CHECK (visit_purpose IS NULL OR visit_purpose IN ('gift', 'trousseau', 'new_home', 'routine'));
