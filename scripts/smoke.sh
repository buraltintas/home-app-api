#!/usr/bin/env bash
set -euo pipefail

: "${API_URL:=http://127.0.0.1:8080}"
: "${BFF_SECRET:?BFF_SECRET is required}"
command -v curl >/dev/null || { echo "curl is required"; exit 1; }
command -v jq >/dev/null || { echo "jq is required"; exit 1; }

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

expect_status() {
  local expected="$1" method="$2" path="$3" body="${4:-}" auth="${5:-}"
  local args=(-sS -o "$work_dir/body.json" -w '%{http_code}' -X "$method" "$API_URL$path")
  args+=(-H "X-BFF-Secret: $BFF_SECRET")
  if [[ -n "$body" ]]; then args+=(-H 'Content-Type: application/json' --data "$body"); fi
  if [[ -n "$auth" ]]; then args+=(-H "Authorization: Bearer $auth"); fi
  local status
  status="$(curl "${args[@]}")"
  if [[ "$status" != "$expected" ]]; then
    echo "$method $path: expected $expected, got $status"
    cat "$work_dir/body.json"
    exit 1
  fi
}

curl -fsS "$API_URL/health" | jq -e '.status == "ok"' >/dev/null
curl -fsS "$API_URL/ready" | jq -e '.status == "ready"' >/dev/null
missing_bff="$(curl -sS -o /dev/null -w '%{http_code}' "$API_URL/v1/feed")"
[[ "$missing_bff" == "401" ]] || { echo "missing BFF secret was not rejected"; exit 1; }

expect_status 200 GET /v1/feed
cp "$work_dir/body.json" "$work_dir/feed.json"
post_id="$(jq -er '.items[0].id' "$work_dir/feed.json")"
author_id="$(jq -er '.items[0].user_id' "$work_dir/feed.json")"
feed_store_id="$(jq -er '.items[0].store_id' "$work_dir/feed.json")"
expect_status 200 GET "/v1/posts/$post_id"
expect_status 200 GET "/v1/posts/$post_id/comments"
expect_status 200 GET "/v1/users/$author_id"
expect_status 200 GET "/v1/users/$author_id/posts"
expect_status 200 GET "/v1/stores/$feed_store_id/posts"
expect_status 200 POST /v1/search '{"query":"IKEA mobilya","latitude":41.0451,"longitude":28.8972,"radius_meters":10000}'
cp "$work_dir/body.json" "$work_dir/search.json"
store_id="$(jq -er '[.results[] | select(.id != null)][0].id' "$work_dir/search.json")"
expect_status 200 GET "/v1/stores/$store_id"
expect_status 401 POST "/v1/stores/$store_id/favorite"
expect_status 401 POST "/v1/posts/$post_id/like"
expect_status 401 POST "/v1/posts/$post_id/comments" '{"text":"anonymous mutation must fail"}'
expect_status 401 POST "/v1/users/$author_id/follow"
expect_status 401 POST /v1/posts '{}'
expect_status 401 PATCH /v1/me '{}'

if [[ -z "${SMOKE_EMAIL:-}" ]]; then
	  echo "Anonymous smoke passed. Set SMOKE_EMAIL to run the authenticated local-mailbox journey."
	  exit 0
fi

if [[ -z "${SMOKE_OTP_CODE:-}" ]]; then
  expect_status 202 POST /v1/auth/email/request-code "{\"email\":\"$SMOKE_EMAIL\"}"
  : "${EMAIL_DEVELOPMENT_DIR:=.data/mailbox}"
  for _ in {1..15}; do
    mail_file="$(ls -t "$EMAIL_DEVELOPMENT_DIR"/*.eml 2>/dev/null | while read -r file; do grep -q "^To: $SMOKE_EMAIL$" "$file" && { echo "$file"; break; }; done || true)"
    if [[ -n "$mail_file" ]]; then
      SMOKE_OTP_CODE="$(grep -E '^Giriş kodunuz: [0-9]{6}$' "$mail_file" | grep -Eo '[0-9]{6}' | head -1)"
      break
    fi
    sleep 1
  done
  [[ -n "${SMOKE_OTP_CODE:-}" ]] || { echo "OTP did not appear in $EMAIL_DEVELOPMENT_DIR; is make worker running?"; exit 1; }
fi

expect_status 200 POST /v1/auth/email/verify-code "{\"email\":\"$SMOKE_EMAIL\",\"code\":\"$SMOKE_OTP_CODE\"}"
access_token="$(jq -er '.access_token' "$work_dir/body.json")"
expect_status 200 GET /v1/me '' "$access_token"
user_id="$(jq -er '.id' "$work_dir/body.json")"
expect_status 200 GET "/v1/users/$user_id"
jq -e '[has("email"),has("preferred_locale"),has("relationship_status"),has("home_style_interests")] | any | not' "$work_dir/body.json" >/dev/null
expect_status 204 POST "/v1/stores/$store_id/favorite" '' "$access_token"

lat="$(jq -er '.store.latitude' "$work_dir/body.json" 2>/dev/null || true)"
expect_status 200 GET "/v1/stores/$store_id" '' "$access_token"
lat="$(jq -er '.store.latitude' "$work_dir/body.json")"
lon="$(jq -er '.store.longitude' "$work_dir/body.json")"
expect_status 201 POST /v1/posts "{\"store_id\":\"$store_id\",\"text\":\"Yerel smoke testi ile doğrulanan mağaza ziyareti\",\"rating\":5,\"latitude\":$lat,\"longitude\":$lon,\"media_ids\":[]}" "$access_token"
created_post_id="$(jq -er '.id' "$work_dir/body.json")"

expect_status 200 GET /v1/feed '' "$access_token"
expect_status 200 GET "/v1/posts/$created_post_id" '' "$access_token"
jq -e --arg post "$created_post_id" '.id == $post' "$work_dir/body.json" >/dev/null
expect_status 200 GET "/v1/users/$user_id/posts" '' "$access_token"
jq -e --arg post "$created_post_id" 'any(.items[]; .id == $post)' "$work_dir/body.json" >/dev/null
expect_status 200 GET /v1/feed '' "$access_token"
post_id="$(jq -er --arg me "$user_id" '[.items[] | select(.user_id != $me)][0].id' "$work_dir/body.json")"
target_user="$(jq -er --arg me "$user_id" '[.items[] | select(.user_id != $me)][0].user_id' "$work_dir/body.json" 2>/dev/null || true)"
expect_status 204 POST "/v1/posts/$post_id/like" '' "$access_token"
expect_status 201 POST "/v1/posts/$post_id/comments" '{"text":"Smoke test yorumu"}' "$access_token"
if [[ -n "$target_user" ]]; then expect_status 204 POST "/v1/users/$target_user/follow" '' "$access_token"; fi
expect_status 200 POST /v1/search '{"query":"IKEA mobilya","latitude":41.0451,"longitude":28.8972,"radius_meters":10000}' "$access_token"
search_id="$(jq -er '.search_id' "$work_dir/body.json")"
search_result_id="$(jq -er '.results[0].search_result_impression_id' "$work_dir/body.json")"
expect_status 204 POST "/v1/searches/$search_id/interactions" "{\"search_result_id\":\"$search_result_id\",\"event_type\":\"result_click\",\"idempotency_key\":\"smoke-result-click-$search_id\"}" "$access_token"

echo "Authenticated user journey smoke passed, including an ownership-bound search interaction. Run 'make rebuild-admin-metrics' to refresh reporting aggregates."
