ALTER TABLE user_private_profiles
  DROP CONSTRAINT IF EXISTS user_private_profiles_discovery_location_consistency_check,
  DROP CONSTRAINT IF EXISTS user_private_profiles_discovery_location_accuracy_check,
  DROP CONSTRAINT IF EXISTS user_private_profiles_discovery_location_source_check,
  DROP COLUMN IF EXISTS discovery_location_updated_at,
  DROP COLUMN IF EXISTS discovery_location_accuracy_meters,
  DROP COLUMN IF EXISTS discovery_location_place_id,
  DROP COLUMN IF EXISTS discovery_location_address,
  DROP COLUMN IF EXISTS discovery_location_label,
  DROP COLUMN IF EXISTS discovery_location_source,
  DROP COLUMN IF EXISTS discovery_location;
