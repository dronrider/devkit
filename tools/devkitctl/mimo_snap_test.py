#!/usr/bin/env python3
"""Съёмщик mimo, kit/harness/snap/mimo.sh: продление сессии и вход по паролю.

На стенде работает локальный http-сервер, который играет и кабинет, и
пасспорт. Usage отдаёт снимок только по живому serviceToken, на мёртвый токен
отвечает 401 с loginUrl. По этому адресу цепочка с пасспортной кукой
переиздаёт сессию, а Auth2 принимает вход по md5-хешу пароля. Заглушка
secretctl на PATH пишет set в файл и читает exec оттуда. Проверяется механика
обновления кук, а не сеть и не хранилище.
"""
import datetime
import hashlib
import json
import os
import shutil
import tempfile
import threading
import unittest
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from testenv import DEVKIT_SRC, executable, run

SNAP = DEVKIT_SRC / "kit" / "harness" / "snap" / "mimo.sh"

USAGE = {"data": {"monthUsage": {"items": [
    {"name": "Token Plan", "used": 40, "limit": 100, "percent": 40}]}}}

STUB_SECRETCTL = """#!/bin/sh
# Заглушка secretctl для стенда. set пишет stdin в файл, exec печатает файл.
dir="$HOME/.devkit/stub-secrets"
mkdir -p "$dir"
case "$1" in
set)
	printf '%s\\n' "$2" >> "$dir/log"
	cat > "$dir/$2"
	;;
exec)
	[ -f "$dir/$2" ] || exit 1
	cat "$dir/$2"
	;;
*)
	exit 2
	;;
esac
"""


class Cab:
    """Состояние стенда между запросами: счётчики путей, выданные токены и
    пара учётной записи, которую ждёт Auth2."""

    base = ""
    calls = []
    tokens = set()
    expected = ()
    twofa_user = "подтверждение@стенд"

    @classmethod
    def reset(cls):
        cls.calls = []
        cls.tokens = {"valid-stend"}
        cls.expected = ()


def cookie_token(header):
    for chunk in (header or "").split(";"):
        k, _, v = chunk.partition("=")
        if k.strip() == "api-platform_serviceToken":
            return v.strip()
    return None


def pass_token(header):
    for chunk in (header or "").split(";"):
        k, _, v = chunk.partition("=")
        if k.strip() == "passToken":
            return v.strip()
    return None


def jsonp_start(doc):
    return ("&&&START&&&" + json.dumps(doc, ensure_ascii=False)).encode("utf-8")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, code, body=b"", headers=()):
        self.send_response(code)
        for key, val in headers:
            self.send_header(key, val)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.route()

    def do_POST(self):
        self.route()

    def route(self):
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        Cab.calls.append(parsed.path)
        cookie = self.headers.get("Cookie", "")
        if parsed.path == "/api/v1/tokenPlan/usage":
            if cookie_token(cookie) in Cab.tokens:
                self.reply(200, json.dumps(USAGE).encode("utf-8"),
                           [("Content-Type", "application/json")])
            else:
                # Мёртвый токен получает тот же ответ, что и живая платформа.
                # Это 401 с подписанным loginUrl, готовой дорогой продления.
                followup = Cab.base + "/api/v1/tokenPlan/usage"
                callback = (Cab.base + "/sts?sign=stend-sign&followup=" +
                            urllib.parse.quote(followup, safe=""))
                login_url = (Cab.base + "/pass/serviceLogin?callback=" +
                             urllib.parse.quote(callback, safe=""))
                body = json.dumps({"code": 401, "loginUrl": login_url}).encode("utf-8")
                self.reply(401, body, [("Content-Type", "application/json")])
        elif parsed.path == "/pass/serviceLogin":
            if "callback" in query:
                # На дороге продления живая пасспортная кука получает переиздание
                # сессии, мёртвая и отсутствующая отсылается к форме входа.
                if pass_token(cookie) == "valid-passport":
                    self.reply(302, headers=[
                        ("Location", (query.get("callback") or [""])[0]),
                        ("Set-Cookie", "passToken=fresh-passport; Path=/")])
                else:
                    self.reply(200, jsonp_start(
                        {"code": 70016, "description": "вход не пройден"}))
            else:
                # Форма входа для Auth2. Поля подписи уходят и живой куке,
                # и мёртвой, вход их спрашивает в любом случае.
                self.reply(200, jsonp_start(
                    {"code": 70016, "description": "вход не пройден",
                     "qs": "qs-стенд", "_sign": "sign-стенд",
                     "callback": Cab.base + "/sts"}))
        elif parsed.path in ("/sts", "/sts2"):
            prefix = "fresh-" if parsed.path == "/sts" else "fresh2-"
            token = "%s%d" % (prefix, len(Cab.tokens))
            Cab.tokens.add(token)
            followup = (query.get("followup") or [""])[0]
            self.reply(302, headers=[
                ("Location", followup or "/done"),
                ("Set-Cookie", "api-platform_serviceToken=%s; Path=/" % token)])
        elif parsed.path == "/done":
            self.reply(200, b"ok")
        elif parsed.path == "/pass/serviceLoginAuth2":
            user = (query.get("user") or [""])[0]
            got_hash = (query.get("hash") or [""])[0]
            if user == Cab.twofa_user:
                body = jsonp_start({"code": 0,
                                    "notificationUrl": Cab.base + "/confirm"})
            elif Cab.expected and (user, got_hash) == Cab.expected:
                body = jsonp_start({"code": 0, "location": Cab.base + "/sts2"})
            else:
                body = jsonp_start({"code": 70016, "description": "неверная пара"})
            self.reply(200, body)
        else:
            self.reply(404)


def setUpModule():
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    Cab.base = "http://127.0.0.1:%d" % server.server_address[1]
    MimoSnapTest.server = server


def tearDownModule():
    MimoSnapTest.server.shutdown()
    MimoSnapTest.server.server_close()


class MimoSnapTest(unittest.TestCase):
    server = None

    def setUp(self):
        Cab.reset()

    def make_home(self, with_passport=True):
        box = Path(tempfile.mkdtemp(prefix="devkit-mimo-"))
        self.addCleanup(shutil.rmtree, str(box), True)
        home = box / "home"
        (home / ".devkit").mkdir(parents=True)
        lines = ["stale = 10", "mimo-cabinet = %s/api/v1" % Cab.base]
        if with_passport:
            lines.append("mimo-passport = %s" % Cab.base)
        (home / ".devkit" / "quota.local").write_text(
            "\n".join(lines) + "\n", encoding="utf-8")
        stub = box / "bin"
        stub.mkdir()
        executable(stub / "secretctl", STUB_SECRETCTL)
        return home, stub

    def snap(self, home, stub, **env):
        base = {"DEVKIT_HARNESS": "mimo",
                "mimo-cabinet-account": None,
                "mimo-cabinet-password": None}
        base.update(env)
        return run([str(SNAP)], env=base,
                   path="%s:%s" % (stub, os.environ["PATH"]), home=home)

    def secret(self, home, name):
        return (home / ".devkit" / "stub-secrets" / name).read_text(
            encoding="utf-8")

    def test_live_cookie_renews_before_usage(self):
        """Живая кука. Плановое продление переиздаёт сессию до запроса
        использования, снимок собирается. Кабинетная кука ложится обратно,
        пасспортная остаётся исходной. Переизданный состав не принимается."""
        home, stub = self.make_home()
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=valid-stend",
                               "mimo-passport-cookie": "passToken=valid-passport"})
        self.assertEqual(rc, 0, out)
        self.assertIn("spent_cred = 40 кред", out, out)
        self.assertIn("/pass/serviceLogin", Cab.calls, "плановое продление не звалось")
        self.assertIn("/sts", Cab.calls, "цепочка не дошла до переиздания токена")
        cab = self.secret(home, "mimo-cabinet-cookie")
        self.assertTrue(cookie_token(cab).startswith("fresh-"),
                        "свежую куку кабинета не записали: %s" % cab)
        self.assertFalse(
            (home / ".devkit" / "stub-secrets" / "mimo-passport-cookie").exists(),
            "пасспортный секрет перезаписали ротированным составом")
        log = (home / ".devkit" / "stub-secrets" / "log").read_text(
            encoding="utf-8")
        self.assertNotIn("mimo-passport-cookie", log,
                         "задан был секрет mimo-passport-cookie: %s" % log)
        marks = (home / ".devkit" / "quota" / "mimo.renew.local").read_text(
            encoding="utf-8")
        self.assertIn("renewed=", marks, "метка продления не записалась: %s" % marks)

    def test_dead_cookie_renews_via_login_url(self):
        """Мёртвая кука при несвежей метке. Плановое продление добывает
        loginUrl у использования, цепочка выдаёт свежий токен, снимок
        собирается без входа по паролю."""
        home, stub = self.make_home()
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=dead-stend",
                               "mimo-passport-cookie": "passToken=valid-passport"})
        self.assertEqual(rc, 0, out)
        self.assertIn("spent_cred = 40 кред", out, out)
        self.assertIn("/sts", Cab.calls, "дорога продления не дошла до sts: %s" % Cab.calls)
        cab = self.secret(home, "mimo-cabinet-cookie")
        self.assertTrue(cookie_token(cab).startswith("fresh-"),
                        "свежую куку кабинета не записали: %s" % cab)

    def test_auth_renewal_when_marks_fresh(self):
        """Мёртвая кука при свежей метке. Плановое продление не запускается,
        но отказ использования поднимает ту же дорогу продления, и снимок
        собирается."""
        home, stub = self.make_home()
        now = datetime.datetime.now().replace(second=0, microsecond=0)
        marks = home / ".devkit" / "quota" / "mimo.renew.local"
        marks.parent.mkdir(parents=True, exist_ok=True)
        marks.write_text("attempted=%s\nrenewed=%s\n" % (
            now.strftime("%Y-%m-%dT%H:%M"), now.strftime("%Y-%m-%dT%H:%M")),
            encoding="utf-8")
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=dead-stend",
                               "mimo-passport-cookie": "passToken=valid-passport"})
        self.assertEqual(rc, 0, out)
        self.assertIn("spent_cred = 40 кред", out, out)
        self.assertIn("/sts", Cab.calls, "отказ использования не поднял дорогу: %s" % Cab.calls)
        self.assertNotIn("/pass/serviceLoginAuth2", Cab.calls,
                         "съём пошёл в парольный вход при живой пасспортной куке")

    def test_renew_mark_throttles_next_run(self):
        """Свежая метка оставляет второй запуск без продления. Съём каждые
        десять минут не должен долбить пасспорт после удачного обновления."""
        home, stub = self.make_home()
        env = {"mimo-cabinet-cookie": "api-platform_serviceToken=valid-stend",
               "mimo-passport-cookie": "passToken=valid-passport"}
        rc, out = self.snap(home, stub, **env)
        self.assertEqual(rc, 0, out)
        renewals = Cab.calls.count("/pass/serviceLogin")
        rc, out = self.snap(home, stub, **env)
        self.assertEqual(rc, 0, out)
        self.assertEqual(Cab.calls.count("/pass/serviceLogin"), renewals,
                         "второй запуск пошёл в пасспорт при свежей метке: %s" % Cab.calls)

    def test_dead_cookie_logs_in_by_password(self):
        """Мёртвая кука. Продление не проходит, съём входит по паролю из
        секрета, куки пишутся обратно, снимок собирается."""
        home, stub = self.make_home()
        account, password = "человек@стенд", "пароль-стенда"
        Cab.expected = (account,
                        hashlib.md5(password.encode("utf-8")).hexdigest().upper())
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=dead-stend",
                               "mimo-cabinet-account": account,
                               "mimo-cabinet-password": password})
        self.assertEqual(rc, 0, out)
        self.assertIn("spent_cred = 40 кред", out, out)
        self.assertIn("/pass/serviceLoginAuth2", Cab.calls, "вход не звался")
        self.assertTrue(self.secret(home, "mimo-cabinet-cookie").startswith(
            "api-platform_serviceToken=fresh2-"), "куки после входа не записались")

    def test_dead_cookie_without_secrets_refuses(self):
        """Мёртвая кука без секретов учётной записи это отказ с их именами,
        а не молчаливый пропуск съёма."""
        home, stub = self.make_home()
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=dead-stend"})
        self.assertNotEqual(rc, 0, "мёртвая кука прошла без секретов: %s" % out)
        self.assertIn("сессия кабинета истекла", out, out)
        self.assertIn("mimo-cabinet-account", out, out)
        self.assertNotIn("taken =", out, "отказ напечатал снимок")

    def test_two_factor_login_is_refused_with_reason(self):
        """Вход, требующий подтверждения, возвращает отказ с причиной.
        Съёмщик не выдумывает код и не трогает снимок."""
        home, stub = self.make_home()
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=dead-stend",
                               "mimo-cabinet-account": Cab.twofa_user,
                               "mimo-cabinet-password": "пароль-стенда"})
        self.assertNotEqual(rc, 0, "подтверждение прошло молча: %s" % out)
        self.assertIn("требует подтверждение", out, out)
        self.assertNotIn("taken =", out, "отказ напечатал снимок")

    def test_without_passport_key_old_behavior(self):
        """Без ключа mimo-passport и пасспортных кук продление и вход
        выключены. Мёртвая кука это отказ, как до механизма, и пасспорт не
        вызывается."""
        home, stub = self.make_home(with_passport=False)
        rc, out = self.snap(home, stub,
                            **{"mimo-cabinet-cookie": "api-platform_serviceToken=dead-stend"})
        self.assertNotEqual(rc, 0, "мёртвая кука прошла без ключа: %s" % out)
        self.assertIn("сессия кабинета истекла", out, out)
        self.assertNotIn("/pass/serviceLogin", Cab.calls,
                         "без ключа съёмщик полез в пасспорт: %s" % Cab.calls)


if __name__ == "__main__":
    unittest.main()
