-- A review's overall score is the average of its eight criteria, and averages have
-- fractions. It was stored as a whole number, so ten stars across eight criteria -- 1.25 --
-- was written as 1, and the store showed 1.0 to somebody who could add their own answers
-- up. The rounding happened before anything could use the real figure: the store average is
-- an average of these, so every store's rating inherited the error.
--
-- The column is widened rather than a second one added: there is one overall score per
-- review and it has always meant the same thing, it was simply being kept in a type that
-- could not hold it.
ALTER TABLE posts ALTER COLUMN rating TYPE numeric(3,2) USING rating::numeric(3,2);
