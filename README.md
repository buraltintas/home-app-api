# Boşa Gezme! API

Production-oriented Go backend for [Boşa Gezme!](https://bosagezme.com), a multilingual social discovery product about real, physical home/living stores.

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
- first-class Turkish, English, German and Russian locale resolution, localized API copy/email/notification foundations and canonical localized taxonomy
- Unicode-aware multilingual search, optional official OpenAI Go SDK/Responses API structured enrichment, locale-aware Google Places, explicit source-separated ratings, deduplication, ranking and fallback
- user/anonymous search history, impression snapshots and ownership-bound interaction events
- rebuildable platform snapshots, Istanbul-day metrics, query/intent/store search aggregates and bounded conversion attribution
- email and notification outboxes, push and object-storage boundaries
- native GCS uploads through ADC/IAM, S3/R2-compatible signed uploads, and a real local filesystem upload provider with content sniffing
- request IDs, route-pattern JSON logs, recovery, size/time limits, security headers and bounded in-process rate limiting
- protected Prometheus metrics and optional OTLP/OpenTelemetry tracing
- deterministic four-locale seed data, unit/contract tests and opt-in real PostgreSQL/PostGIS integration tests

The detailed schema, route matrix, identity concurrency strategy, threat model and search flow are in [docs/architecture.md](docs/architecture.md).
Metric semantics and reporting operations are in [docs/reporting.md](docs/reporting.md).
Approved brand assets and usage notes are in [docs/brand/](docs/brand/).

## Brand identity

- Display name: **Boşa Gezme!**
- Canonical product domain: [bosagezme.com](https://bosagezme.com)
- Runtime identity constants: [`internal/brand`](internal/brand)

The Go module/repository path, local PostgreSQL identifiers, migration history,
and `$home-app-design` command remain stable technical identifiers. Renaming
them without moving the repository and infrastructure would break imports,
existing local volumes, or deterministic seed identity.

## Requirements

- Go 1.25+
- Docker with Compose (recommended)
- PostgreSQL client tools are optional; migrations run through Go

## Local setup

The checked-in `.env.example` is the minimal production template. For local
development, copy it and change/add the following local-only values in `.env`:

```dotenv
APP_ENV=development
DATABASE_URL=postgres://home_app:home_app@localhost:5432/home_app?sslmode=disable
BFF_SECRETS=development-only-change-me
ACCESS_TOKEN_SECRET=development-access-secret-at-least-32-bytes
OTP_HASH_SECRET=development-otp-secret-at-least-32-bytes
EMAIL_PROVIDER=development
EMAIL_DEVELOPMENT_DIR=.data/mailbox
OBJECT_STORAGE_PROVIDER=development
OBJECT_STORAGE_LOCAL_DIR=.data/uploads
OBJECT_STORAGE_PUBLIC_URL=http://localhost:8080/uploads
```

```bash
cp .env.example .env
set -a; source .env; set +a
docker compose up -d
make migrate
make seed
make run
```

When using `source`, quote values containing shell metacharacters such as `&`.
Managed production platforms should inject environment variables natively
instead of shell-sourcing a file.

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

Blank production placeholders must be supplied through the deployment secret
manager. Never commit populated `.env` files or Google service-account JSON.

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

Clients may explicitly select `tr`, `en`, `de`, or `ru` using `X-Locale`.
Regional variants such as `de-DE` are normalized. Resolution order is explicit
`X-Locale`, authenticated private `preferred_locale`, `Accept-Language`, then
`DEFAULT_LOCALE` (`tr` by default). Invalid/unsupported locale hints safely fall
back. API error codes remain stable while their messages use the resolved locale.

An anonymous search creates a visitor session when `X-Visitor-Session-ID` is absent. Persist the returned `visitor_session_id` client-side and send it on later analytics/search calls:

```bash
curl -X POST http://localhost:8080/v1/search \
  -H 'Content-Type: application/json' \
  -H "X-BFF-Secret: $BFF_SECRET" \
  -d '{"query":"Kadıköy’de güzel aydınlatma mağazaları","latitude":40.9908,"longitude":29.0277}'
```

Authenticated actions additionally send `Authorization: Bearer <access_token>`.

## Configuration

See the minimal production template in [.env.example](.env.example). Parameters
with safe code defaults and variables for unused alternative providers are
intentionally omitted. Important supported groups are:

- core: `DATABASE_URL`, `HTTP_ADDR`, `APP_ENV`, `DEFAULT_LOCALE`
- client security: `BFF_SECRETS` (comma-separated; `BFF_SECRET` is a legacy single-secret fallback)
- auth: `ACCESS_TOKEN_SECRET`, `OTP_HASH_SECRET`, access/refresh/OTP TTLs, verification attempts, per-email/IP/visitor request limits, and the optional paired `APP_REVIEW_EMAIL`/`APP_REVIEW_CODE`
- Google: `GOOGLE_CLIENT_ID`, `GOOGLE_PLACES_API_KEY`
- AI: `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_TIMEOUT`; an empty key cleanly disables AI
- email: `EMAIL_PROVIDER=development|gmail|resend`, `EMAIL_FROM`; Gmail Workspace uses `GMAIL_IMPERSONATED_USER` plus exactly one secret source (`GMAIL_SERVICE_ACCOUNT_FILE` recommended, or `GMAIL_SERVICE_ACCOUNT_JSON`) and optional `GMAIL_API_URL`; local development uses `EMAIL_DEVELOPMENT_DIR`; Resend remains available through `RESEND_API_KEY`
- domain/privacy: `STORE_REVIEW_RADIUS_METERS`, `STORE_LOCATION_MAX_ACCURACY_METERS`, `STORE_VISIT_PROOF_TTL`, `SEARCH_LOCATION_DECIMALS`
- media: `OBJECT_STORAGE_PROVIDER=development|gcs|s3|r2`; GCS uses Application Default Credentials plus `OBJECT_STORAGE_BUCKET` and optional `GCS_SIGNING_SERVICE_ACCOUNT`; S3/R2 use endpoint/region/static credentials; all providers use upload TTL and `MEDIA_MAX_BYTES`
- reporting: `REPORTING_TIMEZONE`, `SEARCH_ATTRIBUTION_WINDOW_HOURS`
- operations: `METRICS_TOKEN`, `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`

The API never needs OpenAI or Places credentials to boot. Search falls back to deterministic parsing and internal PostgreSQL results when either provider is unavailable.

### Google Workspace Gmail delivery

Production OTP email can be sent from the Workspace mailbox `no-reply@bosagezme.com` through the Gmail API:

1. Create the Workspace user/mailbox and enable the Gmail API in the Google Cloud project.
2. Create a dedicated service account, enable domain-wide delegation, and note its numeric OAuth client ID.
3. As a Workspace super administrator, open **Security → Access and data control → API controls → Manage Domain Wide Delegation**. Add that numeric client ID with only `https://www.googleapis.com/auth/gmail.send`.
4. Store the service-account JSON in a secret manager available only to the email worker and mount it read-only; set `GMAIL_SERVICE_ACCOUNT_FILE` to that path. `GMAIL_SERVICE_ACCOUNT_JSON` is supported for platforms that inject secrets directly, but never set both and never commit either value. The API process does not require this credential.
5. Set `EMAIL_PROVIDER=gmail`, `EMAIL_FROM="Boşa Gezme! <no-reply@bosagezme.com>"`, and `GMAIL_IMPERSONATED_USER=no-reply@bosagezme.com`. The two addresses must match.
6. Before production, set an explicit safe `GMAIL_TEST_RECIPIENT` and run `make provider-smoke`; this sends one clearly labelled test email. Remove the test-recipient variable afterward.
7. Start/redeploy the worker. Domain-wide delegation can take time to propagate; a configuration/permission error is treated as permanent, while Gmail quota and server failures use the existing bounded outbox retry policy.

The worker sends an RFC-compliant multipart text/HTML message through `users.messages.send`. It requests no Gmail read, modify, compose, or full-mailbox scope.
The OTP template follows the Boşa Gezme! warm editorial palette and includes explicit light/dark mailbox styles, responsive email-safe table layout, high-contrast code presentation, localized `tr`/`en`/`de`/`ru` content, hidden preview text, and a complete plain-text alternative.

### App Store review login

Set `APP_REVIEW_EMAIL=app-review@bosagezme.com` and `APP_REVIEW_CODE=123456` together. The reviewer follows the ordinary email login flow: request a code, then enter `123456`. The request still creates a short-lived, single-use, attempt-limited verification record and uses the normal rate limits, but it does not queue or send an email for that exact address. All other addresses keep the ordinary random-code delivery flow.

Keep these credentials out of the application UI and provide them only in App Store Connect review notes. The account should contain synthetic/minimal review data. Remove both environment variables to disable the exception, or rotate the code between review windows.

## Operational and privacy notes

- Terminate TLS at the deployment edge. BFF and bearer credentials must never travel over plaintext networks.
- A static credential in a React Native binary can be extracted. `X-BFF-Secret` is a client/gateway gate, not user identity. Replace the client verifier with App Attest, Play Integrity or short-lived gateway credentials as mobile hardening matures.
- GPS proximity is not cryptographic proof of presence and can be spoofed. The backend calculates distance and stores only derived distance/time—not the submitted coordinates—so stronger verification can later supplement it.
- Search coordinates are deliberately rounded before persistence. Add a scheduled retention/anonymization job according to the product's final privacy policy.
- Google provider payloads are not stored wholesale. Search snapshots retain provider reference/rank and platform-owned metrics; attribution is returned from live provider data.
- The current limiter is process-local. Replace its storage with Redis only when horizontally scaled instances require coordinated quotas.
- `DELETE /v1/me` is the user-facing account deletion action. It permanently purges profile/private fields, search history, social relationships and user content, revokes every session, and deactivates the row. The verified login identity and original email are retained solely so a later verified login reactivates the same UUID with a blank profile; deleted data is never restored.

## Migrations

`make migrate` records each applied version in `schema_migrations` and applies each file atomically. `make migrate-down` reverts only the latest applied migration. Production deploys should back up first and run migrations as a dedicated release step. The current client contract assumes migrations through `000008_reactivatable_accounts` are applied; an unseeded database is valid and returns stable empty collection arrays.

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
database ownership, size and MIME. GCS, S3 and R2 use the same service contract
and V4/presigned PUT semantics. Published post responses expose media objects
with stable API-relative `/media/{id}` URLs; that public route only resolves
ready media attached to visible posts and redirects to a local or short-lived
signed private-object URL.

For production GCS, set `OBJECT_STORAGE_PROVIDER=gcs` and
`OBJECT_STORAGE_BUCKET`; do not set an access key or secret key. Attach a
service account to the runtime so Application Default Credentials can resolve
it. The service account needs object create/read/delete permissions on the
bucket. V4 upload URL signing additionally requires
`iam.serviceAccounts.signBlob` (normally via
`roles/iam.serviceAccountTokenCreator`) and the IAM Service Account Credentials
API. Grant that role to the runtime principal on the signing service account
(which may be itself), rather than project-wide. `GCS_SIGNING_SERVICE_ACCOUNT`
is optional when the client library can detect the attached account and can be
set to the attached/impersonated service-account email otherwise. Browser-based
direct uploads also require an explicit bucket CORS policy for the deployed web
origin; native mobile uploads do not. Local development can use `gcloud auth
application-default login --impersonate-service-account=SERVICE_ACCOUNT_EMAIL`.

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
make provider-smoke # provider credentials plus an explicit RESEND_TEST_RECIPIENT or GMAIL_TEST_RECIPIENT
make provider-smoke # GCS_TEST_BUCKET + ADC/IAM, creates and removes one smoke object
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
