-- Whether a review is on the page, and why not when it is not.
--
-- The product owner's rule: a review that carries any element of a crime -- an insult, a
-- threat, an accusation of a crime stated as fact, a private person's name or number -- is
-- not shown until a person has read it. Two states a visitor can tell apart, published or
-- not; the third, removed, is a person's decision after reading, kept rather than deleted so
-- the decision can be looked at again.
--
-- Every review already written is published, so the default says so and nothing on any page
-- changes when this runs.
ALTER TABLE posts
  ADD COLUMN moderation text NOT NULL DEFAULT 'published'
    CHECK (moderation IN ('published', 'held', 'removed'));

-- The queue a person works through is small and read newest first.
CREATE INDEX posts_held_idx ON posts (created_at DESC) WHERE moderation = 'held';

-- What the check found, kept beside the review it was about. The quotes are the point: a
-- flag without the passage that raised it makes the person reviewing it read the whole
-- review again, and cannot be audited later.
--
-- verdict 'unchecked' is a review the check could not reach -- the model timed out or
-- failed. It is held exactly like a severe one: nothing is published that was not read.
CREATE TABLE post_moderation (
  post_id    uuid PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at timestamptz NOT NULL DEFAULT now(),
  model      text,
  verdict    text NOT NULL CHECK (verdict IN ('clean', 'severe', 'unchecked')),
  findings   jsonb NOT NULL DEFAULT '[]'::jsonb,
  error      text,
  decided_by uuid REFERENCES users (id) ON DELETE SET NULL,
  decided_at timestamptz,
  decision   text CHECK (decision IN ('approved', 'removed'))
);
