# home-app API

Production-oriented Go backend foundation for a Turkey-first social discovery product about real, physical home/living stores.

The product loop is deliberately narrow: **discover → visit → review → help the next person discover**. Stores do not need an owner or platform membership.

## What is implemented

- PostgreSQL/PostGIS schema with explicit reversible migrations and migration tracking
- mandatory, rotation-ready constant-time `X-BFF-Secret` validation for every `/v1/*` request
- anonymous browsing with strict optional-auth semantics (an invalid supplied bearer token is rejected)
- email OTP outbox with hashed codes, encrypted delivery payloads, attempt/expiry controls and a worker
- production Resend email adapter plus a non-disclosing development adapter
- Google ID-token verification and transactionally safe verified-email identity linking
- short access JWTs and hashed, rotating, family-revocable refresh tokens with reuse detection
- public/private profile separation
- PostGIS physical stores, categories, external IDs, aggregate statistics and favorite uniqueness
- proximity-verified 1–5 star reviews, media metadata links, feed cursor pagination, likes, comments and follows
- deterministic Turkish search, optional official OpenAI Go SDK/Responses API structured enrichment, Google Places (New), explicit source-separated ratings, deduplication, ranking and fallback
- user/anonymous search history, impression snapshots and ownership-bound interaction events
- email and notification outboxes, push and object-storage boundaries
- request IDs, JSON logs, recovery, size/time limits, security headers and bounded in-process rate limiting
- realistic Turkish seed data and security/search unit tests

The detailed schema, route matrix, identity concurrency strategy, threat model and search flow are in [docs/architecture.md](docs/architecture.md).

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

Useful checks:

```bash
make test
make lint
make build
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
curl -H "X-BFF-Secret: $BFF_SECRET" http://localhost:8080/v1/feed
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
- auth: `ACCESS_TOKEN_SECRET`, `OTP_HASH_SECRET`, access/refresh/OTP TTLs and attempt limit
- Google: `GOOGLE_CLIENT_ID`, `GOOGLE_PLACES_API_KEY`
- AI: `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_TIMEOUT`; an empty key cleanly disables AI
- email: `EMAIL_PROVIDER=development|resend`, `EMAIL_FROM`, `EMAIL_API_KEY`, optional `EMAIL_API_URL`
- domain/privacy: `STORE_REVIEW_RADIUS_METERS`, `SEARCH_LOCATION_DECIMALS`

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

The development sender deliberately does not print OTP content. It marks queued mail delivered with a development message ID, which protects logs but means local end-to-end OTP login should use a test-only sender or inspect a code before hashing in a debugger. Production uses Resend's HTTPS API, stores provider message IDs, records delivery attempts, and retries transient failures through the PostgreSQL outbox.

## Current extension points

- `search.IntentParser`: OpenAI Responses API or another validated parser
- `search.PlacesProvider`: Google Places or future physical-store sources
- `email.Sender`: development and Resend implementations
- `media.ObjectStorage`: signed-upload implementation for S3/GCS/R2
- `notification.PushProvider`: future APNs/FCM/web push provider

These boundaries are intentionally narrow; repositories remain concrete and SQL remains visible.
