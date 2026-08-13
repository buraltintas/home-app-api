# Reporting and search analytics definitions

Reporting uses UTC source timestamps and calendar days in `REPORTING_TIMEZONE` (`Europe/Istanbul` by default). A day is converted to explicit UTC `[start,end)` bounds in Go; server-local time is never used.

Core domain tables remain authoritative. `platform_stats` is the cheap single-row current snapshot, `platform_events` preserves non-state transitions, and daily aggregate tables are rebuildable projections. Domain writes and their reporting event/snapshot delta share a PostgreSQL transaction where possible. `idempotency_key` is unique, so replaying the same logical event cannot increment a snapshot twice.

## Metric definitions

- `registered_users_total`: non-deleted users existing at the end of the reporting day.
- `new_users_count`: users whose `created_at` falls within the day.
- `active_users_count`: distinct authenticated users with a successful login, search, verified review, favorite, like, comment, or follow event during the day. Passive anonymous browsing is not an authenticated active-user event.
- `anonymous_visitors_count`: distinct visitor sessions that searched during the day.
- `stores_total`: non-deleted physical stores existing at day end.
- `google_imported_stores_total`: distinct current stores materialized with a Google external source.
- `posts_current_total`: reviews visible at day end. `posts_created_lifetime` includes later-deleted reviews; `posts_deleted_count` is deletion events during the day.
- `new_posts_count`: posts created during the day; `verified_posts_count` is the subset whose server-side proximity check succeeded.
- `location_rejected_post_attempts`: attempts rejected because derived distance exceeded the configured radius. Exact attempted coordinates are never placed in reporting metadata.
- current comments exclude soft-deleted comments. Current likes/follows/favorites are current source rows; creation/removal flow counts come from events.
- `searches_total`: retained canonical search records created before day end. User privacy deletion can intentionally reduce rebuilt retained-history totals.
- `searches_count`: completed canonical searches during the day. `zero_result_searches_count` means `total_result_count=0`; the rate is zero-result/searches.
- authenticated/anonymous search counts are classified by nullable `user_id`; AI and Google counts reflect actual provider use/attempt, not merely configuration.
- search impressions are persisted `search_results` rows. Click/open/favorite/review conversions are explicit `search_interactions`.
- media totals exclude `status='deleted'`. OTP/auth metrics are reporting events and never contain email, token, credential, or OTP values.

Historical current totals for hard-deleted join rows (likes/follows/favorites) are exact from the reporting-launch point through creation/removal events. The global snapshot and rebuild always reproduce the authoritative current totals from source tables.

## Search analytics

Every successful search stores its privacy-rounded location, validated intent, provider classification, duration, result counts, zero-result state, and fallback state. Each returned result receives a stable impression ID, position, source (`internal`, `google`, or `google+platform`), platform-owned metric snapshot, derived distance, backend ranking score, and a short ranking reason. Raw Google payloads and prohibited provider fields are not retained.

`search_query_daily_metrics` aggregates normalized demand using a SHA-256 fingerprint plus normalized representation. `search_intent_daily_metrics` keeps bounded dimensions (category, product, style, location, price intent). `store_search_daily_metrics` supports exposure, engagement, conversion, and high-demand/low-review opportunity reports for both local store IDs and provider references.

Clients should submit the returned `search_result_impression_id` for clicks/opens. Favorite requests may include `X-Origin-Search-ID` and `X-Origin-Search-Result-ID`; review creation may include `origin_search_id` and `origin_search_result_id`. Attribution is ownership checked, store matched, and limited by `SEARCH_ATTRIBUTION_WINDOW_HOURS` (72 by default). Attribution failure never rolls back an already-successful core favorite/review.

Search history is private and owner-only. Deleting retained search history cascades raw impressions/interactions; already aggregated daily metrics remain non-identifying. Anonymous visitor records expire and should be removed by the deployment retention job.

## Operations

The worker recomputes every search day still inside `SEARCH_ATTRIBUTION_WINDOW_HOURS` so late favorites and reviews update the originating query/store aggregates. Repair all projections from retained source data with:

```bash
make rebuild-admin-metrics
```

Future admin code can call `GetPlatformSnapshot`, `GetDailyMetrics`, `GetSearchOverview`, top/zero-result query methods, intent dimensions, most impressed/clicked stores, and high-demand/low-review stores. No admin HTTP endpoint or frontend is exposed yet.
