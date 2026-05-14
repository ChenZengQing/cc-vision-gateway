#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8787}"
MODEL="${MODEL:-claude-opus-4-7}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need curl
need python3

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

png="$tmpdir/sample.png"
payload="$tmpdir/payload.json"
response="$tmpdir/response.json"

python3 - "$png" <<'PY'
import struct
import zlib
import sys

def chunk(kind, data):
    return (
        struct.pack(">I", len(data))
        + kind
        + data
        + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    )

width = height = 32
raw = b"".join(b"\x00" + b"\xff\xff\xff" * width for _ in range(height))
data = (
    b"\x89PNG\r\n\x1a\n"
    + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    + chunk(b"IDAT", zlib.compress(raw))
    + chunk(b"IEND", b"")
)
with open(sys.argv[1], "wb") as f:
    f.write(data)
PY

image_b64="$(base64 < "$png" | tr -d '\n')"

python3 - "$payload" "$MODEL" "$image_b64" <<'PY'
import json
import sys

payload_path, model, image_b64 = sys.argv[1:]
payload = {
    "model": model,
    "max_tokens": 512,
    "stream": False,
    "messages": [
        {
            "role": "user",
            "content": [
                {
                    "type": "text",
                    "text": "This is a plumbing smoke test. Describe what the image diagnostic context says in one short sentence."
                },
                {
                    "type": "image",
                    "source": {
                        "type": "base64",
                        "media_type": "image/png",
                        "data": image_b64
                    }
                }
            ]
        }
    ]
}
with open(payload_path, "w") as f:
    json.dump(payload, f)
PY

status="$(curl -sS -o "$response" -w "%{http_code}" \
  -X POST \
  -H "Content-Type: application/json" \
  "$BASE_URL/v1/messages" \
  --data @"$payload")"

if [[ ! "$status" =~ ^2 ]]; then
  echo "FAIL image message: expected 2xx, got $status" >&2
  cat "$response" >&2
  exit 1
fi

echo "PASS image message"
cat "$response"
echo
