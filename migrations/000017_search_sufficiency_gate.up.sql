-- Local-first search. The provider used to be asked on every search, in parallel with our
-- own catalogue, whether or not the catalogue could already answer the question. These
-- columns record what the gate saw and what it decided, so the saving and its cost can
-- both be read from the same table the searches themselves live in.
ALTER TABLE searches
  ADD COLUMN local_only boolean NOT NULL DEFAULT false,
  ADD COLUMN gate_reason text,
  ADD COLUMN local_relevance real,
  ADD COLUMN catalogue_coverage int,
  ADD COLUMN local_duration_ms int,
  ADD COLUMN places_duration_ms int,
  -- Where the search happened, taken from the nearest result rather than reverse
  -- geocoded. A national fallback rate would hide the towns where the catalogue is thin,
  -- and those are exactly the places where staying local costs somebody a result.
  ADD COLUMN search_city text,
  ADD COLUMN search_district text;

CREATE INDEX searches_gate_idx ON searches (created_at, local_only);

-- A small sample of the searches we decided not to pay for, asked anyway after the answer
-- had already gone out. Nothing here is shown to anybody and nothing here is imported:
-- this is the only place a provider answer is read without the catalogue learning from
-- it, because a measurement that changes the system it measures is not a measurement.
CREATE TABLE search_shadow_measurements (
  search_id                          uuid PRIMARY KEY REFERENCES searches(id) ON DELETE CASCADE,
  city                               text,
  district                           text,
  local_result_count                 int NOT NULL,
  local_top_score                    real,
  catalogue_coverage                 int,
  provider_result_count              int NOT NULL,
  provider_only_count                int NOT NULL,
  provider_only_max_score            real,
  -- The headline number. A search where this is above zero is one where staying local
  -- cost the person a store that would have outranked everything they were shown.
  provider_only_high_relevance_count int NOT NULL,
  provider_duration_ms               int,
  created_at                         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX search_shadow_measurements_created_at_idx ON search_shadow_measurements (created_at);
