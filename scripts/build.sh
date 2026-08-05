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
#   bin/copilot-api                   后端二进制（内嵌前端 SPA，单二进制托管整个系统）
#   web/                              前端静态产物（从 apps/capability-console/dist 拷贝，可选：nginx 独立托管时用）
#   internal/webui/dist/              前端构建产物拷贝到此（//go:embed 来源；发布构建覆盖占位）
#
# 单二进制为默认形态：scripts/build.sh（全量）会把前端构建产物拷入 internal/webui/dist
# 再 go build，产出的 copilot-api 直接托管 SPA（浏览器访问 http://host:18080）。
# web/ + nginx 反代仍保留为可选（需要 TLS / 80 端口 / 更细静态控制时）。
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

# //go:embed 的源目录：构建脚本把前端产物拷到这里，再编译后端。
EMBED_DIR="$REPO_ROOT/internal/webui/dist"

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
  log "copy dist -> $EMBED_DIR (//go:embed 来源)"
  rm -rf "$EMBED_DIR"
  mkdir -p "$EMBED_DIR"
  cp -R "$console/dist"/. "$EMBED_DIR/"
  log "web built: $WEB_DIR"
}

case "$mode" in
  backend) build_backend ;;
  web) build_web ;;
  # 全量必须先构建前端并拷入 embed 源，再编译后端，否则二进制内嵌的是占位文件。
  all) build_web; build_backend ;;
esac

log "done."
