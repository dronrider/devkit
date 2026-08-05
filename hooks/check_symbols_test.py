#!/usr/bin/env python3
"""Проверка запрещённых символов: разбор файла и stdin, режим --hook, пропуск
снимков в testdata, разбор в тексте находки. Отсюда же проверяется первый рубеж
pre-commit: добавленные строки коммита проходят через эту же проверку.

Сами запрещённые символы пишутся escape-последовательностями, чтобы файл
оставался чистым и не спотыкался о собственную проверку.
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-symbols.py")

DASH = "\u2014"    # длинное тире
ARROW = "\u2192"   # стрелка
QUOTE = "\u201c"   # кавычка-лапка
ACUTE = "\u00e9"  # буква с диакритикой, отдельного разбора у неё нет


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def hook_event(path, text):
    return json.dumps({"tool_input": {"file_path": path, "new_string": text}})


class TestFileAndStdin(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_clean_file_passes(self):
        path = self.write("clean.txt",
                          "обычный текст, «ёлочки», № 5, ASCII -> стрелка\n")
        self.assertEqual(run(path).returncode, 0)

    def test_dash_in_file_is_caught_with_place(self):
        path = self.write("dirty.txt", "текст с тире %s вот\n" % DASH)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertTrue(r.stdout.startswith(path + ":1:"), r.stdout)

    def test_dash_in_stdin_is_caught(self):
        r = run("--stdin", input="строка с тире %s\n" % DASH)
        self.assertEqual(r.returncode, 1)

    def test_testdata_snapshot_is_skipped(self):
        path = self.write("testdata/file.txt", "текст с тире %s в снимке\n" % DASH)
        self.assertEqual(run(path).returncode, 0)

    def test_similar_name_is_not_testdata(self):
        # Пропуск идёт по имени директории целиком: mytestdata это чужое место.
        path = self.write("mytestdata/file.txt", "текст с тире %s в снимке\n" % DASH)
        self.assertEqual(run(path).returncode, 1)


class TestHookMode(unittest.TestCase):
    def test_dash_in_new_string_is_refused(self):
        r = run("--hook", input=hook_event("x.md", "плохо %s" % DASH))
        self.assertEqual(r.returncode, 2)

    def test_clean_new_string_passes(self):
        r = run("--hook", input=hook_event("x.md", "чисто, «ёлочки», № 5"))
        self.assertEqual(r.returncode, 0)

    def test_bare_hook_is_claude_code(self):
        # Команды в settings.json на машинах прописаны без имени протокола, и
        # обновление devkit их ломать не должно.
        dirty = hook_event("x.md", "плохо %s" % DASH)
        self.assertEqual(run("--hook", input=dirty).returncode,
                         run("--hook", "claude-code", input=dirty).returncode)
        self.assertEqual(run("--hook", "claude-code", input=dirty).returncode, 2)
        self.assertEqual(run("--hook", "claude-code",
                             input=hook_event("x.md", "чисто")).returncode, 0)

    def test_testdata_snapshot_is_skipped(self):
        r = run("--hook", input=hook_event("mylib/testdata/snapshot.txt",
                                           "тире %s в хуке" % DASH))
        self.assertEqual(r.returncode, 0)

    def test_similar_name_is_not_testdata(self):
        r = run("--hook", input=hook_event("mylib/mytestdata/snapshot.txt",
                                           "тире %s в хуке" % DASH))
        self.assertEqual(r.returncode, 2)


class TestAdvice(unittest.TestCase):
    """Находка не только запрещает символ, но и говорит, чем его переписать."""

    def test_advice_in_cli(self):
        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "advice.txt")
        with open(path, "w", encoding="utf-8") as f:
            f.write("тире %s и стрелка %s\n" % (DASH, ARROW))
        out = run(path).stdout
        self.assertIn("как переписать", out)
        self.assertIn("перестроить предложение", out)
        self.assertIn("ASCII", out)

    def test_advice_for_quotes(self):
        out = run("--stdin", input="кавычки %s\n" % QUOTE).stdout
        self.assertIn("ёлочки", out)

    def test_advice_in_hook_mode(self):
        r = run("--hook", input=hook_event("x.md", "тире %s" % DASH))
        self.assertIn("перестроить предложение", r.stderr)

    def test_advice_for_the_rest(self):
        # Символ, для которого отдельного разбора нет, разбор всё равно получает.
        out = run("--stdin", input="буква %s\n" % ACUTE).stdout
        self.assertIn("клавиатурный аналог", out)


class TestPreCommit(unittest.TestCase):
    """Первый рубеж коммита: тире в добавленных строках ловится, а уже
    закоммиченная чужая строка рубеж не поднимает."""

    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)

    def stage(self, text):
        with open(os.path.join(self.repo, "f.txt"), "w", encoding="utf-8") as f:
            f.write(text)
        subprocess.run(["git", "-C", self.repo, "add", "f.txt"], check=True)

    def hook(self):
        return subprocess.run([os.path.join(HERE, "pre-commit")], cwd=self.repo,
                              capture_output=True, text=True).returncode

    def test_dash_in_added_lines(self):
        self.stage("первая строка\nстарое тире %s тут\nтретья строка\n" % DASH)
        self.assertEqual(self.hook(), 1)

    def test_committed_line_is_left_alone(self):
        self.stage("первая строка\nстарое тире %s тут\nтретья строка\n" % DASH)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "seed"], check=True)
        self.stage("первая строка правлена\nстарое тире %s тут\nтретья строка\n" % DASH)
        self.assertEqual(self.hook(), 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
