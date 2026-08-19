ALTER TABLE stores ADD COLUMN is_premium boolean NOT NULL DEFAULT false;

-- Partial: premium is the rare case, and the ranking query only ever asks for the true side.
CREATE INDEX stores_premium_idx ON stores(is_premium) WHERE is_premium AND deleted_at IS NULL;

-- Every privileged change writes one row here, in the same transaction as the change
-- itself, so the record cannot disagree with what actually happened. The actor is kept
-- even if that account is later deleted, because an audit trail that erases who acted is
-- not an audit trail; only the reference is dropped.
CREATE TABLE admin_actions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  actor_email citext NOT NULL,
  action text NOT NULL,
  target_type text NOT NULL CHECK (target_type IN ('store','user','post')),
  target_id uuid NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_actions_recent_idx ON admin_actions(created_at DESC);
CREATE INDEX admin_actions_target_idx ON admin_actions(target_type, target_id, created_at DESC);
