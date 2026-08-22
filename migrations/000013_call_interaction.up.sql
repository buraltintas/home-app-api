-- Calling a store from a listing is the point where the product replaces a trip to Google,
-- so it is worth counting as its own kind of interaction rather than as a generic click.
ALTER TABLE search_interactions DROP CONSTRAINT search_interactions_event_type_check;
ALTER TABLE search_interactions
  ADD CONSTRAINT search_interactions_event_type_check
  CHECK(event_type IN ('result_impression','result_click','store_open','favorite','unfavorite','review_started','review_created','share','call_click'));
