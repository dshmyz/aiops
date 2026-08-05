"""
QIM CAS 测试服务器
轻量级 CAS 2.0 协议实现，用于本地开发和测试
"""

import json
import uuid
import os
from datetime import datetime, timedelta
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs, urlencode

TICKET_STORE = {}
USERS = {}
TICKET_EXPIRY = 300  # 5 分钟

AUTH_HTML = """<!DOCTYPE html>
<html lang="zh">
<head>
    <meta charset="UTF-8">
    <title>CAS 登录 - QIM 测试</title>
    <style>
        body {{ font-family: Arial, sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; background: #f0f2f5; }}
        .login-box {{ background: white; padding: 40px; border-radius: 8px; box-shadow: 0 2px 12px rgba(0,0,0,0.1); width: 320px; }}
        h2 {{ text-align: center; color: #333; margin-bottom: 30px; }}
        .form-group {{ margin-bottom: 20px; }}
        label {{ display: block; margin-bottom: 5px; color: #555; font-size: 14px; }}
        input[type="text"], input[type="password"] {{ width: 100%; padding: 10px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; font-size: 14px; }}
        input[type="text"]:focus, input[type="password"]:focus {{ border-color: #409eff; outline: none; }}
        button {{ width: 100%; padding: 12px; background: #409eff; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 16px; }}
        button:hover {{ background: #337ecc; }}
        .error {{ color: #f56c6c; background: #fef0f0; padding: 10px; border-radius: 4px; margin-bottom: 15px; text-align: center; font-size: 14px; }}
        .hint {{ text-align: center; color: #999; font-size: 12px; margin-top: 15px; }}
    </style>
</head>
<body>
    <div class="login-box">
        <h2>CAS 统一认证</h2>
        {error_msg}
        <form method="POST" action="/login">
            <input type="hidden" name="service" value="{service}">
            <div class="form-group">
                <label>用户名</label>
                <input type="text" name="username" placeholder="请输入用户名" required autofocus>
            </div>
            <div class="form-group">
                <label>密码</label>
                <input type="password" name="password" placeholder="请输入密码" required>
            </div>
            <button type="submit">登 录</button>
        </form>
        <div class="hint">QIM CAS 测试服务器</div>
    </div>
</body>
</html>"""


def load_users():
    global USERS
    users_file = os.environ.get("USERS_FILE", "/app/users.json")
    if os.path.exists(users_file):
        with open(users_file, "r", encoding="utf-8") as f:
            USERS = json.load(f)
        print(f"已加载 {len(USERS)} 个测试用户")
    else:
        USERS = {
            "zhangsan": {
                "password": "123456",
                "displayName": "张三",
                "mail": "zhangsan@qim.local",
                "phone": "13800000001",
                "department": "技术部-前端组"
            },
            "lisi": {
                "password": "123456",
                "displayName": "李四",
                "mail": "lisi@qim.local",
                "phone": "13800000002",
                "department": "技术部-前端组"
            },
            "admin": {
                "password": "admin123",
                "displayName": "系统管理员",
                "mail": "admin@qim.local",
                "phone": "13800000000",
                "department": "管理层"
            }
        }
        print("使用内置测试用户")


def generate_ticket(prefix="ST"):
    return f"{prefix}-{uuid.uuid4().hex[:32]}"


def cleanup_expired_tickets():
    now = datetime.now()
    expired = [t for t, info in TICKET_STORE.items() if now > info["expires"]]
    for t in expired:
        del TICKET_STORE[t]


class CASHandler(BaseHTTPRequestHandler):

    def log_message(self, format, *args):
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        print(f"[{timestamp}] {args[0]}")

    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path
        params = parse_qs(parsed.query)

        if path == "/login":
            self.handle_login_page(params)
        elif path == "/serviceValidate" or path == "/p3/serviceValidate":
            # copilot-api 的 CASAuthenticator 固定校验 ServerURL + "/p3/serviceValidate"，
            # 因此这里同时接受带 /p3 前缀的路径（标准 CAS 3.0 端点）。
            self.handle_service_validate(params)
        elif path == "/logout":
            self.handle_logout(params)
        elif path == "/status":
            self.handle_status()
        else:
            self.send_error_page(404, "页面不存在")

    def do_POST(self):
        parsed = urlparse(self.path)
        path = parsed.path

        if path == "/login":
            content_length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(content_length).decode("utf-8")
            params = parse_qs(body)
            self.handle_login_submit(params)
        else:
            self.send_error_page(404, "页面不存在")

    def handle_login_page(self, params):
        service = params.get("service", [""])[0]
        error_msg = params.get("error", [""])[0]
        error_html = f'<div class="error">{error_msg}</div>' if error_msg else ""

        html = AUTH_HTML.format(
            service=service,
            error_msg=error_html
        )

        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(html.encode("utf-8"))

    def handle_login_submit(self, params):
        username = params.get("username", [""])[0]
        password = params.get("password", [""])[0]
        service = params.get("service", [""])[0]
        
        print(f"登录提交: username={username}, service={service}")

        if not username or not password:
            self.redirect_to_login(service, "用户名和密码不能为空")
            return

        user = USERS.get(username)
        if not user or user.get("password") != password:
            self.redirect_to_login(service, "用户名或密码错误")
            return

        cleanup_expired_tickets()

        ticket = generate_ticket()
        TICKET_STORE[ticket] = {
            "username": username,
            "service": service,
            "user_info": {k: v for k, v in user.items() if k != "password"},
            "created": datetime.now(),
            "expires": datetime.now() + timedelta(seconds=TICKET_EXPIRY)
        }

        print(f"用户 {username} 登录成功，生成票据: {ticket}")

        if service:
            separator = "&" if "?" in service else "?"
            redirect_url = f"{service}{separator}ticket={ticket}"
            self.send_response(302)
            self.send_header("Location", redirect_url)
            self.end_headers()
        else:
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(f"""
                <html><body style="font-family:Arial,sans-serif;text-align:center;padding:50px;">
                <h2>登录成功</h2>
                <p>用户: {username}</p>
                <p>票据: <code>{ticket}</code></p>
                <p style="color:#999;">未指定 service 参数，无法自动跳转</p>
                </body></html>
            """.encode("utf-8"))

    def handle_service_validate(self, params):
        ticket = params.get("ticket", [""])[0]
        service = params.get("service", [""])[0]

        if not ticket:
            self.send_cas_response(False, error_code="INVALID_REQUEST", error_message="缺少 ticket 参数")
            return

        ticket_info = TICKET_STORE.get(ticket)
        if not ticket_info:
            self.send_cas_response(False, error_code="INVALID_TICKET", error_message=f"票据 {ticket} 不存在或已过期")
            return

        if datetime.now() > ticket_info["expires"]:
            del TICKET_STORE[ticket]
            self.send_cas_response(False, error_code="INVALID_TICKET", error_message="票据已过期")
            return

        # 比较基础 URL（忽略 query 参数如 state）
        if service and ticket_info["service"]:
            service_base = service.split('?')[0]
            ticket_service_base = ticket_info["service"].split('?')[0]
            if service_base != ticket_service_base:
                self.send_cas_response(False, error_code="INVALID_SERVICE", error_message="Service 不匹配")
                return

        username = ticket_info["username"]
        user_info = ticket_info["user_info"]

        del TICKET_STORE[ticket]

        print(f"票据验证成功: {ticket} -> {username}")

        self.send_cas_response(True, username=username, attributes=user_info)

    def handle_logout(self, params):
        service = params.get("service", [""])[0]

        html = """<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>CAS 登出</title>
<style>body{font-family:Arial,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#f0f2f5;}
.box{{background:white;padding:40px;border-radius:8px;box-shadow:0 2px 12px rgba(0,0,0,0.1);text-align:center;}}
a{{color:#409eff;text-decoration:none;}}</style></head>
<body><div class="box"><h2>已成功登出</h2><p>您已安全退出 CAS 认证系统</p>
<p><a href="{service}">返回应用</a></p></div></body></html>""".format(service=service)

        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(html.encode("utf-8"))

    def handle_status(self):
        cleanup_expired_tickets()
        status = {
            "status": "running",
            "users_count": len(USERS),
            "active_tickets": len(TICKET_STORE),
            "server_time": datetime.now().isoformat()
        }
        self.send_response(200)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.end_headers()
        self.wfile.write(json.dumps(status, ensure_ascii=False).encode("utf-8"))

    def send_cas_response(self, success, username="", attributes=None, error_code="", error_message=""):
        if success:
            attrs_xml = ""
            if attributes:
                for key, value in attributes.items():
                    attrs_xml += f"        <{key}>{value}</{key}>\n"

            xml = f"""<?xml version="1.0" encoding="UTF-8"?>
<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>{username}</cas:user>
{attrs_xml}  </cas:authenticationSuccess>
</cas:serviceResponse>"""
        else:
            xml = f"""<?xml version="1.0" encoding="UTF-8"?>
<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationFailure code="{error_code}">
    {error_message}
  </cas:authenticationFailure>
</cas:serviceResponse>"""

        self.send_response(200)
        self.send_header("Content-Type", "application/xml; charset=utf-8")
        self.end_headers()
        self.wfile.write(xml.encode("utf-8"))

    def redirect_to_login(self, service, error):
        params = {"error": error}
        if service:
            params["service"] = service
        self.send_response(302)
        self.send_header("Location", f"/login?{urlencode(params)}")
        self.end_headers()

    def send_error_page(self, code, message):
        self.send_response(code)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(f"""
            <html><body style="font-family:Arial,sans-serif;text-align:center;padding:50px;">
            <h2>{code}</h2><p>{message}</p>
            <p><a href="/status">查看服务器状态</a></p>
            </body></html>
        """.encode("utf-8"))


def main():
    port = int(os.environ.get("CAS_PORT", "8443"))
    load_users()

    server = ThreadingHTTPServer(("0.0.0.0", port), CASHandler)
    print(f"CAS 测试服务器启动在 http://0.0.0.0:{port}")
    print(f"登录地址: http://localhost:{port}/login")
    print(f"状态检查: http://localhost:{port}/status")
    print("按 Ctrl+C 停止服务器")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n服务器已停止")
        server.server_close()


if __name__ == "__main__":
    main()
