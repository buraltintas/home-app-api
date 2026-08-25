ALTER TABLE stores
  ADD COLUMN cover_media_id uuid UNIQUE REFERENCES media(id) ON DELETE SET NULL;

