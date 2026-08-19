#!/usr/bin/env python3
"""Стенд ленты в настоящем браузере (ветка poc-chat).

Мок дерева прощает слишком многое: регресс с пустым чатом он пропустил целиком,
потому что предмет там был не в исключении, а в том, что вся лента схлопывалась
в один свёрнутый блок. Этот стенд открывает страницу в headless-chrome и
смотрит собранную разметку глазами браузера, на двух классах чатов: с работой
субагента (боковые журналы) и без неё.

Вход решается прокси: браузеру ходить с кукой неоткуда, а подсовывать её в
профиль Chrome дорого. Прокси слушает свой порт, дописывает куку входа к
каждому запросу и отдаёт ответ дашборда как есть. Кука считается тут же по
секрету из конфига: unix-срок и HMAC-SHA256 от него.

Зовётся: python3 testdata/poc_browser.py <база> <токен> <проект> <сессия>...
"""
import hashlib
import hmac
import http.server
import json
import os
import re
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request

CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"


def cookie(secret, days=30):
    exp = str(int(time.time()) + days * 86400)
    return exp + "." + hmac.new(secret.encode(), exp.encode(), hashlib.sha256).hexdigest()


def proxy(base, value):
    """Прокси с кукой входа. Порт выбирает сама система, чтобы стенд не спорил
    с занятыми."""
    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            # Поток событий обрывается нарочно: он живёт вечно, страница из-за
            # него не успокаивается никогда, и chrome с --dump-dom висит до
            # срока. Лента при этом собирается обычным запросом хвоста, а
            # предмет стенда именно сборка.
            if "stream=1" in self.path:
                self.send_response(204)
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            req = urllib.request.Request(base + self.path)
            req.add_header("Cookie", "dashboard_session=" + value)
            try:
                with urllib.request.urlopen(req, timeout=20) as r:
                    body, code, ct = r.read(), r.status, r.headers.get("Content-Type", "")
            except urllib.error.HTTPError as e:
                body, code, ct = e.read(), e.code, e.headers.get("Content-Type", "")
            except Exception as e:
                body, code, ct = str(e).encode(), 502, "text/plain"
            self.send_response(code)
            if ct:
                self.send_header("Content-Type", ct)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass

    # Многопоточный нарочно: страница шлёт запросы разом, и однопоточный
    # прокси встаёт на первом же, а браузер висит до срока.
    class Threaded(socketserver.ThreadingMixIn, socketserver.TCPServer):
        daemon_threads = True
        allow_reuse_address = True

    srv = Threaded(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, "http://127.0.0.1:%d" % srv.server_address[1]


def look(chrome_base, project, sid, profile):
    page = "%s/#%s/chat/%s" % (chrome_base, project, sid)
    out = subprocess.run(
        # headless=old нарочно: новый режим на этой машине с --dump-dom не
        # выходит вовсе и висит до срока.
        [CHROME, "--headless=old", "--disable-gpu", "--no-sandbox", "--no-first-run",
         "--user-data-dir=" + profile, "--virtual-time-budget=8000", "--dump-dom", page],
        capture_output=True, text=True, timeout=60)
    return out.stdout or "", out.stderr or ""


def run(base, token, project, sessions):
    if not os.path.exists(CHROME):
        print("chrome не найден, стенд пропущен")
        return 0
    srv, chrome_base = proxy(base, cookie(token))
    bad = 0
    try:
        for sid in sessions:
            with tempfile.TemporaryDirectory() as profile:
                try:
                    dom, err = look(chrome_base, project, sid, profile)
                except subprocess.TimeoutExpired:
                    print("сессия %s: chrome не ответил за срок" % sid[:8])
                    bad = 1
                    continue
            body = re.sub(r"<[^>]+>", " ", dom)
            crash = [ln for ln in err.splitlines() if "Uncaught" in ln or "TypeError" in ln]
            if crash:
                print("сессия %s: исключение в консоли: %s" % (sid[:8], crash[0][:160]))
                bad = 1
                continue
            if 'id="cpanel"' not in dom:
                print("сессия %s: панели чата в разметке нет" % sid[:8])
                bad = 1
                continue
            if "чат открывается" in body:
                print("сессия %s: лента осталась на заглушке открытия" % sid[:8])
                bad = 1
                continue
            # Лента непуста: в ней есть либо пузыри разговора, либо блок работы
            # субагента. Пустая панель это ровно тот регресс, ради которого
            # стенд и заведён.
            if "mlist" not in dom and "subblk" not in dom and "msg" not in dom:
                print("сессия %s: лента пуста, ни реплик, ни работы субагента" % sid[:8])
                bad = 1
                continue
            kind = "с работой субагента" if "subblk" in dom else "обычный"
            print("сессия %s: панель собралась, лента непуста (%s)" % (sid[:8], kind))
    finally:
        srv.shutdown()
    return bad


if __name__ == "__main__":
    if len(sys.argv) < 5:
        sys.stderr.write(__doc__)
        sys.exit(2)
    sys.exit(run(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]))
