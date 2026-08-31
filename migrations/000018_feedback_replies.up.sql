-- Signed-in people can now read an administrator's answer to feedback from their profile.
-- The original message remains the immutable source; the answer is private to its author
-- and the operator surface.
ALTER TABLE feedback
  ADD COLUMN reply text CHECK (reply IS NULL OR length(btrim(reply)) BETWEEN 1 AND 4000),
  ADD COLUMN replied_at timestamptz,
  ADD COLUMN replied_by uuid REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE feedback
  ADD CONSTRAINT feedback_reply_complete CHECK (
    (reply IS NULL AND replied_at IS NULL) OR
    (reply IS NOT NULL AND replied_at IS NOT NULL)
  );

CREATE INDEX feedback_user_messages_idx
  ON feedback(user_id, created_at DESC)
  WHERE user_id IS NOT NULL;
