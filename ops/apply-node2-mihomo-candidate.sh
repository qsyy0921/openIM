#!/usr/bin/env bash
set -euo pipefail

candidate="${1:-}"
expected_sha256="${2:-}"
selections_file="${3:-}"
expected_selections_sha256="${4:-}"
profile_name="${5:-node6-working.yaml}"
mihomo_dir=/etc/mihomo
config_file="$mihomo_dir/config.yaml"
profile_dir="$mihomo_dir/profiles"
ui_module=/opt/nexusim-mihomo-ui/server.py

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$expected_sha256" =~ ^[a-f0-9]{64}$ ]] || {
  echo "expected SHA-256 is malformed" >&2
  exit 1
}
[[ "$expected_selections_sha256" =~ ^[a-f0-9]{64}$ ]] || {
  echo "expected selections SHA-256 is malformed" >&2
  exit 1
}
[[ "$profile_name" =~ ^[A-Za-z0-9._-]+\.ya?ml$ ]] || {
  echo "profile name is malformed" >&2
  exit 1
}
[[ -f "$candidate" && ! -L "$candidate" ]] || {
  echo "candidate must be a regular file" >&2
  exit 1
}
[[ -f "$selections_file" && ! -L "$selections_file" ]] || {
  echo "selections must be a regular file" >&2
  exit 1
}
candidate="$(readlink -f -- "$candidate")"
selections_file="$(readlink -f -- "$selections_file")"
case "$candidate" in
  /dev/shm/openim-mihomo.*/*.yaml) ;;
  *)
    echo "candidate must be staged below a dedicated /dev/shm directory" >&2
    exit 1
    ;;
esac
case "$selections_file" in
  /dev/shm/openim-mihomo.*/*.json) ;;
  *)
    echo "selections must be staged below a dedicated /dev/shm directory" >&2
    exit 1
    ;;
esac
[[ "$(dirname "$candidate")" == "$(dirname "$selections_file")" ]] || {
  echo "candidate and selections must share one staging directory" >&2
  exit 1
}
candidate_owner="$(stat -c '%U' "$candidate")"
selections_owner="$(stat -c '%U' "$selections_file")"
if [[ "$candidate_owner" != root && "$candidate_owner" != "${SUDO_USER:-}" ]]; then
  echo "candidate owner is not the invoking user" >&2
  exit 1
fi
if [[ "$selections_owner" != root && "$selections_owner" != "${SUDO_USER:-}" ]]; then
  echo "selections owner is not the invoking user" >&2
  exit 1
fi
actual_sha256="$(sha256sum "$candidate" | awk '{print $1}')"
[[ "$actual_sha256" == "$expected_sha256" ]] || {
  echo "candidate SHA-256 mismatch" >&2
  exit 1
}
actual_selections_sha256="$(sha256sum "$selections_file" | awk '{print $1}')"
[[ "$actual_selections_sha256" == "$expected_selections_sha256" ]] || {
  echo "selections SHA-256 mismatch" >&2
  exit 1
}
[[ -r "$ui_module" && -r "$config_file" ]] || {
  echo "Mihomo runtime or active configuration is missing" >&2
  exit 1
}

umask 077
install -d -m 0700 -o root -g root "$mihomo_dir" "$profile_dir"
chmod 0600 "$config_file"
sanitized="$(mktemp "$mihomo_dir/.config.candidate.XXXXXX")"
previous_selections="$(mktemp "$mihomo_dir/.selections.previous.XXXXXX")"
backup="$mihomo_dir/config.backup-$(date +%Y%m%d-%H%M%S).yaml"
candidate_dir="$(dirname "$candidate")"
cleanup() {
  rm -f -- "$sanitized" "$previous_selections" "$candidate" "$selections_file"
  rmdir -- "$candidate_dir" 2>/dev/null || true
}
trap cleanup EXIT

wait_for_mihomo() {
  local ready=false
  for _ in $(seq 1 50); do
    if ss -ltnH | grep -q '127.0.0.1:7893 ' && \
      curl --fail --silent --output /dev/null --max-time 1 \
        http://127.0.0.1:9090/version; then
      ready=true
      break
    fi
    sleep 0.2
  done
  [[ "$ready" == true ]]
}

capture_selections() {
  python3 - "$1" <<'PY'
import json
import pathlib
import urllib.request
import sys

with urllib.request.urlopen("http://127.0.0.1:9090/proxies", timeout=5) as response:
    proxies = json.load(response).get("proxies", {})
selections = {
    name: value["now"]
    for name, value in proxies.items()
    if isinstance(value, dict)
    and value.get("type") == "Selector"
    and isinstance(value.get("now"), str)
}
destination = pathlib.Path(sys.argv[1])
destination.write_text(
    json.dumps(selections, ensure_ascii=False, sort_keys=True), encoding="utf-8"
)
destination.chmod(0o600)
PY
}

apply_selections() {
  python3 - "$1" <<'PY'
import json
import pathlib
import sys
import urllib.parse
import urllib.request

controller = "http://127.0.0.1:9090"
desired = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if not isinstance(desired, dict) or not desired:
    raise SystemExit("selection document must be a non-empty JSON object")
if any(not isinstance(name, str) or not isinstance(value, str) or not value
       for name, value in desired.items()):
    raise SystemExit("selection names and values must be non-empty strings")

def fetch_proxies():
    with urllib.request.urlopen(f"{controller}/proxies", timeout=5) as response:
        return json.load(response).get("proxies", {})

proxies = fetch_proxies()
for group, selected in desired.items():
    value = proxies.get(group)
    if not isinstance(value, dict) or value.get("type") != "Selector":
        raise SystemExit(f"candidate selector is missing: {group}")
    if selected not in value.get("all", []):
        raise SystemExit(f"candidate selector target is unavailable: {group}")

ordered = []
visiting = set()
visited = set()
def visit(group):
    if group in visited:
        return
    if group in visiting:
        raise SystemExit("cyclic selector dependency")
    visiting.add(group)
    target = desired[group]
    if target in desired:
        visit(target)
    visiting.remove(group)
    visited.add(group)
    ordered.append(group)

for group in sorted(desired):
    visit(group)

for group in ordered:
    endpoint = f"{controller}/proxies/{urllib.parse.quote(group, safe='')}"
    request = urllib.request.Request(
        endpoint,
        data=json.dumps({"name": desired[group]}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="PUT",
    )
    with urllib.request.urlopen(request, timeout=5) as response:
        response.read()

actual = fetch_proxies()
for group, selected in desired.items():
    if actual.get(group, {}).get("now") != selected:
        raise SystemExit(f"candidate selector did not persist: {group}")
print("mihomo_selections=applied")
PY
}

select_healthy_leaf() {
  python3 - "$1" <<'PY'
import concurrent.futures
import json
import pathlib
import sys
import urllib.error
import urllib.parse
import urllib.request

controller = "http://127.0.0.1:9090"
proxy_url = "http://127.0.0.1:7893"
selection_path = pathlib.Path(sys.argv[1])
desired = json.loads(selection_path.read_text(encoding="utf-8"))

def fetch_proxies():
    with urllib.request.urlopen(f"{controller}/proxies", timeout=5) as response:
        return json.load(response).get("proxies", {})

def put_selection(group, target):
    endpoint = f"{controller}/proxies/{urllib.parse.quote(group, safe='')}"
    request = urllib.request.Request(
        endpoint,
        data=json.dumps({"name": target}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="PUT",
    )
    with urllib.request.urlopen(request, timeout=5) as response:
        response.read()

proxies = fetch_proxies()
leaf_selectors = [
    group
    for group, target in desired.items()
    if target not in desired
    and target not in {"DIRECT", "REJECT", "REJECT-DROP", "PASS"}
    and proxies.get(group, {}).get("type") == "Selector"
]
if len(leaf_selectors) != 1:
    raise SystemExit(
        f"expected exactly one leaf selector, found {len(leaf_selectors)}"
    )
selector = leaf_selectors[0]
group_members = proxies[selector].get("all", [])
group_types = {"Selector", "URLTest", "Fallback", "LoadBalance", "Relay"}
candidates = [
    name
    for name in group_members
    if isinstance(name, str)
    and proxies.get(name, {}).get("type") not in group_types
    and name not in {"DIRECT", "REJECT", "REJECT-DROP", "PASS"}
]
selected = desired[selector]
candidates = ([selected] if selected in candidates else []) + [
    name for name in candidates if name != selected
]
if not candidates:
    raise SystemExit("leaf selector has no directly testable proxy candidates")
if len(candidates) > 96:
    raise SystemExit("leaf selector exceeds the bounded 96-candidate probe limit")

def measure(name):
    encoded = urllib.parse.quote(name, safe="")
    target = urllib.parse.quote("https://www.gstatic.com/generate_204", safe="")
    endpoint = f"{controller}/proxies/{encoded}/delay?url={target}&timeout=7000"
    try:
        with urllib.request.urlopen(endpoint, timeout=9) as response:
            delay = json.load(response).get("delay")
        if isinstance(delay, int) and delay > 0:
            return delay, name
    except Exception:
        pass
    return None

with concurrent.futures.ThreadPoolExecutor(max_workers=min(12, len(candidates))) as pool:
    measured = [result for result in pool.map(measure, candidates) if result]
measured.sort(key=lambda item: item[0])
if not measured:
    raise SystemExit("no candidate proxy passed the controller delay probe")

opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({"http": proxy_url, "https": proxy_url})
)

def external_https_works(url):
    request = urllib.request.Request(url, headers={"User-Agent": "curl/8"})
    try:
        with opener.open(request, timeout=12) as response:
            response.read(1)
        return True
    except urllib.error.HTTPError as error:
        return error.code < 500
    except Exception:
        return False

for _, candidate in measured[:8]:
    put_selection(selector, candidate)
    if external_https_works("https://api.telegram.org/") and external_https_works(
        "https://www.google.com/generate_204"
    ):
        desired[selector] = candidate
        selection_path.write_text(
            json.dumps(desired, ensure_ascii=False, sort_keys=True), encoding="utf-8"
        )
        selection_path.chmod(0o600)
        print(f"mihomo_probe_candidates={len(candidates)}")
        print("mihomo_selector=healthy")
        raise SystemExit(0)
raise SystemExit("no low-latency candidate passed both external HTTPS probes")
PY
}

check_external_https() {
  local url
  for url in https://api.telegram.org/ https://www.google.com/generate_204; do
    curl --fail --silent --show-error --output /dev/null \
      --proxy http://127.0.0.1:7893 --connect-timeout 8 --max-time 20 "$url" || return 1
  done
}

python3 - "$candidate" "$sanitized" "$ui_module" <<'PY'
import importlib.util
import os
import pathlib
import sys

import yaml

source = pathlib.Path(sys.argv[1])
destination = pathlib.Path(sys.argv[2])
module_path = sys.argv[3]
raw = source.read_bytes()
if not raw or len(raw) > 2 * 1024 * 1024:
    raise SystemExit("candidate must be between 1 byte and 2 MiB")
spec = importlib.util.spec_from_file_location("node2_mihomo_ui", module_path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
sanitized = module.sanitize_config(raw)
config = yaml.safe_load(sanitized)
if not isinstance(config, dict):
    raise SystemExit("candidate must contain a YAML mapping")
dns = config.get("dns")
if not isinstance(dns, dict) or not dns.get("enable"):
    raise SystemExit("candidate must contain an enabled DNS configuration")
# Node2's Wi-Fi path drops UDP DNS intermittently. Proxy endpoint lookup must
# use the independently verified TCP resolvers or the proxy cannot bootstrap.
dns["proxy-server-nameserver"] = [
    "tcp://223.5.5.5",
    "tcp://223.6.6.6",
]
sanitized = yaml.safe_dump(
    config, allow_unicode=True, default_flow_style=False, sort_keys=False
).encode("utf-8")
code, stdout, stderr = module.test_config(sanitized)
if code != 0:
    raise SystemExit((stdout + "\n" + stderr)[-4000:])
destination.write_bytes(sanitized)
os.chmod(destination, 0o600)
print("mihomo_candidate=validated")
PY

capture_selections "$previous_selections"
cp -a -- "$config_file" "$backup"
rollback=true
restore_previous() {
  if [[ "$rollback" == true ]]; then
    set +e
    install -m 0600 -o root -g root "$backup" "$config_file"
    systemctl restart mihomo.service
    if wait_for_mihomo; then
      apply_selections "$previous_selections"
    fi
    set -e
  fi
}
trap 'restore_previous; cleanup' EXIT

install -m 0600 -o root -g root "$sanitized" "$config_file"
systemctl restart mihomo.service
systemctl is-active --quiet mihomo.service
wait_for_mihomo || {
  echo "Mihomo proxy did not become ready" >&2
  exit 1
}
apply_selections "$selections_file"
if ! check_external_https; then
  select_healthy_leaf "$selections_file"
  apply_selections "$selections_file"
  check_external_https
fi

systemctl restart mihomo.service
wait_for_mihomo
apply_selections "$selections_file"
check_external_https
install -m 0600 -o root -g root "$sanitized" "$profile_dir/$profile_name"
printf '%s\n' "$profile_name" >"$mihomo_dir/active-profile"
chmod 0600 "$mihomo_dir/active-profile"
install -m 0600 -o root -g root "$selections_file" "$mihomo_dir/active-selections.json"
rollback=false
echo "mihomo_profile=$profile_name"
echo "mihomo_config_sha256=$(sha256sum "$config_file" | awk '{print $1}')"
echo "telegram_proxy=accepted"
