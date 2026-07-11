#!/usr/bin/env bash
set -euo pipefail

BASE_DIR=${BASE_DIR:-"$HOME/MFL/openim-bench"}
BIN=${BIN:-"$BASE_DIR/bin/msgtest-linux-amd64"}
RUN_DIR=${RUN_DIR:-"$BASE_DIR/runs"}

MODE=${MODE:-online_hold}
RUN_ID=${RUN_ID:-"$(hostname)-${MODE}-$(date +%Y%m%d-%H%M%S)"}

API_ADDR=${API_ADDR:-"http://172.31.50.2:10002"}
WS_ADDR=${WS_ADDR:-"ws://172.31.50.2:10001"}
TEST_IP=${TEST_IP:-"172.31.50.2"}
WS_HOSTPORT=${WS_HOSTPORT:-"172.31.50.2:10001"}

ONLINE=${ONLINE:-1000}
START_USER=${START_USER:-9000000}
END_USER=${END_USER:-$((START_USER + ONLINE))}
SAMPLE_RATE=${SAMPLE_RATE:-0.01}
REGISTER=${REGISTER:-1}
HOLD_SECONDS=${HOLD_SECONDS:-1200}
CONNECT_TIMEOUT_SECONDS=${CONNECT_TIMEOUT_SECONDS:-1800}

RANDOM_SENDERS=${RANDOM_SENDERS:-0}
RANDOM_RECEIVERS=${RANDOM_RECEIVERS:-0}
MESSAGE_COUNT=${MESSAGE_COUNT:-0}
SEND_INTERVAL_MS=${SEND_INTERVAL_MS:-1000}
EXIT_AFTER_SEND=${EXIT_AFTER_SEND:-0}

GROUP_100K=${GROUP_100K:-0}
GROUP_10K=${GROUP_10K:-0}
GROUP_1K=${GROUP_1K:-0}
GROUP_100=${GROUP_100:-0}
GROUP_50=${GROUP_50:-0}
GROUP_10=${GROUP_10:-0}
GROUP_SENDER_RATE=${GROUP_SENDER_RATE:-0.1}
GROUP_ONLINE_RATE=${GROUP_ONLINE_RATE:-0}

mkdir -p "$RUN_DIR"

STATUS_FILE="$RUN_DIR/${RUN_ID}.status"
LOG_FILE="$RUN_DIR/${RUN_ID}.msgtest.log"
PID_FILE="$RUN_DIR/${RUN_ID}.pid"

count_connections() {
  ss -Htan state established 2>/dev/null | awk -v hp="$WS_HOSTPORT" 'index($0, hp) {c++} END{print c+0}'
}

write_status() {
  printf '%s\n' "$*" | tee -a "$STATUS_FILE"
}

build_common_args() {
  local args=(-s "$START_USER" -e "$END_USER" -o "$ONLINE" -sr "$SAMPLE_RATE")
  if [ "$REGISTER" = "1" ]; then
    args+=(-r)
  fi
  args+=(-gsr "$GROUP_SENDER_RATE" -gor "$GROUP_ONLINE_RATE")
  args+=(-htg "$GROUP_100K" -ttg "$GROUP_10K" -otg "$GROUP_1K" -hog "$GROUP_100" -fog "$GROUP_50" -teg "$GROUP_10")
  printf '%q ' "${args[@]}"
}

run_msgtest() {
  local extra=("$@")
  ulimit -n "${ULIMIT_NOFILE:-100000}" 2>/dev/null || true
  export OPENIM_API_ADDR="$API_ADDR"
  export OPENIM_WS_ADDR="$WS_ADDR"
  export OPENIM_TEST_IP="$TEST_IP"
  export OPENIM_EXIT_AFTER_SEND="$EXIT_AFTER_SEND"
  # shellcheck disable=SC2207
  local common=($(build_common_args))
  if [ "${RUN_WITHOUT_EXEC:-0}" = "1" ]; then
    "$BIN" "${common[@]}" "${extra[@]}" >"$LOG_FILE" 2>&1
  else
    exec "$BIN" "${common[@]}" "${extra[@]}" >"$LOG_FILE" 2>&1
  fi
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

stop_all_msgtest() {
  for proc in /proc/[0-9]*; do
    exe=$(readlink -f "$proc/exe" 2>/dev/null || true)
    if [ "$exe" = "$BIN" ]; then
      stop_pid "${proc#/proc/}"
    fi
  done
}

case "$MODE" in
  online_hold)
    write_status "run_id=$RUN_ID"
    write_status "mode=$MODE target=$WS_HOSTPORT online=$ONLINE hold_seconds=$HOLD_SECONDS"
    run_msgtest -u &
    pid=$!
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
    ;;

  message)
    write_status "run_id=$RUN_ID"
    write_status "mode=$MODE target=$WS_HOSTPORT online=$ONLINE rs=$RANDOM_SENDERS rr=$RANDOM_RECEIVERS count=$MESSAGE_COUNT interval_ms=$SEND_INTERVAL_MS"
    set +e
    RUN_WITHOUT_EXEC=1 run_msgtest -rs "$RANDOM_SENDERS" -rr "$RANDOM_RECEIVERS" -c "$MESSAGE_COUNT" -i "$SEND_INTERVAL_MS"
    code=$?
    set -e
    write_status "phase=done ts=$(date +%F_%T) exit_code=$code final_conn=$(count_connections)"
    exit 0
    ;;

  stop)
    if [ -f "$PID_FILE" ]; then
      stop_pid "$(cat "$PID_FILE")"
    fi
    ;;

  cleanup)
    stop_all_msgtest
    write_status "phase=cleanup_done ts=$(date +%F_%T) final_conn=$(count_connections)"
    ;;

  *)
    echo "unknown MODE=$MODE" >&2
    exit 2
    ;;
esac
