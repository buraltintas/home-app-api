# Frontend handoff: Boşa Gezme! API

This document is the implementation contract for React Native and Next.js clients. It was cross-checked against the router, middleware, DTOs, services, migrations, providers, and [`openapi.yaml`](./openapi.yaml). Fixtures in [`frontend-fixtures/`](./frontend-fixtures/) are valid API payloads, not proposed view models.

## 1. Product and primary loop

Boşa Gezme! is a social discovery and physical-store review platform focused on home/living stores. The canonical product domain is `bosagezme.com`; the approved source logo is [`docs/brand/bosa-gezme-logo.png`](./brand/bosa-gezme-logo.png).

`Discover → Search → Open Store → Visit Physically → Review → Social Interaction`

Stores do not need to be platform members. Browsing is anonymous; login is required for mutations. Review creation is proximity checked by the backend (500 m by default) using either current coordinates or a single-use proof captured while the user was on site.

## 2. Connection and client headers

| Environment | API origin |
|---|---|
| Local | `http://localhost:8080` with default `HTTP_ADDR=:8080` |
| Staging | Not configured in this repository; inject a frontend server/runtime environment variable. |
| Production | Product/web domain is `https://bosagezme.com`; the Go API deployment origin is not assigned in this repository and must be injected server-side. |

Frontend-relevant backend configuration includes `BFF_SECRETS`, `DEFAULT_LOCALE`, token/OTP TTLs, `GOOGLE_CLIENT_ID`, `MEDIA_MAX_BYTES`, `OBJECT_STORAGE_UPLOAD_TTL`, `STORE_REVIEW_RADIUS_METERS`, `STORE_LOCATION_MAX_ACCURACY_METERS`, `STORE_VISIT_PROOF_TTL`, and `VISITOR_RETENTION_DAYS`. Never copy backend provider keys or secrets into public frontend variables.

Common request headers:

| Header | Use |
|---|---|
| `X-BFF-Secret` | Required on every `/v1/*` request. |
| `Authorization: Bearer <access_token>` | Optional on discovery reads; required on protected routes. |
| `X-Locale` | Optional explicit `tr`, `en`, `de`, or `ru`. |
| `Accept-Language` | Locale fallback for anonymous/non-explicit requests. |
| `X-Visitor-Session-ID` | Persistent anonymous UUID for search/history attribution. |
| `X-Client-Type`, `X-Client-Version` | Optional session metadata during auth. |
| `X-Origin-Search-ID`, `X-Origin-Search-Result-ID` | Optional favorite/unfavorite attribution. |

## 3. BFF and client security

The BFF middleware wraps all `/v1/*` routes, including auth and public reads. It performs constant-time matching against every comma-separated value in `BFF_SECRETS`, allowing current/previous secret rotation. Missing or invalid `X-BFF-Secret` returns HTTP 401 with code `INVALID_CLIENT`.

For web, the required topology is `Browser → Next.js server route/action/BFF → Go backend`. `BFF_SECRET` (the Next-side name may be singular) stays server-only; the backend canonical setting is plural `BFF_SECRETS` and accepts legacy `BFF_SECRET` only as a fallback. The browser must never receive either value.

The current Go API also expects this header from React Native. A secret embedded in a shipped app can be extracted, so it is only an app-identification/abuse-friction control, not trustworthy authentication. User authorization always comes from bearer tokens. A mobile gateway/attestation exchange is not implemented.

## 4. Access matrix

“Optional auth” means the same response works anonymously, but viewer booleans are populated when a valid bearer token is present. An invalid bearer token returns `INVALID_TOKEN`; omit the header for anonymous use.

| Capability | Access |
|---|---|
| Feed, hybrid/classic search, store detail/reviews, post detail/comments, public profile/posts | Anonymous / optional auth |
| Search interaction | Anonymous with owning visitor ID, or authenticated owning user |
| Request/verify OTP, Google login, refresh | Anonymous |
| Create/delete review, upload media, like, comment, follow, favorite | Authenticated |
| Delete review/comment | Owner only; non-owner is surfaced as not found |
| Own profile/edit/delete, own search history/delete | Authenticated owner |
| Admin routes | None exposed |

## 5. Authentication and sessions

### Email OTP

1. `POST /v1/auth/email/request-code` with `{"email":"ada@example.com"}` returns 202 `{"status":"accepted"}`.
2. Delivering the email is entirely backend-owned.
3. `POST /v1/auth/email/verify-code` with `{"email":"ada@example.com","code":"482193"}` returns a token pair.

Codes are six digits, expire after `OTP_TTL` (default 10 minutes), and allow `OTP_MAX_ATTEMPTS` (default 5). The request endpoint intentionally returns accepted without revealing account existence. Invalid, expired, or attempt-exhausted verification uses `INVALID_CODE`. Rate limits are applied by normalized email, IP, and visitor; HTTP 429 uses `RATE_LIMITED` and `Retry-After: 60`.

### Google and identity merging

Obtain a Google ID token for the configured backend audience, then call `POST /v1/auth/google` with `{"id_token":"<google-id-token>"}`. Failures use `INVALID_GOOGLE_TOKEN`; an unverified email uses `EMAIL_NOT_VERIFIED`. Google and OTP identities with the same trusted, verified, normalized email automatically map to one user. The frontend must not implement account merging.

### Token lifecycle

Auth success is shaped exactly like [`auth-success.json`](./frontend-fixtures/auth-success.json). Access tokens are HS256 bearer JWTs (default 15 minutes) with issuer `https://bosagezme.com` and audience `bosagezme-clients`. Refresh tokens are opaque (default 720 hours) and rotate on every `POST /v1/auth/refresh`; atomically replace both stored tokens. Reuse of an already-rotated refresh token revokes its entire session family and returns `INVALID_REFRESH_TOKEN`. `POST /v1/auth/logout` revokes the current family; `/logout-all` revokes all user sessions. Both return 204.

Recommended lifecycle: login → store tokens in OS secure storage/server-only session → send access token → on an authentication failure attempt one serialized refresh → replace refresh token → if refresh returns `INVALID_REFRESH_TOKEN`, clear session and show sign-in. Do not log tokens or OTPs.

## 6. Locale and content language

Resolution order is: valid explicit `X-Locale` → authenticated user's `preferred_locale` → `Accept-Language` → configured default (`tr`). Region/underscore variants normalize (`tr-TR`, `tr_TR` → `tr`). Unsupported values fall back safely.

API/system error messages, store translations, and `category_labels` are localized. Canonical enums, category slugs, and IDs are never translated. Reviews, comments, and bios remain exactly as authored; `content_language`/`bio_language` are metadata only. Locale fields are `preferred_locale`, `bio_language`, post/comment `content_language`, search `intent.query_language`, anonymous visitor locale, and internal email/push locale.

## 7. Responses and errors

Successes are endpoint-specific JSON or an empty 204; there is no universal success envelope. Errors are always:

```json
{"error":{"code":"AUTH_REQUIRED","message":"Authentication is required"},"request_id":"01J5ABCDEF..."}
```

Messages can be localized; branch UI logic only on `code`. Unknown JSON fields, malformed bodies, and oversized bodies are rejected.

| Code | UI action |
|---|---|
| `INVALID_CLIENT` | BFF/mobile configuration failure; do not ask user to retry credentials. |
| `AUTH_REQUIRED` | Show sign-in. |
| `INVALID_TOKEN` | Try refresh once, otherwise sign out. |
| `INVALID_REFRESH_TOKEN` | Clear session and sign in again. |
| `INVALID_CODE` | Keep OTP screen; explain invalid/expired code and permit a rate-aware resend. |
| `INVALID_GOOGLE_TOKEN`, `EMAIL_NOT_VERIFIED` | Restart Google auth or explain account email requirement. |
| `RATE_LIMITED` | Disable/retry using `Retry-After` (currently 60 seconds). |
| `INVALID_INPUT` | Show field/general validation feedback. |
| `STORE_NOT_FOUND`, `POST_NOT_FOUND`, `COMMENT_NOT_FOUND`, `USER_NOT_FOUND`, `SEARCH_NOT_FOUND`, `MEDIA_NOT_FOUND` | Show not-found state. |
| `STORE_VISIT_NOT_VERIFIED` | Explain the user must be physically near the store. |
| `VISIT_VERIFICATION_INVALID` | Discard the expired/used proof and request a new on-site verification or fresh current location. |
| `USERNAME_TAKEN` | Request another username. |
| `CANNOT_FOLLOW_SELF` | Suppress self-follow UI. |
| `DUPLICATE_MEDIA`, `INVALID_MEDIA` | Correct media selection/readiness before retry. |
| `MEDIA_STATE_CONFLICT` | Do not finalize twice; refresh upload state/start over. |
| `MEDIA_UPLOAD_INCOMPLETE`, `MEDIA_UPLOAD_MISMATCH` | Re-upload binary with exact declared bytes/type. |
| `INVALID_EXTERNAL_STORE` | Google result can no longer be materialized. |
| `INVALID_LOCATION` | Discard the stale/non-geographic manual candidate and let the user choose again. |
| `PLACES_NOT_CONFIGURED`, `PLACES_UNAVAILABLE` | Show provider-degraded state; do not call Google directly. |
| `SEARCH_ATTRIBUTION_INVALID` | Drop stale/foreign attribution IDs and continue core action where appropriate. |
| `INTERNAL_ERROR` | Generic retry/support state; raw provider/DB errors are never exposed. |

## 8. Complete route reference

Every row below requires BFF unless explicitly marked “No”. `optional` auth accepts anonymous or bearer.

| Method/path | Auth | Request / response / pagination | Important errors |
|---|---|---|---|
| `GET /health` | No BFF | `{"status":"ok"}` | — |
| `GET /ready` | No BFF | `{"status":"ready"}` or 503 | — |
| `GET /metrics` | No BFF; metrics token may apply | Prometheus text | 401 |
| `GET /media/{id}` | No BFF | 307 to renderable local/signed object URL; only attached ready media | `MEDIA_NOT_FOUND` |
| `POST /v1/auth/email/request-code` | none | email → 202 accepted | `INVALID_INPUT`, `RATE_LIMITED` |
| `POST /v1/auth/email/verify-code` | none | email + six-digit code → token pair | `INVALID_CODE` |
| `POST /v1/auth/google` | none | `id_token` → token pair | Google codes above |
| `POST /v1/auth/refresh` | none | `refresh_token` → rotated pair | `INVALID_REFRESH_TOKEN` |
| `POST /v1/auth/logout`, `/logout-all` | required | empty → 204 | auth errors |
| `GET /v1/feed` | optional | `cursor`, `limit` → `{items,next_cursor}`; default 20/max 50 | invalid cursor |
| `GET /v1/locations/search` | optional | manual city/district/neighborhood text → geographic Google candidates; default 5/max 10 | input/rate/provider errors |
| `POST /v1/search` | optional | search request → structured results; no pagination, max 30 | input/rate/provider errors |
| `GET /v1/stores/search`, `/nearby` | optional | `q`, coordinate pair, radius, limit → `{search_id,visitor_session_id,items}`; default 20/max 50 | `INVALID_INPUT` |
| `POST /v1/stores/resolve-external` | required | `{provider:"google",place_id}` → `{id}` | provider errors |
| `GET /v1/stores/{id}` | optional | optional coordinate pair → `{store,recent_posts}` (5 posts) | not found/input |
| `GET /v1/stores/{id}/posts` | optional | limit → `{items}`; default 20/max 50, no next page token | — |
| `POST /v1/stores/{id}/visit-verifications` | required | fresh mobile coordinates + horizontal accuracy → single-use expiring visit proof | proximity/input/rate errors |
| `POST`, `DELETE /v1/stores/{id}/favorite` | required | empty → 204, idempotent | not found/auth |
| `POST /v1/posts` | required | review payload → `{id}` | media/proximity/input |
| `GET /v1/posts/{id}` | optional | post object | not found |
| `DELETE /v1/posts/{id}` | owner | empty → 204 | not found |
| `POST`, `DELETE /v1/posts/{id}/like` | required | empty → 204, idempotent | not found/auth |
| `GET /v1/posts/{id}/comments` | optional | limit → `{items}` oldest-first; default 50/max 100 | — |
| `POST /v1/posts/{id}/comments` | required | `{text,content_language?}` → `{id}` | input/not found |
| `DELETE /v1/comments/{id}` | owner | empty → 204 | not found |
| `GET /v1/users/{id}` | optional | public profile | not found |
| `GET /v1/users/{id}/posts` | optional | limit → `{items}`; default 20/max 50 | — |
| `POST`, `DELETE /v1/users/{id}/follow` | required | empty → 204, idempotent | `CANNOT_FOLLOW_SELF` |
| `POST /v1/searches/{id}/interactions` | owning user/visitor | event payload → 204, idempotent when key supplied | attribution/not found |
| `GET`, `PATCH /v1/me` | required | private profile / partial update → private profile | input/username conflict |
| `PUT`, `DELETE /v1/me/discovery-location` | required | persist current/manual private discovery location → private profile; clear → 204 | input/rate/provider errors |
| `DELETE /v1/me` | required | anonymize/delete → 204 | auth |
| `GET /v1/me/searches` | required | limit → `{items}` default 30/max 100 | auth |
| `DELETE /v1/me/searches` | required | all → 204 | auth |
| `DELETE /v1/me/searches/{id}` | owner | one → 204 | not found |
| `POST /v1/media/uploads` | required | declaration → upload authorization | media/input |
| `POST /v1/media/{id}/complete` | owner | dimensions → 204 | media state/upload errors |

Exact request/response schemas and status responses are in OpenAPI.

## 9. Feed, posts, and media

`GET /v1/feed?limit=20&cursor=...` returns newest-first posts when no location is supplied. With a valid paired `latitude`/`longitude`, it returns reviews ordered by the viewer's distance to each store, nearest first. Feed coordinates are request-scoped and not persisted. Explain the nearby-feed benefit before requesting location; if permission is denied, use the chronological feed. Treat `cursor` as opaque, keep the same location mode and coordinates for subsequent pages, and note that `next_cursor` is `""` when done (there is no `has_more`). Compare anonymous and authenticated fixtures: viewer flags are false anonymously and account-specific when authenticated.

A post contains IDs for post/author/store; original text and optional `content_language`; rating; backend-derived `visit_verified` and visit-verification `distance_meters`; timestamp; denormalized author/store display fields; ordered `media`; counts; and viewer liked/followed/favorited booleans. Location-aware feed responses additionally include `store_distance_meters`, which is the viewer-to-store distance and must not be confused with visit verification. `media[].url` is backend-origin-relative (for example `/media/<uuid>`), so resolve it against the Go API origin, not the Next BFF page origin unless that route is proxied. See [`post-detail.json`](./frontend-fixtures/post-detail.json).

### Location choice on web and mobile

Do not request location at app/page launch. When the user first chooses nearby feed, nearby stores, maps, or location-aware search, show short localized benefit copy and two equal, explicit actions: **Use current location** and **Choose a location**. Also provide **Not now**; it keeps the chronological/non-location experience usable.

- Web: request browser geolocation only from the user's action. Browser code sends browser-produced coordinates only through the same-origin BFF. The BFF forwards them to feed/search/store endpoints and must not expose backend credentials.
- Mobile: request platform “while using the app” permission only from the user's action. Do not request background location. Forward native-location coordinates through the isolated API transport.
- Denied, restricted, unavailable, or deliberately skipped permission: immediately keep the product usable and show manual location selection. Never repeatedly trigger the system permission dialog.
- Manual entry has exactly one user-editable text field. The user types a human place name such as `Kadıköy`, `Çankaya` or `Berlin Mitte`; never show latitude/longitude inputs. Debounce after at least two characters, call `GET /v1/locations/search?q=<text>&limit=5`, and render only candidate place name/address plus provider attribution. See [`location-search.json`](./frontend-fixtures/location-search.json). After selection, call `PUT /v1/me/discovery-location` with only `{"source":"manual","place_id":"..."}`. The backend re-fetches and verifies the geographic place and stores its coordinates privately; never copy candidate coordinates into the manual-selection request, render numeric fields, or ask the user to type them.
- Keep a visible, keyboard/screen-reader accessible location control showing the active human-readable place name, with Change and Clear actions. `GET /v1/me.discovery_location` is the authenticated source of truth and is private; `source=manual` supplies `label`/`address`, while `source=device` should display localized “Current location” copy. Coordinates remain transport-only and must not appear in UI copy, analytics, crash reports, or logs. `DELETE /v1/me/discovery-location` clears the preference.
- Mobile current-location mode: persist OS coordinates and horizontal accuracy with `source=device` while the app is foregrounded. Update after meaningful movement (recommended: at least 250 m) or when the last persisted fix is at least 15 minutes old; do not request background location. A manual selection is sticky: ordinary device updates use `override_manual=false` and cannot overwrite it. Send `override_manual=true` only for the first device update after the user explicitly taps **Use current location**. Manual changes persist immediately.
- Web may persist a current fix after an explicit browser-geolocation action, but it must not continuously poll. Re-request only from a user action or while an already active nearby experience reasonably needs a refreshed fix.
- Manual location is discovery context only. It must never be submitted to `/visit-verifications` or used as evidence for a review. Review verification always requires fresh native/browser device geolocation and horizontal accuracy while the user is physically present.

All visible copy and location/error states must exist in `tr`, `en`, `de`, and `ru`. Cover loading suggestions, no geographic matches, provider unavailable, permission prompt, denied/restricted permission, location unavailable, active manual location, and clear/change states.

Create with:

```json
{"store_id":"...","text":"Işık çok sıcak ve güzel.","rating":5,"latitude":40.9901,"longitude":29.0292,"accuracy_meters":18.4,"media_ids":["..."],"origin_search_id":"...","origin_search_result_id":"...","content_language":"tr"}
```

`text` is 3–5000 Unicode characters; rating is 1–5; at most 10 unique ready media IDs owned by the caller. Do not send `visit_verified`: the server calculates distance and rejects beyond the configured radius with HTTP 422 `STORE_VISIT_NOT_VERIFIED`. Send mobile horizontal accuracy when using current coordinates; accuracy contributes conservatively to the proximity boundary. Deleting is owner-only and soft-deletes.

To let a user write later about a real visit, verify while they are physically present:

```json
POST /v1/stores/<store-id>/visit-verifications
{"latitude":40.9901,"longitude":29.0292,"accuracy_meters":18.4}
```

The response contains `id`, `store_id`, backend-computed `distance_meters`, `verified_at`, and `expires_at`; see [`visit-verification.json`](./frontend-fixtures/visit-verification.json). The server uses receipt time and stores distance/accuracy, not raw device coordinates. Keep the proof ID in secure app storage. A later review replaces coordinates with `"visit_verification_id":"..."`. A proof is bound to its authenticated user and store, expires after `STORE_VISIT_PROOF_TTL` (default 30 days), and is consumed atomically by one review. Never send both current coordinates and a proof. Client-supplied historical timestamps or historical coordinates are not accepted as evidence.

Media upload sequence:

1. `POST /v1/media/uploads` with `{"mime_type":"image/webp","size_bytes":184532}`.
2. Receive `{id,upload:{storage_key,upload_url,headers,expires_at}}`. Upload raw bytes directly to `upload_url`, copying every returned header exactly. Do not send the Go API bearer/BFF headers to storage.
3. Before `expires_at` (default authorization TTL 15 minutes), `POST /v1/media/{id}/complete` with pixel `width`/`height` (1–20000).
4. Only after 204 include the media `id` in `media_ids` when creating the review.

Supported types are JPEG, PNG, WebP; default maximum is 10 MiB per file. Finalization verifies object size and MIME. Post media uses a stable API redirect while the private storage URL is minted only when read.

## 10. Stores, platform data, and Google

A physical location is the store identity; `brand_name` is only descriptive. The exposed store model contains `id`, localized `name`, `slug`, optional `brand_name`, address/city/district, coordinates, optional distance, canonical categories, localized category labels/description, `platform` stats, viewer favorite/review booleans, and (on detail) optional external sources. Country, phone, website, cover image, and merchant/claim state are not exposed.

Platform statistics are distinct from Google statistics:

| Data | Meaning |
|---|---|
| `platform.average_rating`, `rating_count`, `review_count`, `favorite_count`, `post_count` | Boşa Gezme! community only. Rating/review/post counts currently advance together for created reviews. |
| `google.rating`, `rating_count` | Google provider only; never merged into platform rating. |

Hybrid search `source` is `internal`, `google`, or `google+platform`. An internal/platform result has `id` and `platform`; a Google-only result omits `id` and has `google`; an enriched result has both. Results with at least one proximity-verified Boşa Gezme! review rank ahead of provider-only results; `platform` and `google` ratings must still be labelled and rendered separately. Before opening a platform detail/favoriting/reviewing a Google-only result, authenticated clients call `/stores/resolve-external`, then use the returned internal ID. [`store-google-only.json`](./frontend-fixtures/store-google-only.json) is intentionally a search response fragment because no Google-only store-detail endpoint exists.

Favoriting a store and liking a post are separate idempotent relationships. Their POST/DELETE routes return 204; viewer booleans come from later reads. Counts change only when the relationship actually changes.

## 11. Comments, follows, and profiles

Comments are one-level only, oldest-first, and have no cursor. They contain `body` (not `text`), optional language metadata, author display fields, and timestamp. Only the owner can delete; non-owners receive the same not-found behavior.

Follows are unique/idempotent. Self-follow returns HTTP 422 `CANNOT_FOLLOW_SELF`. Public profiles expose only `id`, username/display name/avatar, bio and optional `bio_language`, city, follower/following counts, and post count. `viewer_follows_author` is present on post objects, not public profile.

`GET /v1/me` adds email, preferred locale, private discovery location, and private personalization: relationship status, children flag/age ranges, housing status, occupation, age range, home style interests. The discovery location belongs in the profile settings area with readable current/manual state and Change/Clear actions; never show its numeric coordinates. These fields and search history must never appear on another user's profile. PATCH is partial. Username is 3–30 ASCII letters/digits/underscore and unique. Account deletion revokes sessions, removes private location/relationship/search data, soft-deletes content, and anonymizes the profile.

## 12. Search contract

The preferred discovery endpoint is `POST /v1/search`:

```json
{"query":"Kadıköy'de modern avize mağazaları","latitude":40.9901,"longitude":29.0292,"radius_meters":10000}
```

Query is 2–500 Unicode characters. Coordinates are optional but must be supplied as a pair; radius defaults to 10,000 and is 100–50,000 m. Locale comes from headers, not the body. Search has no pagination and returns at most 30 results.

The backend, not frontend, optionally asks OpenAI to parse intent, runs deterministic fallback parsing, queries internal/PostGIS and optional Google Places, deduplicates/enriches, ranks, and returns structured data. The frontend never calls OpenAI or Places and never receives their keys/prompts.

`intent` fields are exactly `scope`, `query_language`, `normalized_query`, `store_name`, `location_text`, `categories`, `product_terms`, `style_terms`, `price_intent`, `attributes`, `sort_preference`, `semantic_terms`. `scope` is `home_living`, `out_of_scope`, or `unclear`. `store_name` contains an extracted home/living store or brand name and is empty when absent. A bare store-name query such as `IKEA` or `Madame Coco`, including a name plus location, is a valid `home_living` search even without product/category terms; see [`search-store-name.json`](./frontend-fixtures/search-store-name.json). Canonical values are language-independent. Examples: Turkish `modern avize`, English `modern chandelier`, German `moderner Kronleuchter`, and Russian `современная люстра` all map to locale-specific `query_language` plus compatible canonical `lighting`/`chandelier` intent. Returned intent may be rendered as filter chips but must not be treated as a user-authored translation.

Every result always has `search_result_impression_id`, source, name, address, coordinates, and category array. `id`, city/district/distance, `platform`, and `google` depend on source/context. Array order is ranking; no separate rank field exists. Full mixed and zero-result examples are fixtures.

For an explicit unrelated request such as a tyre shop, `scope` is `out_of_scope`; greetings, chitchat, and unrecognizable text use `unclear`. Both return HTTP 200 with `results: []` and a localized `guidance` object containing `code: HOME_LIVING_ONLY`, the reason, a message, and exactly two example home searches. Example pairs rotate server-side, so clients must render the returned strings instead of hard-coding them. Internal and Google providers are not called for these requests. See [`search-out-of-scope.json`](./frontend-fixtures/search-out-of-scope.json).

For `home_living`, parsed intent drives internal and Google Places queries concurrently to reduce latency. Indirect requests such as “çeyiz almak istiyorum” and “nevresim takımı lazım” are valid home/living searches, not guidance states.

`fallback_state` is omitted normally and may be `ai_unavailable_or_invalid`, `places_unavailable`, or a comma-joined combination. Absence of configured AI uses deterministic parsing without necessarily emitting an AI fallback marker; UI should rely only on the returned field.

Classic `GET /stores/search` and `/nearby` query internal data and still return a `search_id`; they do not return per-result impression IDs, so use hybrid POST search for full per-result instrumentation.

### Search history, visitor identity, and interactions

Both anonymous and authenticated searches are recorded. If an anonymous request has no `X-Visitor-Session-ID`, the backend creates a UUID and returns `visitor_session_id`; persist it in secure/local app storage (not as an auth credential) and send it on later `/v1` calls. Default retention is 180 days. On login, searches for the supplied visitor ID are associated with the user. Only an authenticated user can read/delete their own history.

Hybrid result impressions are stored automatically server-side. For client actions call `POST /v1/searches/{search_id}/interactions` with:

```json
{"search_result_id":"<search_result_impression_id>","event_type":"result_click","idempotency_key":"result-click:<uuid>"}
```

Allowed events: `result_impression`, `result_click`, `store_open`, `favorite`, `unfavorite`, `review_started`, `review_created`, `share`. Use the result's `search_result_impression_id` as `search_result_id`; include a stable idempotency key. The endpoint verifies ownership by bearer user or visitor header. Favorites can instead carry both origin headers; review creation carries both origin IDs in its body. Attribution defaults to a 72-hour window.

History items are `{id,raw_query,intent,created_at,result_count}`. `GET /me/searches` has limit-only truncation, not a continuation token.

## 13. Pagination, dates, taxonomy, and enums

| Resource | Pagination |
|---|---|
| Feed | Cursor, default 20/max 50; opaque `next_cursor`, empty when complete. |
| Store/user posts | Limit-only, default 20/max 50. |
| Comments | Limit-only, default 50/max 100, oldest-first. |
| Search | No pagination, max 30 hybrid results. |
| Search history | Limit-only, default 30/max 100. |

JSON timestamps are RFC 3339 instants (normally UTC from Go/PostgreSQL `timestamptz`). The frontend owns calendar/relative-time formatting for `tr/en/de/ru`. Reporting-day aggregation uses `Europe/Istanbul` but does not change API instant semantics.

Canonical category keys: `furniture`, `home_textile`, `lighting`, `decoration`, `kitchenware`, `bathroom`, `carpet`, `curtain`, `bedding`, `tableware`, `storage`, `home_accessories`, `household`. Use `category_labels` for localized display.

Frontend-relevant unions:

| Type | Values |
|---|---|
| locale/content/query/bio language | `tr`, `en`, `de`, `ru` |
| search source | `internal`, `google`, `google+platform` |
| external provider | `google` |
| price intent | empty, `budget`, `midrange`, `premium` |
| sort preference | empty, `relevance`, `distance`, `rating`, `popularity` |
| housing status | `owner`, `renter`, `living_with_family`, `other`, or null |
| media MIME | `image/jpeg`, `image/png`, `image/webp` |
| client type | Free-form session metadata; recommended `ios`, `android`, `web` (not validated as an enum). |
| interaction event | Values listed in the interaction section. |
| internal push platform | `ios`, `android`, `web` (no HTTP contract yet). |

## 14. Email, push, Next.js, and React Native responsibilities

Email generation, OTP delivery, retry, and provider tracking are backend responsibilities. The frontend only requests, verifies, and rate-aware resends.

Push tables/services support device platform/token, locale, preferences, templates, and outbox processing internally, but **no push device/preference HTTP route exists**. Frontends cannot register/update/delete a token yet; do not build a network integration against an invented endpoint.

Next.js should proxy `/v1` calls server-side, inject its server-only secret, forward bearer token, `X-Locale`/`Accept-Language`, visitor ID, origin attribution headers, status, error body, `Retry-After`, and request ID without rewriting stable error codes. Keep refresh tokens in an appropriate secure server-side/HTTP-only session design; never expose the backend BFF secret to browser code.

React Native should use secure token storage, serialize refreshes, persist visitor UUID and unconsumed visit-proof IDs, forward device locale, implement and synchronize the private current/manual profile location choice above, explain the review/nearby benefit immediately before asking location permission, send fresh coordinates plus horizontal accuracy for current review verification or on-site visit verification, and follow the direct-upload sequence. A persisted discovery location is never proof of presence. Treat the embedded BFF header as public/recoverable and never as user authentication.

## 15. Backend-supported frontend states

| Resource | States to model |
|---|---|
| Store | platform-reviewed; no community reviews (`review_count=0`); Google-only search result needing resolution; enriched platform+Google; viewer favorited/not; viewer reviewed/not. |
| Post | liked/not, author followed/not, store favorited/not, media/no media. Published posts are visit-verified; an unverified published state is not produced. |
| User | anonymous viewer, authenticated own profile, authenticated other profile; following state is available in post context. |
| Search | product/category intent, bare store-name intent, results, zero results, Google-only result, provider fallback marker, provider hard error, persisted visitor. |
| Auth | signed out, active access token, refresh in flight, irrecoverably expired/revoked. |
| Upload | authorization created, binary uploading, finalize pending, ready, expired/mismatch. |

## 16. Current limitations and non-contracts

- No push registration/preferences HTTP API or active frontend notification flow.
- No merchant accounts/store claiming or admin HTTP API.
- No nested comments, collections, or review translation.
- Store phone, website, country, cover image, and opening hours are not exposed.
- Sending JSON `null` for nullable scalar profile fields is currently a no-op (the PATCH decoder cannot distinguish it from omission); clear text fields with `""` where allowed, while relationship/housing/occupation/age scalars cannot currently be reset to SQL null.
- Limit-only post/comment/history lists cannot fetch beyond the requested maximum with a continuation token.
- Google-only results require authentication and materialization before platform detail/social actions.
- Classic search lacks per-result impression IDs.
- Static mobile BFF secret is extractable; mobile attestation/gateway exchange is not implemented.
- Staging/production origins are deployment-owned and absent from the repository.

These are factual boundaries, not implied roadmap commitments.

## 17. Security rules

Frontend must never expose the BFF secret in browser JavaScript; send OpenAI/Google Places/storage credentials; log tokens or OTPs; calculate or submit trusted `visit_verified`; expose another user's private profile/search history; fabricate or merge Google and platform ratings; infer authorization from viewer booleans; or treat visitor ID as authentication.

## 18. Integration checklist

- [ ] API origin is environment-injected; browser traffic goes through Next BFF.
- [ ] Every `/v1` backend call receives BFF header server-side/current mobile adapter.
- [ ] Anonymous and authenticated feed fixtures render, including relative media URLs.
- [ ] Anonymous search persists and resends visitor ID.
- [ ] Mixed/internal/Google-only/zero/fallback search states render.
- [ ] Google-only store resolves before platform detail/social action.
- [ ] OTP and Google auth work; token refresh is serialized and rotated token replaces old token.
- [ ] Logout and refresh-reuse failure clear local session.
- [ ] Favorite/like/follow/comment/review prompt sign-in when anonymous.
- [ ] Nearby discovery offers current location, manual location, and a usable permission-denied fallback.
- [ ] Review obtains fresh device location and never reuses manual discovery coordinates for verification.
- [ ] Direct media upload sends exact headers, finalizes, then attaches IDs.
- [ ] Search/result IDs and idempotency keys propagate to interaction/attribution calls.
- [ ] `tr/en/de/ru` locale is forwarded; canonical values remain untranslated.
- [ ] Public UI never consumes private `/me` fields for other users.
- [ ] Push integration remains disabled until routes exist.

## 19. Contract artifacts

- Machine-readable contract: [`docs/openapi.yaml`](./openapi.yaml)
- Mock payloads: [`docs/frontend-fixtures/`](./frontend-fixtures/)
- Runtime defaults (not public-client configuration): [`.env.example`](../.env.example)

When code and examples appear to disagree, OpenAPI and this document describe the intended public contract; file a backend contract defect rather than silently inventing a client field.
