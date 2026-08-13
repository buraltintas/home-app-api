CREATE INDEX email_codes_ip_time_idx
  ON email_verification_codes(request_ip_hash,created_at DESC)
  WHERE request_ip_hash IS NOT NULL;

CREATE INDEX email_codes_visitor_time_idx
  ON email_verification_codes(visitor_session_id,created_at DESC)
  WHERE visitor_session_id IS NOT NULL;

CREATE INDEX email_outbox_processing_recovery_idx
  ON email_outbox(locked_at)
  WHERE status='processing';
