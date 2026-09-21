ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_criterion_notes_check;
ALTER TABLE posts DROP COLUMN IF EXISTS criterion_notes;
