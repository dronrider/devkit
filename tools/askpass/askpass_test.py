"""Помощник ввода пароля askpass (DK-772): разбор адреса демона, чтение
секрета и три исхода похода в демон, пароль, отмена и срок. Сервер тут не
настоящий дашборд, а синтетическая HTTP-заглушка стандартной библиотеки:
проверяется контракт (заголовок секрета, тело запроса, разбор ответа), а не
сам демон, его стенд свой (askpass_test.go).
"""
import contextlib
import http.server
import io
import json
import os
import sys
import tempfile
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import askpass  # noqa: E402


class FakeDaemon(http.server.BaseHTTPRequestHandler):
    # Заполняется каждым тестом перед стартом: код ответа, тело JSON и секрет,
    # который сервер сверяет с заголовком запроса (несовпадение это 401, как у
    # настоящего демона).
    status = 200
    body = {}
    # ASCII, как настоящий секрет демона (hex 32 байт, auth.go newSecret):
    # заголовок HTTP не носит кириллицу.
    want_secret = "cafef00d1234"
    seen = None

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        FakeDaemon.seen = {
            "path": self.path,
            "secret": self.headers.get("X-Devkit-Askpass-Secret"),
            "body": json.loads(raw.decode("utf-8")) if raw else {},
        }
        if FakeDaemon.seen["secret"] != FakeDaemon.want_secret:
            self.send_response(401)
            self.end_headers()
            self.wfile.write(json.dumps({"error": "секрет неверный"}).encode("utf-8"))
            return
        self.send_response(FakeDaemon.status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(FakeDaemon.body).encode("utf-8"))

    def log_message(self, fmt, *args):
        pass


@contextlib.contextmanager
def fake_daemon(status, body, local_key="cafef00d1234"):
    FakeDaemon.status, FakeDaemon.body, FakeDaemon.want_secret, FakeDaemon.seen = status, body, local_key, None
    srv = http.server.HTTPServer(("127.0.0.1", 0), FakeDaemon)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        yield srv.server_address[1]
    finally:
        srv.shutdown()
        t.join(timeout=5)
        srv.server_close()


@contextlib.contextmanager
def secret_file(text):
    with tempfile.TemporaryDirectory() as d:
        path = os.path.join(d, "askpass.local")
        if text is not None:
            with open(path, "w", encoding="utf-8") as f:
                f.write(text)
        was = askpass.SECRET_PATH
        askpass.SECRET_PATH = path
        try:
            yield path
        finally:
            askpass.SECRET_PATH = was


class ExecutableTest(unittest.TestCase):
    def test_helper_is_executable_in_the_checkout(self):
        # sudo и ssh зовут SUDO_ASKPASS/SSH_ASKPASS напрямую, без интерпретатора
        # перед ним: без бита запуска раскладка devkitctl doctor --fix копирует
        # мёртвый файл, а sudo из чата отказывает execve, а не разбором пароля.
        path = Path(askpass.__file__)
        self.assertTrue(os.access(str(path), os.X_OK), "%s без бита запуска" % path)


class AddrOfTest(unittest.TestCase):
    def test_empty_host_becomes_loopback(self):
        # Демон слушает все интерфейсы (addr пуст в конфиге), и DEVKIT_ADDR
        # приходит вида ":7112": для похода изнутри той же машины пустой хост
        # подменяется петлёй, иначе "http://:7112" никуда не достучится.
        self.assertEqual(askpass.addr_of(":7112"), "127.0.0.1:7112")

    def test_explicit_host_is_kept(self):
        self.assertEqual(askpass.addr_of("127.0.0.1:7112"), "127.0.0.1:7112")


class ReadSecretTest(unittest.TestCase):
    def test_missing_file_exits_with_reason(self):
        with secret_file(None):
            with self.assertRaises(SystemExit) as cm:
                askpass.read_secret()
            self.assertEqual(cm.exception.code, 1)

    def test_empty_file_exits_with_reason(self):
        with secret_file(""):
            with self.assertRaises(SystemExit):
                askpass.read_secret()

    def test_trims_trailing_newline(self):
        with secret_file("сам-секрет\n"):
            self.assertEqual(askpass.read_secret(), "сам-секрет")


class MainTest(unittest.TestCase):
    def run_main(self, argv, env):
        out, err = io.StringIO(), io.StringIO()
        old_out, old_err = sys.stdout, sys.stderr
        sys.stdout, sys.stderr = out, err
        try:
            askpass.main(argv, env)
            code = 0
        except SystemExit as exc:
            code = exc.code
        finally:
            sys.stdout, sys.stderr = old_out, old_err
        return code, out.getvalue(), err.getvalue()

    def test_no_prompt_argument_fails(self):
        code, out, err = self.run_main(["askpass.py"], {})
        self.assertNotEqual(code, 0)
        self.assertIn("текст запроса", err)
        self.assertEqual(out, "")

    def test_no_devkit_addr_fails(self):
        code, out, err = self.run_main(["askpass.py", "[sudo] пароль:"], {})
        self.assertNotEqual(code, 0)
        self.assertIn("DEVKIT_ADDR", err)

    def test_happy_path_prints_password_and_nothing_else(self):
        with fake_daemon(200, {"password": "стук-стук"}) as port, secret_file("cafef00d1234\n"):
            env = {"DEVKIT_ADDR": ":%d" % port, "DEVKIT_CHAT": "chat-1", "DEVKIT_TMUX": "chat-XR-4-1"}
            code, out, err = self.run_main(["askpass.py", "[sudo] Password:"], env)
        self.assertEqual(code, 0, err)
        self.assertEqual(out, "стук-стук\n")
        self.assertEqual(err, "")
        self.assertEqual(FakeDaemon.seen["body"],
                         {"chat": "chat-1", "tmux": "chat-XR-4-1", "prompt": "[sudo] Password:"})

    def test_wrong_secret_fails_without_password_in_output(self):
        with fake_daemon(200, {"password": "не должно уйти"}) as port, secret_file("badc0de5678\n"):
            env = {"DEVKIT_ADDR": ":%d" % port}
            code, out, err = self.run_main(["askpass.py", "[sudo] Password:"], env)
        self.assertNotEqual(code, 0)
        self.assertEqual(out, "", "пароль не должен уйти в stdout при чужом секрете")
        self.assertIn("демон отказал", err)

    def test_cancel_fails_loudly(self):
        with fake_daemon(410, {"error": "отменено человеком"}) as port, secret_file("cafef00d1234\n"):
            env = {"DEVKIT_ADDR": ":%d" % port}
            code, out, err = self.run_main(["askpass.py", "[sudo] Password:"], env)
        self.assertNotEqual(code, 0)
        self.assertEqual(out, "")
        self.assertIn("отменено", err)

    def test_timeout_fails_loudly(self):
        with fake_daemon(504, {"error": "срок ожидания пароля вышел"}) as port, secret_file("cafef00d1234\n"):
            env = {"DEVKIT_ADDR": ":%d" % port}
            code, out, err = self.run_main(["askpass.py", "[sudo] Password:"], env)
        self.assertNotEqual(code, 0)
        self.assertEqual(out, "")
        self.assertIn("срок ожидания", err)


if __name__ == "__main__":
    unittest.main()
