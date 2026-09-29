DROP TABLE IF EXISTS comment_moderation;
DROP INDEX IF EXISTS comments_held_idx;
ALTER TABLE comments DROP COLUMN IF EXISTS moderation;
