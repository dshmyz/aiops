#!/usr/bin/env bash
#
# nginx.sh — 渲染 / 校验宿主机 nginx 反代配置。
#
# 用法:
#   scripts/nginx.sh generate   # 从模板渲染 deploy/console-nginx.conf
#   scripts/nginx.sh test       # 渲染并 nginx -t 校验（需本机装 nginx）
#   scripts/nginx.sh print      # 渲染并打印到 stdout
#   scripts/nginx.sh -h         # 帮助
#
# 变量: CONSOLE_PORT(默认 8080) / COPILOT_HTTP_ADDR(默认 0.0.0.0:18080) 决定监听端口与上游。
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

DEPLOY_DIR="$REPO_ROOT/deploy"
TEMPLATE="$DEPLOY_DIR/console-nginx.conf.template"
OUTPUT="$DEPLOY_DIR/console-nginx.conf"

# 上游地址：只保留 host:port，去掉监听的前缀绑定；默认 127.0.0.1:API_PORT
API_UPSTREAM="${API_UPSTREAM:-127.0.0.1:${API_PORT}}"

case "${1:-}" in
  -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
  generate|test|print) : ;;
  *) echo "未知子命令: ${1:-}（generate|test|print）" >&2; sed -n '2,10p' "$0" >&2; exit 2 ;;
esac

[ -f "$TEMPLATE" ] || { echo "模板缺失: $TEMPLATE" >&2; exit 1; }

render() {
  sed -e "s|__WEB_DIR__|$WEB_DIR|g" \
      -e "s|__CONSOLE_PORT__|$CONSOLE_PORT|g" \
      -e "s|__API_UPSTREAM__|$API_UPSTREAM|g" \
      "$TEMPLATE"
}

cmd="${1:-generate}"
case "$cmd" in
  print)
    render
    ;;
  generate)
    render > "$OUTPUT"
    echo "rendered $OUTPUT (listen :$CONSOLE_PORT, upstream $API_UPSTREAM, root $WEB_DIR)"
    ;;
  test)
    render > "$OUTPUT"
    if command -v nginx >/dev/null 2>&1; then
      nginx -t -c "$OUTPUT" && echo "nginx -t OK: $OUTPUT"
    else
      echo "本机未装 nginx，跳过 nginx -t（配置已渲染到 $OUTPUT）"
    fi
    ;;
esac
