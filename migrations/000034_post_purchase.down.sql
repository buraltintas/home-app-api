ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_purchased_item_check;
ALTER TABLE posts DROP COLUMN IF EXISTS purchased_item;
ALTER TABLE posts DROP COLUMN IF EXISTS purchased;
