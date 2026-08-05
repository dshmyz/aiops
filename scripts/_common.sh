#!/usr/bin/env bash
# 共享的路径 / 环境解析逻辑，被 scripts/*.sh 通过 `source "$(dirname "$0")/_common.sh"` 引入。
set -euo pipefail

# 仓库根：本脚本位于 <repo>/scripts/，上溯一级即仓库根。
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPTS_DIR/.." && pwd)"

# 部署根（也即进程工作目录，后端相对 cwd 读 migrations/）。
# 默认仓库根；可用 RUN_ROOT / AIOS_HOME 覆盖（例如打包部署到 /opt/aiops）。
if [ -n "${RUN_ROOT:-}" ]; then
  RUN_ROOT="$(cd "$RUN_ROOT" && pwd)"
elif [ -n "${AIOS_HOME:-}" ]; then
  RUN_ROOT="$(cd "$AIOS_HOME" && pwd)"
else
  RUN_ROOT="$REPO_ROOT"
fi

# 目录
BIN_DIR="$RUN_ROOT/bin"
RUN_DIR="$RUN_ROOT/run"
LOG_DIR="$RUN_ROOT/log"
WEB_DIR="$RUN_ROOT/web"

# 文件
API_BIN="$BIN_DIR/copilot-api"
API_PID="$RUN_DIR/api.pid"
API_LOG="$LOG_DIR/api.log"

# 控制台端口（宿主机 nginx 监听；80 需 root，默认 8080）
CONSOLE_PORT="${CONSOLE_PORT:-8080}"

mkdir -p "$BIN_DIR" "$RUN_DIR" "$LOG_DIR"

# 载入 .env（若存在），但只"填充"未设置的变量——与 main.go 的 loadDotEnv 语义一致：
# 已存在的真实环境变量优先，绝不覆盖。这样外面注入的 COPILOT_HTTP_ADDR / DSN 等不会被 .env 顶掉。
load_env() {
  local env_file="$RUN_ROOT/.env"
  [ -f "$env_file" ] || return 0
  while IFS='=' read -r key rest; do
    key="${key#"export "}"          # 容忍 "export KEY=..."
    key="${key%"${key##*[![:space:]]}"}"  # 去尾部空白（保守）
    case "$key" in
      ''|\#*) continue ;;                 # 空行 / 注释
      *[!A-Za-z0-9_]*) continue ;;        # 只接受简单标识符
    esac
    # 仅当该变量当前未设置时才填充
    if [ -z "${!key+x}" ]; then
      export "$key=$rest"
    fi
  done < "$env_file"
}

load_env

# 后端监听地址的解析顺序与后端一致：真实环境变量 → .env → 默认 0.0.0.0:18080。
# env 已在上面 load_env 之后才取，保证与进程实际绑定一致。
API_ADDR="${COPILOT_HTTP_ADDR:-0.0.0.0:18080}"
API_PORT="$(printf '%s' "$API_ADDR" | sed -E 's/^.*://')"
API_BASE="http://127.0.0.1:${API_PORT}"

# 取 pid 文件里存活的 pid，否则返回空
read_live_pid() {
  local pid_file="$1"
  [ -f "$pid_file" ] || return 1
  local pid
  pid="$(tr -d '[:space:]' < "$pid_file")"
  [ -n "$pid" ] || return 1
  if kill -0 "$pid" 2>/dev/null; then
    printf '%s' "$pid"
    return 0
  fi
  return 1
}
