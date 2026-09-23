#!/usr/bin/env python3
"""Стенд для DK-1124-check.sh: сценарий проверки живёт рядом с постановкой, а
не в tools/, и живого дашборда с живым дереватором нагрузки здесь нет.

Настоящий прогон занимает 20-25 минут и просит уже запущенный дашборд
(разбор в docs/tasks/DK-1124.md, раздел «Ход работы»), поэтому тест глушит
обе внешние зависимости точками подмены самого скрипта: DEVKIT_HOME уводит
конфиг и журнал в свой каталог, DEVKIT_LOAD_CMD заменяет второй полный
прогон `parallel.py` на мгновенную команду, DEVKIT_PROBE_INTERVAL убирает
паузы между замерами. `taskctl` подменён стендовым скриптом в голове PATH.
HTTP-часть сценария (login и /api/projects) идёт через python3
urllib.request и http.cookiejar, не через curl (замечание ревью DK-1124:
curl отбит разрешениями Bash харнесса агента), поэтому вместо стенда-бинаря
поднят настоящий loopback-сервер стандартной библиотекой http.server: логика
самого сценария (разбор конфига, порог, окно журнала, код возврата, cookie
сессии между login и замером) проверяется по-настоящему, а не изображается.
"""
import http.server
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "DK-1124-check.sh"

STAND_TOKEN = "stand-token"
STAND_COOKIE = "session=stand-session"


def _make_handler(state):
    class Handler(http.server.BaseHTTPRequestHandler):

        def log_message(self, *a):
            pass

        def do_POST(self):
            if self.path != "/api/login":
                self.send_response(404)
                self.end_headers()
                return
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length) if length else b"{}"
            data = json.loads(body)
            if data.get("token") == STAND_TOKEN:
                self.send_response(200)
                self.send_header("Set-Cookie", STAND_COOKIE + "; Path=/")
                self.end_headers()
            else:
                self.send_response(401)
                self.end_headers()

        def do_GET(self):
            if self.path != "/api/projects":
                self.send_response(404)
                self.end_headers()
                return
            state["total"] += 1
            if STAND_COOKIE in self.headers.get("Cookie", ""):
                state["authed"] += 1
                self.send_response(200)
            else:
                self.send_response(401)
            self.end_headers()

    return Handler


class Stand(unittest.TestCase):

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="dk-1124-check-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        self.state = {"total": 0, "authed": 0}
        self.server = http.server.HTTPServer(("127.0.0.1", 0), _make_handler(self.state))
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        (self.home / ".devkit" / "dashboard.local").write_text(
            "port = %d\ntoken = %s\n" % (self.server.server_port, STAND_TOKEN),
            encoding="utf-8")
        self.bin = self.dir / "bin"
        self.bin.mkdir()

    def stub(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\n" + body, encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

    def run_script(self, limit=5, load="true", probe_interval="0", extra_env=None):
        env = dict(os.environ)
        env["PATH"] = str(self.bin) + os.pathsep + env.get("PATH", "")
        env["DEVKIT_HOME"] = str(self.home)
        env["DEVKIT_LOAD_CMD"] = load
        env["DEVKIT_PROBE_INTERVAL"] = probe_interval
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            ["sh", str(SCRIPT), str(limit)],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, timeout=60)


class SyntaxTest(unittest.TestCase):

    def test_shell_syntax_is_valid(self):
        # Регрессия на опечатку: `sh -n` не гоняет тело, только разбирает
        # его, поэтому тест быстрый и не просит стенда.
        proc = subprocess.run(["sh", "-n", str(SCRIPT)],
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True)
        self.assertEqual(proc.returncode, 0, proc.stdout)

    def test_no_external_bc_dependency(self):
        # Замечание ревью DK-1124: bc нигде больше в tools/ и hooks/ не
        # встречается и не гарантирован на всякой машине, а сравнение и
        # разность времени можно свести к awk, который сценарий и так
        # зовёт для разбора конфига. Регрессия на возврат bc текстовая:
        # запуск с PATH без bc уронил бы сам прогон, а не только этот тест.
        text = SCRIPT.read_text(encoding="utf-8")
        self.assertNotRegex(text, r'\bbc\b',
                            "bc обязан быть убран, сравнение и разность времени на awk")

    def test_no_curl_dependency(self):
        # Замечание ревью DK-1124: curl отбит разрешениями Bash харнесса
        # агента, включая curl --version без байта по сети (разбор в
        # docs/tasks/DK-1124.md, «Ход работы»). HTTP-часть переведена на
        # python3 urllib.request и http.cookiejar стандартной библиотеки.
        # Комментарии, где curl назван по имени ради этого разбора, тест не
        # считает: смотрит только исполняемые строки.
        code_lines = [ln for ln in SCRIPT.read_text(encoding="utf-8").splitlines()
                      if not ln.strip().startswith("#")]
        self.assertNotRegex("\n".join(code_lines), r'\bcurl\b',
                            "curl обязан быть убран, HTTP-запросы идут через python3 urllib.request")


class ThresholdTest(Stand):

    def test_reports_ok_under_the_limit(self):
        # Быстрый taskctl и loopback-сервер, порог с запасом: все десять
        # замеров укладываются, сценарий обязан отдать 0 и напечатать OK.
        self.stub("taskctl", "exit 0")
        proc = self.run_script(limit=5)
        self.assertEqual(proc.returncode, 0, proc.stdout)
        self.assertIn("OK", proc.stdout)
        self.assertIn("замер 10:", proc.stdout, "сценарий обязан взять все десять замеров")

    def test_reports_failed_over_the_limit(self):
        # taskctl нарочно медленный при пороге 0: каждый из десяти замеров
        # обязан уйти за порог, а сценарий обязан провалиться кодом 1 и
        # назвать число замеров сверх порога.
        self.stub("taskctl", "sleep 0.2")
        proc = self.run_script(limit=0)
        self.assertEqual(proc.returncode, 1, proc.stdout)
        self.assertIn("FAILED", proc.stdout)
        self.assertIn("10 замер", proc.stdout, proc.stdout)

    def test_flags_the_30s_ceiling_from_the_log(self):
        # Строка потолка в журнале дашборда после старта сценария обязана
        # попасть в отчёт: окно берётся по времени старта, не по факту файла.
        self.stub("taskctl", "exit 0")
        log = self.home / ".devkit" / "dashboard.log"
        log.write_text("", encoding="utf-8")

        def load_and_write_log():
            log.write_text(
                "9999-01-01T00:00:00 taskctl list --json не ответил за 30s"
                " и снят по сроку\n",
                encoding="utf-8")
            return True

        # Строка журнала пишется до старта сценария нарочно: дата
        # 9999-01-01 гарантированно позже метки старта из самого сценария
        # (date -u сегодняшним годом), так что окно `awk -v s="$start"` её
        # не отбросит ни на одной машине.
        load_and_write_log()
        proc = self.run_script(limit=5)
        self.assertEqual(proc.returncode, 0, proc.stdout)
        self.assertIn("потолок задет", proc.stdout)
        self.assertIn("не ответил за 30s", proc.stdout)

    def test_clean_log_reports_the_ceiling_is_not_touched(self):
        self.stub("taskctl", "exit 0")
        proc = self.run_script(limit=5)
        self.assertIn("потолок не задет", proc.stdout)

    def test_measurement_reuses_the_login_cookie(self):
        # curl -c/-b передавали cookie между login и замером через файл
        # cookies; http.cookiejar обязан делать то же самое через
        # MozillaCookieJar на том же файле, иначе все десять запросов
        # /api/projects идут неавторизованными.
        self.stub("taskctl", "exit 0")
        proc = self.run_script(limit=5)
        self.assertEqual(proc.returncode, 0, proc.stdout)
        self.assertEqual(self.state["total"], 10)
        self.assertEqual(self.state["authed"], 10,
                          "все десять замеров /api/projects обязаны идти с cookie сессии")

    def test_default_load_command_is_the_real_full_run(self):
        # DEVKIT_LOAD_CMD это точка подмены для теста, а не смена
        # поведения: без неё сценарий обязан гнать настоящий второй прогон
        # parallel.py, как того просит DoD DK-1124.
        text = SCRIPT.read_text(encoding="utf-8")
        self.assertIn("python3 tools/devkitctl/parallel.py", text)


if __name__ == "__main__":
    unittest.main()
