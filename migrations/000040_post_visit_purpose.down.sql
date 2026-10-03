ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_visit_purpose_check;
ALTER TABLE posts DROP COLUMN IF EXISTS visit_purpose;
