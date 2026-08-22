DELETE FROM search_interactions WHERE event_type='call_click';
ALTER TABLE search_interactions DROP CONSTRAINT search_interactions_event_type_check;
ALTER TABLE search_interactions
  ADD CONSTRAINT search_interactions_event_type_check
  CHECK(event_type IN ('result_impression','result_click','store_open','favorite','unfavorite','review_started','review_created','share'));
