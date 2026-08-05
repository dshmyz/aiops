# copilot-api — 多阶段构建
#
# 运行期完全由环境变量配置（无需 config 文件）。镜像内一并携带运行期依赖：
#   migrations/   —— MySQL 迁移（启动时自动应用）
#   prompts/      —— prompt 模板（热加载）
#   docs/         —— 「使用手册」等 markdown（COPILOT_DOCS_DIR 留空时相对工作目录读取）
# 密钥（JWT_SECRET / API_KEY / webhook secret 等）一律经环境变量 / Secret 注入，绝不写入镜像。

# --- build 阶段 ---
FROM golang:1.25-alpine AS build
WORKDIR /src

# 先拷贝 go.mod / go.sum 以最大化依赖层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO 关闭以产出纯静态二进制（SQLite 驱动仅在 migrate/开发构建使用，发布走 MySQL）
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/copilot-api ./cmd/copilot-api

# --- 运行阶段 ---
FROM alpine:3.20
# ca-certificates 供外部调用（LLM / 中间件 / MCP）使用 TLS
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=build /out/copilot-api /app/copilot-api
COPY migrations /app/migrations
COPY prompts /app/prompts
COPY docs /app/docs

ENV COPILOT_HTTP_ADDR=:18080 \
    COPILOT_DOCS_DIR=/app/docs \
    COPILOT_PROMPTS_DIR=/app/prompts

EXPOSE 18080

# 非 root 运行
RUN addgroup -S copilot && adduser -S -G copilot copilot
USER copilot

ENTRYPOINT ["/app/copilot-api"]
