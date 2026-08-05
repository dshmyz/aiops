#!/usr/bin/env bash
#
# stop.sh — 优雅停止 copilot-api（pid 管理）。
#
# 用法:
#   scripts/stop.sh     # 停止后端
#   scripts/stop.sh -h  # 帮助
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

case "${1:-}" in
  -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
esac

log() { printf '[stop] %s\n' "$*"; }

if pid="$(read_live_pid "$API_PID")"; then
  log "sending TERM to pid=$pid"
  kill "$pid"
  # 等待优雅退出，最多 ~10s，超时 SIGKILL
  for _ in $(seq 1 10); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done
  if kill -0 "$pid" 2>/dev/null; then
    log "process did not exit in time, sending KILL"
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$API_PID"
  log "stopped"
else
  rm -f "$API_PID"
  log "copilot-api 未在运行"
fi
exit 0
