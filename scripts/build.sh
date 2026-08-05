#!/usr/bin/env bash
#
# build.sh — 构建后端二进制与前端产物到部署根。
#
# 用法:
#   scripts/build.sh                  # 构建后端 + 前端
#   scripts/build.sh --backend-only   # 只构建 Go 后端
#   scripts/build.sh --web-only       # 只构建前端
#   scripts/build.sh -h               # 帮助
#
# 产物:
#   bin/copilot-api                   后端二进制
#   web/                              前端静态产物（从 apps/capability-console/dist 拷贝）
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

mode="all"
case "${1:-}" in
  -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
  --backend-only) mode="backend" ;;
  --web-only) mode="web" ;;
  ""|--) : ;;
  *) echo "未知参数: $1" >&2; sed -n '2,7p' "$0" >&2; exit 2 ;;
esac

log() { printf '[build] %s\n' "$*"; }

build_backend() {
  log "go build -> $API_BIN"
  # 注意：SQLite 驱动（go-sqlite3）依赖 CGO，不能全局硬关 CGO，否则本地/测试会
  # "Binary was compiled with CGO_ENABLED=0 ... stub" 崩掉。默认用平台默认（macOS/多数 Linux
  # 自带 CC 可用）；仅当确定生产纯 MySQL 且环境无 CGO 时，可用 CGO_ENABLED=0 覆盖。
  go build -trimpath -ldflags="-s -w" -o "$API_BIN" ./cmd/copilot-api
  log "backend built: $API_BIN"
}

build_web() {
  local console="$REPO_ROOT/apps/capability-console"
  log "npm ci + build in $console"
  ( cd "$console" && npm ci && npm run build )
  log "copy dist -> $WEB_DIR"
  rm -rf "$WEB_DIR"
  cp -R "$console/dist" "$WEB_DIR"
  log "web built: $WEB_DIR"
}

case "$mode" in
  backend) build_backend ;;
  web) build_web ;;
  all) build_backend; build_web ;;
esac

log "done."
