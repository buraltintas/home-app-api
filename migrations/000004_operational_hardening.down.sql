DROP INDEX IF EXISTS notification_outbox_recovery_idx;
DROP INDEX IF EXISTS notification_outbox_available_idx;
DROP INDEX IF EXISTS notification_outbox_idempotency_uidx;
ALTER TABLE notification_outbox
  DROP CONSTRAINT IF EXISTS notification_outbox_status_check,
  DROP COLUMN IF EXISTS provider_message_id,
  DROP COLUMN IF EXISTS last_error,
  DROP COLUMN IF EXISTS locked_at,
  DROP COLUMN IF EXISTS idempotency_key;

DROP INDEX IF EXISTS stores_text_idx;
CREATE INDEX stores_text_idx ON stores USING gin(
  to_tsvector('simple', coalesce(name,'') || ' ' || coalesce(brand_name,'') || ' ' || coalesce(city,'') || ' ' || coalesce(district,''))
);
