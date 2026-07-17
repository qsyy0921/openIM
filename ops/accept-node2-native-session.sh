#!/usr/bin/env bash
set -euo pipefail

public_host="${1:-192.168.0.38}"
device_id="${2:-ubuntu-web}"
public_origin="${3:-https://${public_host}:3443}"
platform_id=5
issuer="${public_origin}/auth/realms/platform"
platform_api="http://127.0.0.1:18080"

token_response="$(curl -fsS --max-time 15 -X POST \
  "${issuer}/protocol/openid-connect/token" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=password' \
  --data-urlencode 'client_id=platform-api' \
  --data-urlencode 'scope=openid' \
  --data-urlencode 'username=local-member' \
  --data-urlencode 'password=local-test-only')"
oidc_token="$(RESPONSE="$token_response" python3 -c \
  'import json,os; print(json.loads(os.environ["RESPONSE"])["id_token"])')"
unset token_response

session_response="$(curl -fsS --max-time 20 -X POST \
  "${platform_api}/v1/im/session" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${oidc_token}" \
  --data "{\"platform_id\":${platform_id},\"device_id\":\"${device_id}\"}")"
unset oidc_token

printf '%s' "$session_response" | python3 /dev/fd/3 "$platform_id" 3<<'PY'
import base64
import json
import os
import socket
import ssl
import sys
import urllib.parse

platform_id = int(sys.argv[1])
session = json.load(sys.stdin)
for field in ("user_id", "ws_url", "user_token", "expires_at"):
    assert isinstance(session.get(field), str) and session[field], field

parsed = urllib.parse.urlparse(session["ws_url"])
assert parsed.scheme in {"ws", "wss"}, parsed.scheme
assert parsed.hostname and parsed.port, session["ws_url"]
query = urllib.parse.urlencode(
    {
        "sendID": session["user_id"],
        "token": session["user_token"],
        "platformID": platform_id,
        "operationID": "node2-native-session-acceptance",
    }
)
path = parsed.path or "/"
path = f"{path}?{query}"
key = base64.b64encode(os.urandom(16)).decode("ascii")
request = (
    f"GET {path} HTTP/1.1\r\n"
    f"Host: {parsed.hostname}:{parsed.port}\r\n"
    "Upgrade: websocket\r\n"
    "Connection: Upgrade\r\n"
    f"Sec-WebSocket-Key: {key}\r\n"
    "Sec-WebSocket-Version: 13\r\n\r\n"
).encode("ascii")

raw_stream = socket.create_connection((parsed.hostname, parsed.port), timeout=10)
if parsed.scheme == "wss":
    stream = ssl.create_default_context().wrap_socket(raw_stream, server_hostname=parsed.hostname)
else:
    stream = raw_stream
with stream:
    stream.sendall(request)
    response = stream.recv(4096).decode("latin-1", errors="replace")

status_line = response.split("\r\n", 1)[0]
assert status_line.startswith("HTTP/1.1 101 "), status_line
print("oidc_token_grant=accepted")
print("platform_im_session=accepted")
print("openim_websocket=accepted")
PY
unset session_response
