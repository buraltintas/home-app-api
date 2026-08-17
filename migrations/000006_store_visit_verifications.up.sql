CREATE TABLE store_visit_verifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  store_id uuid NOT NULL REFERENCES stores(id) ON DELETE RESTRICT,
  verification_distance_meters numeric(10,2) NOT NULL CHECK(verification_distance_meters >= 0),
  reported_accuracy_meters numeric(10,2) NOT NULL CHECK(reported_accuracy_meters > 0),
  verified_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  consumed_post_id uuid REFERENCES posts(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK(expires_at > verified_at),
  CHECK(consumed_at IS NULL OR consumed_at >= verified_at)
);

CREATE UNIQUE INDEX store_visit_verifications_available_idx
  ON store_visit_verifications(user_id,store_id)
  WHERE consumed_at IS NULL;

CREATE INDEX store_visit_verifications_expiry_idx
  ON store_visit_verifications(expires_at)
  WHERE consumed_at IS NULL;
