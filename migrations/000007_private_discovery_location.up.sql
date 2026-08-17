ALTER TABLE user_private_profiles
  ADD COLUMN discovery_location geography(Point,4326),
  ADD COLUMN discovery_location_source text,
  ADD COLUMN discovery_location_label text,
  ADD COLUMN discovery_location_address text,
  ADD COLUMN discovery_location_place_id text,
  ADD COLUMN discovery_location_accuracy_meters numeric(10,2),
  ADD COLUMN discovery_location_updated_at timestamptz,
  ADD CONSTRAINT user_private_profiles_discovery_location_source_check
    CHECK (discovery_location_source IS NULL OR discovery_location_source IN ('device','manual')),
  ADD CONSTRAINT user_private_profiles_discovery_location_accuracy_check
    CHECK (discovery_location_accuracy_meters IS NULL OR discovery_location_accuracy_meters > 0),
  ADD CONSTRAINT user_private_profiles_discovery_location_consistency_check CHECK (
    (discovery_location IS NULL AND discovery_location_source IS NULL AND discovery_location_label IS NULL
      AND discovery_location_address IS NULL AND discovery_location_place_id IS NULL
      AND discovery_location_accuracy_meters IS NULL AND discovery_location_updated_at IS NULL)
    OR
    (discovery_location IS NOT NULL AND discovery_location_source IS NOT NULL AND discovery_location_updated_at IS NOT NULL
      AND (
        (discovery_location_source = 'manual' AND discovery_location_place_id IS NOT NULL
          AND discovery_location_label IS NOT NULL AND discovery_location_accuracy_meters IS NULL)
        OR
        (discovery_location_source = 'device' AND discovery_location_place_id IS NULL)
      ))
  );
