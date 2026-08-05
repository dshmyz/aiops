#!/usr/bin/env bash
#
# gen-token.sh — 生成 24h 有效的 admin JWT（开发/联调用）。
#
# 用法:
#   scripts/gen-token.sh     # 打印 token 到 stdout
#   scripts/gen-token.sh -h  # 帮助
#
# 读取 ./.env 里的 COPILOT_JWT_HMAC_SECRET；未设则报错退出 1。
# 仅用于本地联调 / 探活，不要用作生产身份体系。
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPTS_DIR/_common.sh"

case "${1:-}" in
  -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
esac

load_env

if [ -z "${COPILOT_JWT_HMAC_SECRET:-}" ]; then
  echo "COPILOT_JWT_HMAC_SECRET is not set (check .env)" >&2
  exit 1
fi

( cd "$RUN_ROOT" && go run ./gen_token.go )
