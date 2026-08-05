#!/usr/bin/env bash
#
# logs.sh — 跟踪 copilot-api 日志。
#
# 用法:
#   scripts/logs.sh        # tail -f 跟踪
#   scripts/logs.sh 50     # 查看最近 50 行（不跟随）
#   scripts/logs.sh -h     # 帮助
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

case "${1:-}" in
  -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
esac

[ -f "$API_LOG" ] || { echo "无日志文件: $API_LOG" >&2; exit 1; }

if [ $# -gt 0 ]; then
  exec tail -n "$1" "$API_LOG"
else
  exec tail -F "$API_LOG"
fi
