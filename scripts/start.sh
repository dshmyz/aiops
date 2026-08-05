#!/usr/bin/env bash
#
# start.sh — 启动后端 copilot-api（pid 管理，幂等）。
#
# 用法:
#   scripts/start.sh                # 启动后端
#   scripts/start.sh --force        # 已运行时强制重启
#   scripts/start.sh --web          # 额外生成宿主机 nginx 配置（deploy/console-nginx.conf）
#   scripts/start.sh -h            # 帮助
#
# 前置: bin/copilot-api 已构建（scripts/build.sh）；migrations/ 可读；
#       若 COPILOT_DATABASE_DRIVER=mysql 需 DB 可达。
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

force=0; gen_web=0
for arg in "$@"; do
  case "$arg" in
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    --force) force=1 ;;
    --web) gen_web=1 ;;
    *) echo "未知参数: $arg" >&2; sed -n '2,11p' "$0" >&2; exit 2 ;;
  esac
done

load_env
export COPILOT_HTTP_ADDR="${COPILOT_HTTP_ADDR:-$API_ADDR}"

log() { printf '[start] %s\n' "$*"; }
err() { printf '[start] ERROR: %s\n' "$*" >&2; }

# 前置校验
[ -x "$API_BIN" ] || { err "后端二进制不存在: $API_BIN（先跑 scripts/build.sh）"; exit 1; }
[ -d "$RUN_ROOT/migrations" ] || { err "migrations/ 不可读（迁移启动时自动应用）"; exit 1; }

# 已有存活实例
if existing="$(read_live_pid "$API_PID")"; then
  if [ "$force" -eq 1 ]; then
    log "已有实例 pid=$existing，--force 触发重启"
    "$SCRIPTS_DIR/stop.sh"
  else
    log "copilot-api 已在运行 (pid=$existing, $API_BASE)。如要重启用 --force 或先 scripts/stop.sh"
  fi
else
  : # 未运行，继续启动
fi

# 幂等：force 已停旧实例，或未运行才启动
if ! read_live_pid "$API_PID" >/dev/null 2>&1; then
  log "starting copilot-api -> $API_LOG"
  nohup "$API_BIN" >> "$API_LOG" 2>&1 &
  echo $! > "$API_PID"
fi

# 轮询就绪
ready=0
for i in $(seq 1 30); do
  if curl --noproxy '*' -sf -o /dev/null "$API_BASE/readyz" 2>/dev/null; then
    ready=1; break
  fi
  sleep 1
done

if [ "$ready" -eq 1 ]; then
  log "copilot-api UP: $API_BASE (pid=$(cat "$API_PID"))"
else
  err "copilot-api 未在 30s 内就绪。查看日志: tail -f $API_LOG"
  exit 1
fi

# 生成宿主机 nginx 配置
if [ "$gen_web" -eq 1 ]; then
  "$SCRIPTS_DIR/nginx.sh" generate
fi

exit 0
