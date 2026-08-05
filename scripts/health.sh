#!/usr/bin/env bash
#
# health.sh — 健康检查（供监控 / systemd / 外部探活调用）。
#
# 用法:
#   scripts/health.sh                 # 存活 + 就绪 + 指标
#   scripts/health.sh --auth          # 额外用 API_TOKEN 打 /v1/capabilities 验证鉴权链路
#   scripts/health.sh -h             # 帮助
#
# 通过则退出 0；任一失败退出 1 并打印失败项。
# 令牌经环境变量 API_TOKEN 注入（或传入 --auth 时自动 scripts/gen-token.sh 生成）。
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

load_env

auth=0
for arg in "$@"; do
  case "$arg" in
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    --auth) auth=1 ;;
    *) echo "未知参数: $arg" >&2; sed -n '2,10p' "$0" >&2; exit 2 ;;
  esac
done

fail=0
curl --noproxy '*' -sf -o /dev/null "$API_BASE/healthz" 2>/dev/null \
  && echo "healthz  OK" || { echo "healthz  FAIL"; fail=1; }
curl --noproxy '*' -sf -o /dev/null "$API_BASE/readyz" 2>/dev/null \
  && echo "readyz   OK" || { echo "readyz   FAIL"; fail=1; }
curl --noproxy '*' -sf -o /dev/null "$API_BASE/metrics" 2>/dev/null \
  && echo "metrics  OK" || { echo "metrics  FAIL"; fail=1; }

if [ "$auth" -eq 1 ]; then
  token="${API_TOKEN:-}"
  if [ -z "$token" ]; then
    token="$("$SCRIPTS_DIR/gen-token.sh")"
  fi
  if curl --noproxy '*' -sf -o /dev/null -H "Authorization: Bearer $token" "$API_BASE/v1/capabilities" 2>/dev/null; then
    echo "auth     OK"
  else
    echo "auth     FAIL"
    fail=1
  fi
fi

exit "$fail"
