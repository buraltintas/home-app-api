DROP INDEX IF EXISTS feedback_user_messages_idx;
ALTER TABLE feedback DROP CONSTRAINT IF EXISTS feedback_reply_complete;
ALTER TABLE feedback
  DROP COLUMN IF EXISTS replied_by,
  DROP COLUMN IF EXISTS replied_at,
  DROP COLUMN IF EXISTS reply;
