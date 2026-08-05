# capability-console — 多阶段构建
#
# SPA 用相对 /v1 路径 fetch（见 src/api.ts），生产由 nginx 同源托管静态文件并把
# /v1 反向代理到 copilot-api，否则相对路径与 CAS 跳转无法工作。

# --- build 阶段 ---
FROM node:20-alpine AS build
WORKDIR /app

# 先拷贝 lockfile 以最大化依赖层缓存
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

# --- serve 阶段 ---
FROM nginx:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80
