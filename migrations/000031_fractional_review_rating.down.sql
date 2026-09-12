ALTER TABLE posts ALTER COLUMN rating TYPE smallint USING round(rating)::smallint;
