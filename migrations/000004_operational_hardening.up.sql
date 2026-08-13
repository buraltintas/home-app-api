DROP INDEX IF EXISTS stores_text_idx;
CREATE INDEX stores_text_idx ON stores USING gin(
  to_tsvector('simple', coalesce(name,'') || ' ' || coalesce(brand_name,'') || ' ' || coalesce(description,'') || ' ' || coalesce(city,'') || ' ' || coalesce(district,''))
);

ALTER TABLE notification_outbox
  ADD COLUMN idempotency_key text,
  ADD COLUMN locked_at timestamptz,
  ADD COLUMN last_error text,
  ADD COLUMN provider_message_id text,
  ADD CONSTRAINT notification_outbox_status_check CHECK(status IN ('pending','processing','sent','failed'));

CREATE UNIQUE INDEX notification_outbox_idempotency_uidx
  ON notification_outbox(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX notification_outbox_available_idx
  ON notification_outbox(available_at) WHERE status IN ('pending','failed');
CREATE INDEX notification_outbox_recovery_idx
  ON notification_outbox(locked_at) WHERE status='processing';
