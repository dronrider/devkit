#!/usr/bin/env python3
"""Проверка чувствительного: IP, токены и корп-домен ловятся в любом
коммитимом файле, роли машин и loopback проходят, служебные каталоги
пропускаются, а local-docs в коммите это находка сама по себе. Корп-домен
в тестовый файл не въезжает: значение генерируется рандомом и подаётся в
хук через env подпроцесса или через RULES.local.md во временной директории.
Отсюда же проверяется второй рубеж pre-commit: он гоняет эту проверку по
добавленным строкам.
"""
import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-sensitive.py")
TOKEN = "ghp_" + "a" * 36
DOMAIN_ENV = "DEVKIT_CORP_DOMAIN"


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def hook_event(path, text):
    return json.dumps({"tool_input": {"file_path": path, "new_string": text}})


class TestFiles(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_role_and_loopback_pass(self):
        path = self.write("sens_ok.md", "хост роутер DE, локально 127.0.0.1, "
                                        "маска 255.255.255.0\n")
        self.assertEqual(run(path).returncode, 0)

    def test_ip_and_secret_are_caught(self):
        path = self.write("sens_bad.md", "сервер живёт на 10.1.2.3, "
                                         "пароль: hunter2secret\n")
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("IP-адрес", r.stdout)
        self.assertIn("секрет", r.stdout)


class TestDiffMode(unittest.TestCase):
    def test_ip_in_board_diff(self):
        r = run("--diff", input="docs/TASKS.md:3:| XR-1 | сервер 10.1.2.3 | task |\n")
        self.assertEqual(r.returncode, 1)

    def test_code_files_are_scanned(self):
        # скоуп шире доски: IP в исходнике ловится тем же рубежом
        r = run("--diff", input='src/config.go:1:addr := "10.1.2.3"\n')
        self.assertEqual(r.returncode, 1)
        self.assertIn("IP-адрес", r.stdout)

    def test_skip_dirs_are_left_alone(self):
        r = run("--diff", input='vendor/lib.go:1:addr := "10.1.2.3"\n')
        self.assertEqual(r.returncode, 0)
        r = run("--diff", input='node_modules/pkg/index.js:1:host = "10.1.2.3"\n')
        self.assertEqual(r.returncode, 0)

    def test_skip_files_are_left_alone(self):
        # тестовые фикстуры с адресами и токенами это данные, не утечка
        r = run("--diff", input='pkg/net_test.go:1:host := "10.1.2.3"\n')
        self.assertEqual(r.returncode, 0)
        r = run("--diff", input='tests/test_client.py:1:TOKEN = "ghp_x"\n')
        self.assertEqual(r.returncode, 0)

    def test_local_docs_never_rides_into_commit(self):
        r = run("--diff", input="local-docs/hosts.md:1:строка\n")
        self.assertEqual(r.returncode, 1)
        self.assertIn("gitignore", r.stdout)


class TestHookMode(unittest.TestCase):
    def test_token_in_task_file_is_refused(self):
        r = run("--hook", input=hook_event("/a/docs/tasks/XR-1.md",
                                           "токен %s" % TOKEN))
        self.assertEqual(r.returncode, 2)

    def test_outside_board_is_scanned(self):
        # скоуп шире доски: IP в исходнике ловится и на записи файла
        r = run("--hook", input=hook_event("/a/src/main.go", "10.1.2.3"))
        self.assertEqual(r.returncode, 2)

    def test_skip_dirs_are_left_alone(self):
        r = run("--hook", input=hook_event("/a/vendor/lib.go", "10.1.2.3"))
        self.assertEqual(r.returncode, 0)
        r = run("--hook", input=hook_event("/a/node_modules/pkg/index.js",
                                           "10.1.2.3"))
        self.assertEqual(r.returncode, 0)

    def test_skip_files_are_left_alone(self):
        r = run("--hook", input=hook_event("/a/pkg/net_test.go", "10.1.2.3"))
        self.assertEqual(r.returncode, 0)
        r = run("--hook", input=hook_event("/a/tests/test_client.py",
                                           "ghp_" + "a" * 36))
        self.assertEqual(r.returncode, 0)

    def test_machine_role_passes(self):
        r = run("--hook", input=hook_event("/a/docs/tasks/XR-1.md",
                                           "проверить на роутере DE"))
        self.assertEqual(r.returncode, 0)


class TestCorpDomain(unittest.TestCase):
    """Паттерн корп-домена держит значение вне кода и тестов: домен
    генерируется рандомом в setUp и подаётся в хук через env подпроцесса или
    через RULES.local.md во временной директории. В исходнике теста значения
    нет, греп по нему молчит."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.domain = "dc-%x.test" % random.randrange(16 ** 8)

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def env(self, **extra):
        env = dict(os.environ)
        env.pop(DOMAIN_ENV, None)
        env.update(extra)
        return env

    def test_caught_when_env_set(self):
        path = self.write("host.md", "ход на https://%s/api\n" % self.domain)
        r = run(path, env=self.env(**{DOMAIN_ENV: self.domain}), cwd=self.tmp)
        self.assertEqual(r.returncode, 1)
        self.assertIn("корп-домен", r.stdout)

    def test_caught_when_local_rule_set(self):
        with open(os.path.join(self.tmp, "RULES.local.md"), "w",
                  encoding="utf-8") as f:
            f.write("# локальные дополнения\ncorp_domain = %s\n" % self.domain)
        path = self.write("host.md", "ход на https://%s/api\n" % self.domain)
        r = run(path, env=self.env(), cwd=self.tmp)
        self.assertEqual(r.returncode, 1)
        self.assertIn("корп-домен", r.stdout)

    def test_silent_when_no_source(self):
        # ни env, ни RULES.local.md: домен неизвестен, паттерн не активен
        path = self.write("host.md", "ход на https://%s/api\n" % self.domain)
        r = run(path, env=self.env(), cwd=self.tmp)
        self.assertEqual(r.returncode, 0)

    def test_does_not_match_inside_longer_host(self):
        # границы домена режут my-<домен>.host: это уже другой хост
        path = self.write("host.md", "my-%s.host/api\n" % self.domain)
        r = run(path, env=self.env(**{DOMAIN_ENV: self.domain}), cwd=self.tmp)
        self.assertEqual(r.returncode, 0)

    def test_hook_refuses_domain(self):
        env = self.env(**{DOMAIN_ENV: self.domain})
        r = run("--hook",
                input=hook_event("/a/src/main.go", "url https://%s/api" % self.domain),
                env=env)
        self.assertEqual(r.returncode, 2)

    def test_diff_refuses_domain(self):
        env = self.env(**{DOMAIN_ENV: self.domain})
        r = run("--diff",
                input="src/main.go:5:url https://%s/api\n" % self.domain,
                env=env)
        self.assertEqual(r.returncode, 1)
        self.assertIn("корп-домен", r.stdout)


class TestLocalDocsOnWrite(unittest.TestCase):
    """На записи находка ровно тогда, когда local-docs не заигнорен."""

    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        os.makedirs(os.path.join(self.repo, "local-docs"))
        self.event = hook_event("local-docs/hosts.md", "роутер")

    def hook(self):
        return run("--hook", input=self.event, cwd=self.repo).returncode

    def test_not_ignored_is_refused(self):
        self.assertEqual(self.hook(), 2)

    def test_ignored_passes(self):
        with open(os.path.join(self.repo, ".gitignore"), "w", encoding="utf-8") as f:
            f.write("local-docs/\n")
        self.assertEqual(self.hook(), 0)


class TestPreCommit(unittest.TestCase):
    """Второй рубеж коммита: IP в staged-строках доски."""

    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)
        os.makedirs(os.path.join(self.repo, "docs"))

    def stage(self, text):
        with open(os.path.join(self.repo, "docs", "TASKS.md"), "w",
                  encoding="utf-8") as f:
            f.write(text)
        subprocess.run(["git", "-C", self.repo, "add", "docs/TASKS.md"], check=True)

    def hook(self):
        return subprocess.run([os.path.join(HERE, "pre-commit")], cwd=self.repo,
                              capture_output=True, text=True).returncode

    def test_ip_in_board_is_caught(self):
        self.stage("| XR-1 | сервер 10.1.2.3 | task |\n")
        self.assertEqual(self.hook(), 1)

    def test_clean_board_passes(self):
        self.stage("| XR-1 | сервер уехал на роль VPS RU | task |\n")
        self.assertEqual(self.hook(), 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
