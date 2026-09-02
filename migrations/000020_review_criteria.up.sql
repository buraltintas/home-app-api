-- A review is eight scores now, not a paragraph and a star.
--
-- Nullable on purpose: every review published before this exists without them, and there is
-- no honest value to invent. A review either carries the eight or it is one of the older
-- ones. The check constraints hold for the rows that do carry them.
--
-- The overall rating stays where it was and keeps its meaning for both kinds of row. For a
-- new review it is the average of the eight; for an older one it is what its author gave.
ALTER TABLE posts
  ADD COLUMN rating_availability    smallint CHECK (rating_availability    BETWEEN 1 AND 5),
  ADD COLUMN rating_value           smallint CHECK (rating_value           BETWEEN 1 AND 5),
  ADD COLUMN rating_layout          smallint CHECK (rating_layout          BETWEEN 1 AND 5),
  ADD COLUMN rating_staff_care      smallint CHECK (rating_staff_care      BETWEEN 1 AND 5),
  ADD COLUMN rating_staff_knowledge smallint CHECK (rating_staff_knowledge BETWEEN 1 AND 5),
  ADD COLUMN rating_checkout        smallint CHECK (rating_checkout        BETWEEN 1 AND 5),
  ADD COLUMN rating_returns         smallint CHECK (rating_returns         BETWEEN 1 AND 5),
  ADD COLUMN rating_cleanliness     smallint CHECK (rating_cleanliness     BETWEEN 1 AND 5);
