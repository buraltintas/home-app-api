-- The catalogue becomes something this product owns and maintains, rather than a cache of
-- somebody else's index. Three groups of tables: who the chains are, what an import run
-- did, and what we know about a store beyond its address.

-- ---------------------------------------------------------------------------
-- Brands. The registry of chains whose own published store lists we read.
-- ---------------------------------------------------------------------------
CREATE TABLE brands(
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug                 text NOT NULL UNIQUE,
  name                 text NOT NULL,
  website              text,
  logo_media_id        uuid UNIQUE REFERENCES media(id) ON DELETE SET NULL,
  -- 1 = the largest national chains, imported first so that smaller chains and
  -- independents match onto rows that are already settled. Ordering is part of
  -- deduplication, not a display preference.
  tier                 smallint NOT NULL DEFAULT 3 CHECK (tier BETWEEN 1 AND 3),
  locator_kind         text NOT NULL DEFAULT 'none' CHECK (locator_kind IN ('none','json','html')),
  locator_config       jsonb NOT NULL DEFAULT '{}',
  -- What this chain sells. A bed brand is not a decoration shop, and the categories a
  -- store carries should come from what the brand is, not from the words in its sign.
  category_profile     text[] NOT NULL DEFAULT '{}',
  store_count_estimate integer,
  active               boolean NOT NULL DEFAULT true,
  last_imported_at     timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX brands_active_tier_idx ON brands(tier, slug) WHERE active;

ALTER TABLE stores ADD COLUMN brand_id uuid REFERENCES brands(id) ON DELETE SET NULL;
CREATE INDEX stores_brand_idx ON stores(brand_id) WHERE deleted_at IS NULL;

-- Where this row came from, and whether anything still vouches for it.
--
-- 'legacy' is the honest name for a row imported from a provider we no longer talk to: the
-- name and address are still here, but nothing can refresh them, so data_verified_at is
-- null until a brand's own list confirms the store.
ALTER TABLE stores ADD COLUMN source_kind text NOT NULL DEFAULT 'legacy'
  CHECK (source_kind IN ('brand_locator','admin','user','legacy'));
ALTER TABLE stores ADD COLUMN data_verified_at timestamptz;
CREATE INDEX stores_unverified_idx ON stores(city) WHERE deleted_at IS NULL AND data_verified_at IS NULL;

-- The name folded for comparison: lowercased, stripped of diacritics and punctuation, and
-- with spaces removed. Maintained in Go by internal/textnorm, because "KADIKÖY" and
-- "Kadıköy" are only the same word under Turkish rules that Postgres does not apply.
ALTER TABLE stores ADD COLUMN compact_name text NOT NULL DEFAULT '';
CREATE INDEX stores_compact_name_gin_idx ON stores USING gin (compact_name gin_trgm_ops) WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- Import runs. Fetching and matching are separate jobs on purpose: matching has to be
-- re-runnable, and reviewable, without asking the source for the same page again.
-- ---------------------------------------------------------------------------
CREATE TABLE store_import_runs(
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  brand_id    uuid NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  applied     boolean NOT NULL DEFAULT false,
  fetched     integer NOT NULL DEFAULT 0,
  inserted    integer NOT NULL DEFAULT 0,
  updated     integer NOT NULL DEFAULT 0,
  review      integer NOT NULL DEFAULT 0,
  skipped     integer NOT NULL DEFAULT 0,
  error       text
);
CREATE INDEX store_import_runs_brand_idx ON store_import_runs(brand_id, started_at DESC);

CREATE TABLE store_import_records(
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id          uuid NOT NULL REFERENCES store_import_runs(id) ON DELETE CASCADE,
  external_id     text NOT NULL,
  name            text NOT NULL,
  raw             jsonb NOT NULL DEFAULT '{}',
  source_url      text,
  matched_store_id uuid REFERENCES stores(id) ON DELETE SET NULL,
  -- What the matcher decided and why. 'needs_review' is the deliberate middle: a
  -- resemblance too strong to ignore and too weak to merge on, parked where a person can
  -- see it rather than guessed at silently.
  action          text NOT NULL CHECK (action IN ('inserted','updated','needs_review','skipped','unchanged')),
  reason          text,
  similarity      real,
  distance_meters double precision,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX store_import_records_run_idx ON store_import_records(run_id, action);
CREATE INDEX store_import_records_review_idx ON store_import_records(created_at DESC) WHERE action='needs_review';

-- ---------------------------------------------------------------------------
-- What a store carries, and what else is true about it. No interface reads these yet;
-- they are created now because search will be built on top of them, and adding them
-- afterwards costs far more than adding them here.
-- ---------------------------------------------------------------------------

-- Which brands a store sells. This is what answers "a shop in Antalya that carries Yataş",
-- a question the catalogue cannot answer from a store's own name.
CREATE TABLE store_carried_brands(
  store_id   uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  brand_id   uuid NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  source     text NOT NULL CHECK (source IN ('import','admin','user')),
  confidence real NOT NULL DEFAULT 1 CHECK (confidence > 0 AND confidence <= 1),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, brand_id)
);
CREATE INDEX store_carried_brands_brand_idx ON store_carried_brands(brand_id);

-- Opening hours, parking, delivery, assembly, instalments. The keys are a closed list so
-- this cannot decay into a bag of free text nobody can query.
CREATE TABLE store_attribute_keys(
  key        text PRIMARY KEY,
  value_type text NOT NULL CHECK (value_type IN ('text','boolean','hours')),
  label_tr   text NOT NULL
);
INSERT INTO store_attribute_keys(key,value_type,label_tr) VALUES
  ('opening_hours','hours','Çalışma saatleri'),
  ('parking','boolean','Otopark'),
  ('delivery','boolean','Teslimat'),
  ('assembly','boolean','Kurulum'),
  ('instalments','boolean','Taksit'),
  ('wheelchair_access','boolean','Engelli erişimi');

CREATE TABLE store_attributes(
  store_id   uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  key        text NOT NULL REFERENCES store_attribute_keys(key),
  value      text NOT NULL,
  source     text NOT NULL CHECK (source IN ('import','admin','user')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, key)
);

-- The words people actually search with, mapped to the categories we hold. "gardırop" is
-- furniture even though no shop is called that; this table is how a query stops needing a
-- model to say so.
CREATE TABLE product_terms(
  term         text NOT NULL,
  locale       text NOT NULL DEFAULT 'tr',
  category_slug text NOT NULL REFERENCES store_categories(slug) ON DELETE CASCADE,
  source       text NOT NULL DEFAULT 'admin' CHECK (source IN ('seed','admin','user')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (term, locale, category_slug)
);
CREATE INDEX product_terms_category_idx ON product_terms(category_slug);
