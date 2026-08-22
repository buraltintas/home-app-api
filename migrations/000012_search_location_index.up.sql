-- Nearby suggestions read searches by coordinate over a rolling window. Without this the
-- lookup is a sequential scan of every search ever made, on a page that renders before
-- anyone has typed anything.
CREATE INDEX IF NOT EXISTS searches_location_time_idx
  ON searches(request_latitude, request_longitude, created_at DESC)
  WHERE request_latitude IS NOT NULL AND request_longitude IS NOT NULL;
