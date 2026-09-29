-- A review that was stopped before it was ever written.
--
-- Until now a review carrying something severe was created, held, and shown to its author
-- as "under review". Two things were wrong with that. The author was told their review was
-- on its way when it was not, and they learned what was wrong with it only by waiting; and
-- the flow carried on to the next step as though nothing had happened.
--
-- So the check now runs before the step advances, and nothing is created. But a refusal
-- that leaves no trace is a refusal nobody can audit or appeal, so the attempt is recorded
-- here instead: the passage, what was found in it, who wrote it and about which shop. It
-- reaches the operator panel; it does not reach the author's own list of reviews, because
-- there is no review.
CREATE TABLE blocked_review_attempts (
  id          uuid PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  store_id    uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  -- Which field the author was filling: the explanation a low score demands, or what they
  -- bought. The wording shown to them is the same either way; the operator wants to know.
  field       text NOT NULL CHECK (field IN ('criterion_note','purchased_item')),
  body        text NOT NULL,
  model       text NOT NULL DEFAULT '',
  findings    jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX blocked_review_attempts_recent_idx ON blocked_review_attempts (created_at DESC);
CREATE INDEX blocked_review_attempts_user_idx ON blocked_review_attempts (user_id, created_at DESC);
