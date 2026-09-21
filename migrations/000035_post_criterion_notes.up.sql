-- Why a low score was low, kept against the question it answers.
--
-- A one or a two out of five is unanswerable on its own: the shop cannot tell what went
-- wrong and the next reader cannot tell whether it would bother them. The review form now
-- asks for a sentence whenever a heading is scored one or two, and this is where that
-- sentence lives -- keyed by the heading, so the store page can show it beside the score it
-- explains rather than as a paragraph the reader has to match up by hand.
--
-- The notes were briefly folded into the review body as "Heading: sentence" lines. That is
-- lossy: the heading was written in whatever language the reviewer was using, so reading it
-- back means parsing translated text. Keyed storage does not have that problem.
ALTER TABLE posts
  ADD COLUMN IF NOT EXISTS criterion_notes jsonb;

ALTER TABLE posts
  DROP CONSTRAINT IF EXISTS posts_criterion_notes_check;
ALTER TABLE posts
  ADD CONSTRAINT posts_criterion_notes_check
  CHECK (criterion_notes IS NULL OR jsonb_typeof(criterion_notes) = 'object');
