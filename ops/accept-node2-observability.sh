#!/usr/bin/env bash
set -euo pipefail

env_file="${1:-/home/qsyy0921/MFL/deploy/node2-native/platform/.env}"
prometheus_url="${OPENIM_NODE2_PROMETHEUS_URL:-http://127.0.0.1:19091}"
grafana_url="${OPENIM_NODE2_GRAFANA_URL:-http://127.0.0.1:13001}"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

[[ -r "$env_file" ]] || {
  echo "platform environment is not readable: $env_file" >&2
  exit 1
}
grafana_password="$(sed -n 's/^PLATFORM_LOCAL_GRAFANA_ADMIN_PASSWORD=//p' "$env_file" | tail -1 | tr -d '\r')"
[[ "$grafana_password" =~ ^[0-9a-f]{48}$ ]] || {
  echo "Grafana administrator password is missing or malformed" >&2
  exit 1
}

curl --fail --silent --show-error "$prometheus_url/-/ready" >/dev/null
targets_ready=false
for _ in $(seq 1 30); do
  curl --fail --silent --show-error "$prometheus_url/api/v1/targets" >"$work_dir/targets.json"
  if python3 - "$work_dir/targets.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
health = {
    target.get("labels", {}).get("job"): target.get("health")
    for target in payload.get("data", {}).get("activeTargets", [])
}
expected = {"prometheus", "openim-platform-api", "openim-intelligence-worker"}
raise SystemExit(0 if set(health) == expected and all(health[job] == "up" for job in expected) else 1)
PY
  then
    targets_ready=true
    break
  fi
  sleep 2
done
[[ "$targets_ready" == "true" ]] || {
  echo "Prometheus targets did not become healthy" >&2
  exit 1
}
curl --fail --silent --show-error "$prometheus_url/api/v1/rules" >"$work_dir/rules.json"
curl --fail --silent --show-error "$grafana_url/api/health" >"$work_dir/grafana-health.json"
curl --fail --silent --show-error --user "local-admin:$grafana_password" \
  "$grafana_url/api/datasources/name/Prometheus" >"$work_dir/datasource.json"
curl --fail --silent --show-error --get --user "local-admin:$grafana_password" \
  --data-urlencode 'query=OpenIM Agent Platform' \
  "$grafana_url/api/search" >"$work_dir/dashboards.json"
unset grafana_password

python3 - "$work_dir" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
targets = json.loads((root / "targets.json").read_text(encoding="utf-8"))
rules = json.loads((root / "rules.json").read_text(encoding="utf-8"))
grafana = json.loads((root / "grafana-health.json").read_text(encoding="utf-8"))
datasource = json.loads((root / "datasource.json").read_text(encoding="utf-8"))
dashboards = json.loads((root / "dashboards.json").read_text(encoding="utf-8"))

active_targets = targets.get("data", {}).get("activeTargets", [])
health_by_job = {
    target.get("labels", {}).get("job"): target.get("health")
    for target in active_targets
}
expected_jobs = {
    "prometheus",
    "openim-platform-api",
    "openim-intelligence-worker",
}
if set(health_by_job) != expected_jobs:
    raise SystemExit(f"unexpected Prometheus jobs: {sorted(health_by_job)}")
if any(health_by_job[job] != "up" for job in expected_jobs):
    raise SystemExit(f"unhealthy Prometheus targets: {health_by_job}")

rule_count = sum(
    len(group.get("rules", []))
    for group in rules.get("data", {}).get("groups", [])
)
if rule_count != 6:
    raise SystemExit(f"unexpected Prometheus rule count: {rule_count}")
if grafana.get("database") != "ok":
    raise SystemExit("Grafana database health is not ok")
if datasource.get("name") != "Prometheus":
    raise SystemExit("Grafana Prometheus datasource is not provisioned")
if not any(item.get("uid") == "openim-agent-platform" for item in dashboards):
    raise SystemExit("Grafana OpenIM Agent Platform dashboard is not provisioned")

print("prometheus_targets=3_up")
print("prometheus_rules=6")
print("grafana_datasource=Prometheus")
print("grafana_dashboard=openim-agent-platform")
print("node2_observability=accepted")
PY
