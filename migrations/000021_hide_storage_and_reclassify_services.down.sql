UPDATE store_categories SET active=true WHERE slug='storage';

-- Removed links were classifications proven false, so recreating them on rollback would
-- knowingly restore the defect. The store rows and all community data remain untouched.
SELECT 1;
