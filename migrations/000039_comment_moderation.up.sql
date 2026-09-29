-- Whether a comment is on the page, and why not when it is not.
--
-- The same rule as reviews, and for the same reason: a comment under a review is read by
-- the same visitors, carries the same legal risk, and until now went straight on to the
-- page unread. Nothing about a comment makes it a smaller risk than the review above it --
-- it is shorter, which is not the same thing.
--
-- Three states, matching posts exactly so there is one vocabulary and not two: published,
-- held while a person has not read it, and removed after a person decided. Removed is kept
-- rather than deleted, so a decision can be looked at again.
--
-- Every comment already written is published, so the default says so and nothing on any
-- page changes when this runs.
ALTER TABLE comments
  ADD COLUMN moderation text NOT NULL DEFAULT 'published'
    CHECK (moderation IN ('published', 'held', 'removed'));

-- The queue a person works through is small and read newest first.
CREATE INDEX comments_held_idx ON comments (created_at DESC) WHERE moderation = 'held';

-- What the check found, kept beside the comment it was about. The same shape as
-- post_moderation, including 'unchecked' for a comment the check could not reach: that one
-- is held exactly like a severe one, because nothing is published that was not read.
CREATE TABLE comment_moderation (
  comment_id uuid PRIMARY KEY REFERENCES comments (id) ON DELETE CASCADE,
  checked_at timestamptz NOT NULL DEFAULT now(),
  model      text,
  verdict    text NOT NULL CHECK (verdict IN ('clean', 'severe', 'unchecked')),
  findings   jsonb NOT NULL DEFAULT '[]'::jsonb,
  error      text,
  decided_by uuid REFERENCES users (id) ON DELETE SET NULL,
  decided_at timestamptz,
  decision   text CHECK (decision IN ('approved', 'removed'))
);
