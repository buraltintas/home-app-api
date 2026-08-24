-- The removed categories came from incorrect inference, not reference data. Reintroducing
-- them on rollback would recreate the production defect, so this data correction is a no-op.
SELECT 1;
