-- Category links cannot be reconstructed reliably from a business name. Re-importing or
-- manually inventing them during rollback would be less safe than leaving the corrected
-- catalogue rows uncategorised, so this data correction is intentionally irreversible.
DROP INDEX IF EXISTS store_compact_name_trgm_idx;
SELECT 1;
