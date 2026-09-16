-- What the visit was for. A review that records a purchase is a different kind of evidence
-- from one written by somebody who looked and left: it says the shop was able to sell the
-- thing somebody came for. The item is free text because a shopper names what they bought in
-- their own words, and those words are what will teach the search which products a shop
-- actually carries.
--
-- Both are optional, and the item is only meaningful when the answer was yes; the check
-- keeps a name from being stored against "no".
ALTER TABLE posts
  ADD COLUMN IF NOT EXISTS purchased boolean,
  ADD COLUMN IF NOT EXISTS purchased_item text;

ALTER TABLE posts
  DROP CONSTRAINT IF EXISTS posts_purchased_item_check;
ALTER TABLE posts
  ADD CONSTRAINT posts_purchased_item_check
  CHECK (purchased_item IS NULL OR (purchased IS TRUE AND length(btrim(purchased_item)) BETWEEN 1 AND 120));
