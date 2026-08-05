#!/bin/bash

echo "正在停止 CAS 测试服务器..."
cd "$(dirname "$0")"

if command -v docker &> /dev/null && docker ps | grep -q qim-cas-test; then
    docker compose down
else
    if [ -f cas_server.pid ]; then
        PID=$(cat cas_server.pid)
        if ps -p $PID > /dev/null 2>&1; then
            kill $PID
            rm -f cas_server.pid cas_server.log
            echo "已停止本地 CAS 服务器 (PID: $PID)"
        else
            rm -f cas_server.pid cas_server.log
            echo "CAS 服务器未运行"
        fi
    else
        echo "未找到运行中的 CAS 服务器"
    fi
fi

echo "已停止"
