#!/usr/bin/env bash
set -euo pipefail

BASE_DIR=${BASE_DIR:-"$HOME/openim-lab/msgtest"}
BIN=${BIN:-"$BASE_DIR/msgtest-darwin-arm64"}
RUN_DIR=${RUN_DIR:-"$BASE_DIR/runs"}

RUN_ID=${RUN_ID:-"$(hostname)-online-hold-$(date +%Y%m%d-%H%M%S)"}
API_ADDR=${API_ADDR:-"http://172.31.50.2:10002"}
WS_ADDR=${WS_ADDR:-"ws://172.31.50.2:10001"}
TEST_IP=${TEST_IP:-"172.31.50.2"}
NETSTAT_REMOTE=${NETSTAT_REMOTE:-"172.31.50.2.10001"}

ONLINE=${ONLINE:-50000}
START_USER=${START_USER:-9000000}
END_USER=${END_USER:-$((START_USER + ONLINE))}
SAMPLE_RATE=${SAMPLE_RATE:-0.01}
HOLD_SECONDS=${HOLD_SECONDS:-300}
CONNECT_TIMEOUT_SECONDS=${CONNECT_TIMEOUT_SECONDS:-1800}
START_RETRIES=${START_RETRIES:-5}
RETRY_DELAY_SECONDS=${RETRY_DELAY_SECONDS:-5}

mkdir -p "$RUN_DIR"

STATUS_FILE="$RUN_DIR/${RUN_ID}.status"
LOG_FILE="$RUN_DIR/${RUN_ID}.msgtest.log"
PID_FILE="$RUN_DIR/${RUN_ID}.pid"

count_connections() {
  netstat -an -p tcp 2>/dev/null | awk -v remote="$NETSTAT_REMOTE" '$6 == "ESTABLISHED" && $5 == remote {c++} END {print c+0}'
}

write_status() {
  printf '%s\n' "$*" | tee -a "$STATUS_FILE"
}

stop_pid() {
  local pid="$1"
  if kill -0 "$pid" 2>/dev/null; then
    kill -INT "$pid" 2>/dev/null || true
    sleep 5
  fi
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
  fi
  if kill -0 "$pid" 2>/dev/null; then
    kill -KILL "$pid" 2>/dev/null || true
  fi
}

if [ ! -x "$BIN" ]; then
  echo "msgtest binary not executable: $BIN" >&2
  exit 2
fi

ulimit -n "${ULIMIT_NOFILE:-60000}" 2>/dev/null || true
export OPENIM_API_ADDR="$API_ADDR"
export OPENIM_WS_ADDR="$WS_ADDR"
export OPENIM_TEST_IP="$TEST_IP"

write_status "run_id=$RUN_ID"
write_status "mode=online_hold target=$NETSTAT_REMOTE online=$ONLINE hold_seconds=$HOLD_SECONDS"

preflight_network() {
  ping -c 1 -W 1000 "$TEST_IP" >/dev/null 2>&1 || true
  nc -vz -G 3 "$TEST_IP" 10002 >/dev/null 2>&1 || true
  nc -vz -G 3 "$TEST_IP" 10001 >/dev/null 2>&1 || true
}

launch_msgtest() {
  "$BIN" \
    -s "$START_USER" \
    -e "$END_USER" \
    -o "$ONLINE" \
    -sr "$SAMPLE_RATE" \
    -r \
    -gsr 0.1 \
    -gor 0 \
    -htg 0 \
    -ttg 0 \
    -otg 0 \
    -hog 0 \
    -fog 0 \
    -teg 0 \
    -u >"$LOG_FILE" 2>&1 &
  pid=$!
}

pid=0
for attempt in $(seq 1 "$START_RETRIES"); do
  preflight_network
  launch_msgtest
  sleep 5
  if kill -0 "$pid" 2>/dev/null; then
    break
  fi
  if grep -qi "no route to host" "$LOG_FILE" 2>/dev/null; then
    write_status "phase=start_retry ts=$(date +%F_%T) attempt=$attempt reason=no_route_to_host"
    sleep "$RETRY_DELAY_SECONDS"
    continue
  fi
  break
done

echo "$pid" >"$PID_FILE"
write_status "phase=launched ts=$(date +%F_%T) pid=$pid"

ready=0
reached=0
hold_start=0
deadline=$(( $(date +%s) + CONNECT_TIMEOUT_SECONDS ))
while true; do
  now=$(date +%s)
  ts=$(date +%F_%T)
  alive=0
  if kill -0 "$pid" 2>/dev/null; then
    alive=1
  fi
  conn=$(count_connections)
  if grep -q "all user init connect to server success" "$LOG_FILE" 2>/dev/null; then
    ready=1
  fi
  if [ "$reached" = "0" ] && { [ "$ready" = "1" ] || [ "$conn" -ge "$ONLINE" ]; }; then
    reached=1
    hold_start=$now
    write_status "phase=hold_start ts=$ts conn=$conn ready=$ready pid=$pid"
  fi
  held=0
  if [ "$reached" = "1" ]; then
    held=$((now - hold_start))
  fi
  write_status "phase=poll ts=$ts alive=$alive ready=$ready conn=$conn held_sec=$held"
  if [ "$reached" = "1" ] && [ "$held" -ge "$HOLD_SECONDS" ]; then
    write_status "phase=hold_complete ts=$ts conn=$conn held_sec=$held"
    break
  fi
  if [ "$alive" = "0" ]; then
    write_status "phase=process_exited ts=$ts conn=$conn ready=$ready reached=$reached"
    break
  fi
  if [ "$reached" = "0" ] && [ "$now" -gt "$deadline" ]; then
    write_status "phase=connect_timeout ts=$ts conn=$conn ready=$ready"
    break
  fi
  sleep 10
done

stop_pid "$pid"
wait "$pid" 2>/dev/null || true
write_status "phase=done ts=$(date +%F_%T) final_conn=$(count_connections)"
