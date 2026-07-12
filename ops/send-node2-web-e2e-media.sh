#!/usr/bin/env bash
set -euo pipefail

target_user_id="${1:-}"
media_type="${2:-}"
url_base64="${3:-}"
name_base64="${4:-}"
file_size="${5:-}"
width="${6:-1}"
height="${7:-1}"
deploy_root="${8:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"

[[ "$target_user_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || { echo "target OpenIM user ID is invalid" >&2; exit 1; }
[[ "$media_type" == "image" || "$media_type" == "file" ]] || { echo "media type is invalid" >&2; exit 1; }
[[ "$file_size" =~ ^[1-9][0-9]*$ && "$width" =~ ^[1-9][0-9]*$ && "$height" =~ ^[1-9][0-9]*$ ]] || {
  echo "media dimensions or size are invalid" >&2
  exit 1
}
[[ -r "$openim_env" && -n "$url_base64" && -n "$name_base64" ]] || {
  echo "node2 OpenIM environment or media metadata is missing" >&2
  exit 1
}

readarray -t decoded < <(URL_BASE64="$url_base64" NAME_BASE64="$name_base64" python3 - <<'PY'
import base64
import os
import urllib.parse

url = base64.b64decode(os.environ["URL_BASE64"], validate=True).decode("utf-8")
name = base64.b64decode(os.environ["NAME_BASE64"], validate=True).decode("utf-8")
parsed = urllib.parse.urlsplit(url)
if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.username or parsed.password:
    raise SystemExit("media URL is invalid")
if not 1 <= len(name) <= 255 or "\x00" in name:
    raise SystemExit("media filename is invalid")
print(url)
print(name)
PY
)
media_url="${decoded[0]}"
file_name="${decoded[1]}"

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r')"
admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-web-e2e-media-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"
unset admin_response secret

request_file="$(mktemp)"
trap 'rm -f "$request_file"' EXIT
TARGET_USER_ID="$target_user_id" MEDIA_TYPE="$media_type" MEDIA_URL="$media_url" FILE_NAME="$file_name" FILE_SIZE="$file_size" WIDTH="$width" HEIGHT="$height" python3 - <<'PY' >"$request_file"
import json
import os
import uuid

media_type = os.environ["MEDIA_TYPE"]
media_url = os.environ["MEDIA_URL"]
file_name = os.environ["FILE_NAME"]
file_size = int(os.environ["FILE_SIZE"])
object_id = str(uuid.uuid4())
if media_type == "image":
    picture = {
        "uuid": object_id,
        "type": "image/png",
        "size": file_size,
        "width": int(os.environ["WIDTH"]),
        "height": int(os.environ["HEIGHT"]),
        "url": media_url,
    }
    content = {
        "sourcePath": "",
        "sourcePicture": picture,
        "bigPicture": picture,
        "snapshotPicture": picture,
    }
    content_type = 102
else:
    content = {
        "filePath": "",
        "uuid": object_id,
        "sourceUrl": media_url,
        "fileName": file_name,
        "fileSize": file_size,
    }
    content_type = 105

print(json.dumps({
    "recvID": os.environ["TARGET_USER_ID"],
    "sendID": "imAdmin",
    "groupID": "",
    "senderNickname": "Node2 E2E Peer",
    "senderPlatformID": 5,
    "content": content,
    "contentType": content_type,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": "node2-web-media-e2e",
}, separators=(",", ":"), ensure_ascii=False))
PY

send_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/msg/send_msg \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-media-send-$(date +%s%N)" \
  --data-binary "@$request_file")"
unset admin_token media_url file_name
server_msg_id="$(RESPONSE="$send_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["serverMsgID"])')"
echo "node2_e2e_media=accepted"
echo "server_msg_id=$server_msg_id"
