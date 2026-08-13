# home-app API

Production-oriented Go backend foundation for a Turkey-first social discovery product about real, physical home/living stores.

The product loop is deliberately narrow: **discover → visit → review → help the next person discover**. Stores do not need an owner or platform membership.

## What is implemented

- PostgreSQL/PostGIS schema with explicit reversible migrations and migration tracking
- mandatory, rotation-ready constant-time `X-BFF-Secret` validation for every `/v1/*` request
- anonymous browsing with strict optional-auth semantics (an invalid supplied bearer token is rejected)
- email OTP outbox with hashed codes, encrypted delivery payloads, attempt/expiry controls and a worker
- production Resend email adapter plus a private local development mailbox
- Google ID-token verification and transactionally safe verified-email identity linking
- short access JWTs and hashed, rotating, family-revocable refresh tokens with reuse detection
- public/private profile separation
- PostGIS physical stores, categories, external IDs, aggregate statistics and favorite uniqueness
- proximity-verified 1–5 star reviews, media metadata links, feed cursor pagination, likes, comments and follows
- deterministic Turkish search, optional official OpenAI Go SDK/Responses API structured enrichment, Google Places (New), explicit source-separated ratings, deduplication, ranking and fallback
- user/anonymous search history, impression snapshots and ownership-bound interaction events
- rebuildable platform snapshots, Istanbul-day metrics, query/intent/store search aggregates and bounded conversion attribution
- email and notification outboxes, push and object-storage boundaries
- verified S3/R2-compatible signed image uploads and a real local filesystem upload provider with content sniffing
- request IDs, route-pattern JSON logs, recovery, size/time limits, security headers and bounded in-process rate limiting
- protected Prometheus metrics and optional OTLP/OpenTelemetry tracing
- deterministic Turkish seed data, unit/contract tests and opt-in real PostgreSQL/PostGIS integration tests

The detailed schema, route matrix, identity concurrency strategy, threat model and search flow are in [docs/architecture.md](docs/architecture.md).
Metric semantics and reporting operations are in [docs/reporting.md](docs/reporting.md).

## Requirements

- Go 1.25+
- Docker with Compose (recommended)
- PostgreSQL client tools are optional; migrations run through Go

## Local setup

```bash
cp .env.example .env
set -a; source .env; set +a
docker compose up -d
make migrate
make seed
make run
```

Run the email outbox worker separately:

```bash
set -a; source .env; set +a
make worker
```

Development email is written with `0600` permissions to `.data/mailbox/*.eml`.
This makes the complete OTP flow locally testable without printing OTP values in
application logs. Keep the worker running while authenticating or running the
authenticated smoke test.

Useful checks:

```bash
make test
make test-race
make vet
make lint
make build
make rebuild-admin-metrics
make privacy-maintenance
```

All example secrets are intentionally development-only. Replace them before using any shared environment.

## Minimal API examples

Health probes carry no business data and need no credentials:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Every application endpoint requires the client credential:

```bash
curl -H "X-BFF-Secret: ${BFF_SECRETS%%,*}" http://localhost:8080/v1/feed
```

An anonymous search creates a visitor session when `X-Visitor-Session-ID` is absent. Persist the returned `visitor_session_id` client-side and send it on later analytics/search calls:

```bash
curl -X POST http://localhost:8080/v1/search \
  -H 'Content-Type: application/json' \
  -H "X-BFF-Secret: $BFF_SECRET" \
  -d '{"query":"Kadıköy’de güzel aydınlatma mağazaları","latitude":40.9908,"longitude":29.0277}'
```

Authenticated actions additionally send `Authorization: Bearer <access_token>`.

## Configuration

See [.env.example](.env.example). Important groups are:

- core: `DATABASE_URL`, `HTTP_ADDR`, `APP_ENV`
- client security: `BFF_SECRETS` (comma-separated; `BFF_SECRET` is a legacy single-secret fallback)
- auth: `ACCESS_TOKEN_SECRET`, `OTP_HASH_SECRET`, access/refresh/OTP TTLs, verification attempts, and per-email/IP/visitor request limits
- Google: `GOOGLE_CLIENT_ID`, `GOOGLE_PLACES_API_KEY`
- AI: `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_TIMEOUT`; an empty key cleanly disables AI
- email: `EMAIL_PROVIDER=development|resend`, `EMAIL_FROM`, local `EMAIL_DEVELOPMENT_DIR`, `RESEND_API_KEY` (legacy `EMAIL_API_KEY` also works), optional `EMAIL_API_URL`
- domain/privacy: `STORE_REVIEW_RADIUS_METERS`, `SEARCH_LOCATION_DECIMALS`
- media: `OBJECT_STORAGE_PROVIDER=development|s3|r2`; local directory/public URL or endpoint/region/credentials/bucket, upload TTL and `MEDIA_MAX_BYTES`
- reporting: `REPORTING_TIMEZONE`, `SEARCH_ATTRIBUTION_WINDOW_HOURS`
- operations: `METRICS_TOKEN`, `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`

The API never needs OpenAI or Places credentials to boot. Search falls back to deterministic parsing and internal PostgreSQL results when either provider is unavailable.

## Operational and privacy notes

- Terminate TLS at the deployment edge. BFF and bearer credentials must never travel over plaintext networks.
- A static credential in a React Native binary can be extracted. `X-BFF-Secret` is a client/gateway gate, not user identity. Replace the client verifier with App Attest, Play Integrity or short-lived gateway credentials as mobile hardening matures.
- GPS proximity is not cryptographic proof of presence and can be spoofed. The backend calculates distance and stores only derived distance/time—not the submitted coordinates—so stronger verification can later supplement it.
- Search coordinates are deliberately rounded before persistence. Add a scheduled retention/anonymization job according to the product's final privacy policy.
- Google provider payloads are not stored wholesale. Search snapshots retain provider reference/rank and platform-owned metrics; attribution is returned from live provider data.
- The current limiter is process-local. Replace its storage with Redis only when horizontally scaled instances require coordinated quotas.

## Migrations

`make migrate` records each applied version in `schema_migrations` and applies each file atomically. `make migrate-down` reverts only the latest applied migration. Production deploys should back up first and run migrations as a dedicated release step.

## Email behavior

The development sender never prints OTP content. The worker renders queued mail
and writes it to the git-ignored `.data/mailbox` directory. Production uses
Resend's HTTPS API, stores provider message IDs, records delivery attempts, and
retries transient failures through the PostgreSQL outbox. Queue claiming uses
`FOR UPDATE SKIP LOCKED`; stale `processing` jobs are recovered after five
minutes. Permanent failures are not retried and transient failures use bounded
exponential backoff.

## Local media flow

With `OBJECT_STORAGE_PROVIDER=development`, `POST /v1/media/uploads` returns a
short-lived HMAC-authorized local PUT URL. PUT the exact declared number of bytes
with the declared content type, then call `POST /v1/media/{id}/complete`. The
local provider sniffs file bytes, rejects MIME spoofing, stores files under
`.data/uploads`, and serves them from `/uploads/*`. Finalization also verifies
database ownership, size and MIME. S3 and R2 use the same service contract and
presigned PUT semantics.

## Metrics, tracing and logs

`GET /metrics` exposes Prometheus counters/histograms for stable HTTP route
patterns, auth, search, providers and workers. Set `METRICS_TOKEN` to require an
`X-Metrics-Token` header; production infrastructure should set it or restrict
the route at the network edge. Labels never contain email, user IDs, raw search
queries or URLs.

Set `OTEL_ENABLED=true` and `OTEL_EXPORTER_OTLP_ENDPOINT` to enable OTLP/HTTP
tracing. When disabled, no collector is required. Incoming HTTP and OpenAI,
Google Places, Resend and object-storage calls propagate request context. JSON
request logs include request ID, method, matched route pattern, status, duration,
client type and authenticated/visitor identifiers when available. Headers,
tokens, OTPs and provider keys are not logged.

## Database integration and provider tests

The integration suite never substitutes SQLite for PostgreSQL behavior. Prepare
a migrated PostgreSQL/PostGIS database, then run:

```bash
export TEST_DATABASE_URL="$DATABASE_URL"
make integration-test
```

It covers OTP identity/session lifecycle, cross-provider identity merge,
refresh rotation/reuse/logout, PostGIS proximity rejection/acceptance, social
uniqueness, viewer-specific feed state, search interaction/attribution,
reporting rebuild idempotency, account deletion, local media, and 20 concurrent
Google Place materializations.

Live providers are deliberately opt-in and skipped without credentials:

```bash
make provider-smoke # OPENAI_API_KEY + optional OPENAI_MODEL
make provider-smoke # GOOGLE_PLACES_API_KEY when present
make provider-smoke # RESEND_API_KEY, EMAIL_FROM and explicit RESEND_TEST_RECIPIENT
```

The suite itself checks the required variables, so `make provider-smoke` is safe
without them and reports skips. Normal `go test` never uses the internet or
incurs provider cost. Resend and Google Places also have local fake-transport
contract tests.

## Reproducible smoke journey

Start API and worker after migration/seed, then:

```bash
export BFF_SECRET="${BFF_SECRETS%%,*}"
SMOKE_EMAIL=smoke@example.test make smoke-test
```

The script checks liveness/readiness, BFF rejection, anonymous feed/search/store
detail, local mailbox OTP login, private profile, favorite, geographically valid
review, feed, like, comment, follow where another author exists, and a second
authenticated search. Omit `SMOKE_EMAIL` for the anonymous subset. It requires
`curl` and `jq`. Refresh aggregates afterwards with
`make rebuild-admin-metrics`.

The complete client contract, request schemas, security requirements,
pagination and domain error shape are in [docs/openapi.yaml](docs/openapi.yaml).

## Reporting and privacy operations

`make rebuild-admin-metrics` rebuilds the canonical snapshot and every
Europe/Istanbul reporting day from source-of-truth tables. Repeated rebuilds are
idempotent. Database timestamps remain UTC; only day boundaries use the
configured timezone. `make privacy-maintenance` removes expired anonymous/search
data and old OTP/outbox rows, clears aged rounded search coordinates, then
refreshes the snapshot. Run both as separately scheduled operational jobs.

## Reverse proxy and scaling expectations

TLS terminates at the trusted deployment edge. The application currently uses
the TCP peer address for IP rate limiting and deliberately ignores arbitrary
`X-Forwarded-For`; configure the edge to preserve the real socket peer or add a
deployment-specific trusted-proxy allowlist before consuming forwarded headers.
The limiter is process-local, so quotas are per instance. Its boundary can later
move to Redis when horizontal coordination is actually required.

## Current extension points

- `search.IntentParser`: OpenAI Responses API or another validated parser
- `search.PlacesProvider`: Google Places or future physical-store sources
- `email.Sender`: development and Resend implementations
- `media.ObjectStorage`: signed-upload implementation for S3/GCS/R2
- `notification.PushProvider`: future APNs/FCM/web push provider

These boundaries are intentionally narrow; repositories remain concrete and SQL remains visible.
