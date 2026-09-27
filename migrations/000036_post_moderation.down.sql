DROP TABLE IF EXISTS post_moderation;
DROP INDEX IF EXISTS posts_held_idx;
ALTER TABLE posts DROP COLUMN IF EXISTS moderation;
