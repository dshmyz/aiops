#!/usr/bin/env bash
#
# status.sh — 查看 copilot-api 运行状态。
#
# 用法:
#   scripts/status.sh     # 输出 UP / DOWN / STOPPED
#   scripts/status.sh -h  # 帮助
#
# 退出码: UP=0; DOWN/STOPPED=1（便于健康监控引用）。
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

case "${1:-}" in
  -h|--help) sed -n '2,9p' "$0"; exit 0 ;;
esac

if ! pid="$(read_live_pid "$API_PID")"; then
  echo "STOPPED"
  exit 1
fi

if curl --noproxy '*' -sf -o /dev/null "$API_BASE/healthz" 2>/dev/null; then
  echo "UP (pid=$pid, $API_BASE)"
  exit 0
else
  echo "DOWN (pid=$pid but /healthz unreachable)"
  exit 1
fi
