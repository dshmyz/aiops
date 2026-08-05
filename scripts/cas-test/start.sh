#!/bin/bash

echo "=========================================="
echo "  QIM CAS 测试服务器"
echo "=========================================="
echo ""

cd "$(dirname "$0")"

CAS_PORT=8443

if command -v docker &> /dev/null; then
    echo "检测到 Docker，使用 Docker 模式启动..."
    docker compose up -d --build

    echo ""
    echo "等待 CAS 服务器启动..."
    sleep 3

    echo ""
    echo "检查服务器状态..."
    if curl -s http://localhost:$CAS_PORT/status > /dev/null 2>&1; then
        echo "CAS 服务器启动成功!"
    else
        echo "CAS 服务器可能还在启动中，请稍等..."
        sleep 2
    fi
else
    echo "未检测到 Docker，使用本地 Python 模式启动..."

    if ! command -v python3 &> /dev/null; then
        echo "错误: 未安装 python3，请先安装 Python 3"
        exit 1
    fi

    if lsof -i :$CAS_PORT > /dev/null 2>&1; then
        echo "错误: 端口 $CAS_PORT 已被占用"
        exit 1
    fi

    USERS_FILE="$(pwd)/users.json" CAS_PORT=$CAS_PORT nohup python3 cas_server.py > cas_server.log 2>&1 &
    echo $! > cas_server.pid
    sleep 2

    if curl -s http://localhost:$CAS_PORT/status > /dev/null 2>&1; then
        echo "CAS 服务器启动成功! (PID: $(cat cas_server.pid))"
    else
        echo "启动失败，请查看 cas_server.log"
        exit 1
    fi
fi

echo ""
echo "=========================================="
echo "  CAS 服务器信息"
echo "=========================================="
echo ""
echo "CAS 服务器地址: http://localhost:8443"
echo "登录地址:      http://localhost:8443/login"
echo "票据验证地址:  http://localhost:8443/serviceValidate"
echo "状态检查地址:  http://localhost:8443/status"
echo "登出地址:      http://localhost:8443/logout"
echo ""
echo "=========================================="
echo "  测试用户"
echo "=========================================="
echo ""
echo "用户1: zhangsan / 123456 (张三 - 技术部前端组)"
echo "用户2: lisi / 123456 (李四 - 技术部前端组)"
echo "用户3: wangwu / 123456 (王五 - 技术部后端组)"
echo "用户4: zhaoliu / 123456 (赵六 - 技术部后端组)"
echo "用户5: sunqi / 123456 (孙七 - 产品部设计组)"
echo "用户6: zhouba / 123456 (周八 - 产品部运营组)"
echo "管理员: admin / admin123 (系统管理员)"
echo ""
echo "=========================================="
echo "  QIM 配置示例"
echo "=========================================="
echo ""
echo "在 QIM 管理后台添加 CAS 认证提供者时使用以下配置："
echo ""
cat << 'EOF'
{
  "server_url": "http://localhost:8443",
  "service_url": "http://localhost:8080/api/v1/auth/cas/callback",
  "validate_url": "http://localhost:8443/serviceValidate",
  "attribute_mapping": {
    "username": "username",
    "nickname": "displayName",
    "email": "mail",
    "phone": "phone"
  }
}
EOF
echo ""
echo "=========================================="
