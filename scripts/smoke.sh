#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8787}"
MODEL="${MODEL:-claude-opus-4-7}"
SKIP_MESSAGE="${SKIP_MESSAGE:-false}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local tmp
  tmp="$(mktemp)"
  local status
  if [[ -n "$body" ]]; then
    status="$(curl -sS -o "$tmp" -w "%{http_code}" \
      -X "$method" \
      -H "Content-Type: application/json" \
      "$BASE_URL$path" \
      --data "$body")"
  else
    status="$(curl -sS -o "$tmp" -w "%{http_code}" \
      -X "$method" \
      "$BASE_URL$path")"
  fi
  printf '%s' "$status"
  echo
  cat "$tmp"
  rm -f "$tmp"
}

assert_2xx() {
  local name="$1"
  local status="$2"
  if [[ ! "$status" =~ ^2 ]]; then
    echo "FAIL $name: expected 2xx, got $status" >&2
    exit 1
  fi
  echo "PASS $name"
}

need curl

echo "==> health"
health_output="$(request GET /health)"
health_status="$(printf '%s\n' "$health_output" | head -n1)"
health_body="$(printf '%s\n' "$health_output" | tail -n +2)"
assert_2xx "health" "$health_status"
echo "$health_body"

echo "==> ready"
ready_output="$(request GET /ready)"
ready_status="$(printf '%s\n' "$ready_output" | head -n1)"
ready_body="$(printf '%s\n' "$ready_output" | tail -n +2)"
assert_2xx "ready" "$ready_status"
echo "$ready_body"

echo "==> models"
models_output="$(request GET /v1/models)"
models_status="$(printf '%s\n' "$models_output" | head -n1)"
models_body="$(printf '%s\n' "$models_output" | tail -n +2)"
assert_2xx "models" "$models_status"
echo "$models_body"

if [[ "$SKIP_MESSAGE" == "true" ]]; then
  echo "SKIP message smoke test"
  exit 0
fi

echo "==> messages"
message_body="$(cat <<JSON
{
  "model": "$MODEL",
  "max_tokens": 128,
  "stream": false,
  "messages": [
    {
      "role": "user",
      "content": "Reply with exactly: cc-vision-gateway-ok"
    }
  ]
}
JSON
)"
message_output="$(request POST /v1/messages "$message_body")"
message_status="$(printf '%s\n' "$message_output" | head -n1)"
message_response="$(printf '%s\n' "$message_output" | tail -n +2)"
assert_2xx "messages" "$message_status"
echo "$message_response"
