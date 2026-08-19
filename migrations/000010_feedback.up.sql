-- Feedback people send us about the product itself. Deliberately separate from posts and
-- comments: those are about a store and are public, this is a private message to us.
CREATE TABLE feedback (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  visitor_session_id uuid,
  kind text NOT NULL CHECK (kind IN ('suggestion','problem','praise','other')),
  message text NOT NULL CHECK (length(btrim(message)) BETWEEN 5 AND 4000),
  -- Optional, and only so we can reply. Nulled with the account it belongs to.
  contact_email citext,
  locale supported_locale NOT NULL DEFAULT 'tr',
  status text NOT NULL DEFAULT 'new' CHECK (status IN ('new','read','handled')),
  created_at timestamptz NOT NULL DEFAULT now(),
  handled_at timestamptz
);
CREATE INDEX feedback_created_idx ON feedback(created_at DESC);
CREATE INDEX feedback_status_idx ON feedback(status) WHERE status <> 'handled';
CREATE INDEX feedback_user_idx ON feedback(user_id) WHERE user_id IS NOT NULL;
