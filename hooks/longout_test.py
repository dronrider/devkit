#!/usr/bin/env python3
"""Тесты рубежа длинного вывода (DK-499): длинный вывод Bash подменяется
выжимкой с путём к полному файлу, короткий доходит до модели нетронутым, и
срабатывание отличимо от бездействия по следу (JSON подмены в stdout хука)."""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "longout.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "tool-done-bash.json")
CMDOUT_SRC = os.path.join(os.path.dirname(HERE), "tools", "cmdout")


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


class Stand(unittest.TestCase):
    """Общий стенд: собранный cmdout в PATH и git-каталог события. Файл
    полного вывода пишется только в git-дерево, и без репозитория даже
    длинный вывод подменять нечем."""

    @classmethod
    def setUpClass(cls):
        cls.bindir = tempfile.mkdtemp(prefix="longout-bin-")
        r = subprocess.run(
            ["go", "build", "-o", os.path.join(cls.bindir, "cmdout"), "."],
            cwd=CMDOUT_SRC,
            env=dict(os.environ, GOWORK="off"),
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            raise unittest.SkipTest("cmdout не собрался: %s" % r.stderr)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.bindir, ignore_errors=True)

    def setUp(self):
        # realpath: macOS прячет временные каталоги за симлинком /var, а хук
        # печатает путь из git root разрешённым.
        self.repo = os.path.realpath(tempfile.mkdtemp(prefix="longout-repo-"))
        for args in (("init", "-q", "-b", "main"),
                     ("config", "user.email", "t@t"),
                     ("config", "user.name", "t")):
            subprocess.run(["git", "-C", self.repo] + list(args),
                           capture_output=True, check=True)
        with open(os.path.join(self.repo, "README"), "w", encoding="utf-8") as f:
            f.write("seed\n")
        subprocess.run(["git", "-C", self.repo, "add", "."],
                       capture_output=True, check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "seed"],
                       capture_output=True, check=True)

    def tearDown(self):
        shutil.rmtree(self.repo, ignore_errors=True)

    def event(self, stdout, stderr="", command="go test ./..."):
        with open(SAMPLE, encoding="utf-8") as f:
            data = json.load(f)
        data["cwd"] = self.repo
        data["tool_input"]["command"] = command
        data["tool_response"]["stdout"] = stdout
        data["tool_response"]["stderr"] = stderr
        return json.dumps(data)

    def hook(self, event, env=None):
        environ = dict(os.environ)
        environ["PATH"] = self.bindir + os.pathsep + environ.get("PATH", "")
        if env is not None:
            environ.update(env)
        return run("--hook", "claude-code", input=event, env=environ,
                   cwd=self.repo)

    def cmdout_dirs(self):
        base = os.path.join(self.repo, ".devkit", "cmdout")
        return os.listdir(base) if os.path.isdir(base) else []

    @staticmethod
    def long_output(lines=150):
        return "".join("строка вывода номер %d\n" % i for i in range(lines))


class TestShortOutputPasses(Stand):
    """Короткий вывод проходит нетронутым: подмены нет, и файл за ней не
    заводится (пустые каталоги в .devkit/cmdout это мусор для чистильщика)."""

    def test_ping_is_untouched(self):
        r = self.hook(self.event("ping\n"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_short_output_leaves_no_file(self):
        r = self.hook(self.event(self.long_output(30)))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")
        self.assertEqual(self.cmdout_dirs(), [])

    def test_empty_output_passes(self):
        r = self.hook(self.event(""))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")


class TestLongOutputSubstituted(Stand):
    """Длинный вывод подменяется выжимкой, полный текст ложится файлом."""

    def test_substitution_is_json_on_stdout(self):
        # След срабатывания: удачный ответ хука пишется в транскрипт записью
        # hook_success со stdout хука, и бездействие от срабатывания отличается
        # пустым следом, тем же порядком, что у check-longfile.
        r = self.hook(self.event(self.long_output()))
        self.assertEqual(r.returncode, 0, r.stderr)
        record = json.loads(r.stdout)
        sub = record["hookSpecificOutput"]
        self.assertEqual(sub["hookEventName"], "PostToolUse")
        out = sub["updatedToolOutput"]
        self.assertIn("lines_total:", out["stdout"])
        self.assertIn("lines_hidden:", out["stdout"])
        self.assertIn("path:", out["stdout"])

    def test_full_text_lies_on_disk(self):
        text = self.long_output()
        r = self.hook(self.event(text))
        out = json.loads(r.stdout)["hookSpecificOutput"]["updatedToolOutput"]
        path = [ln for ln in out["stdout"].splitlines()
                if ln.startswith("path: ")][0][len("path: "):]
        self.assertTrue(path.startswith(self.repo), path)
        with open(path, encoding="utf-8") as f:
            self.assertEqual(f.read(), text)

    def test_stderr_is_merged_into_the_digest(self):
        # Длинный stderr тоже уходит в выжимку и в файл, а в подменённом
        # ответе поле stderr пустое: иначе его текст остался бы в контексте
        # вторым экземпляром.
        r = self.hook(self.event("спокойный stdout\n",
                                 stderr=self.long_output(150)))
        out = json.loads(r.stdout)["hookSpecificOutput"]["updatedToolOutput"]
        self.assertEqual(out["stderr"], "")
        path = [ln for ln in out["stdout"].splitlines()
                if ln.startswith("path: ")][0][len("path: "):]
        with open(path, encoding="utf-8") as f:
            self.assertIn("строка вывода номер 149", f.read())

    def test_slug_comes_from_the_first_word(self):
        r = self.hook(self.event(self.long_output(), command="git status"))
        out = json.loads(r.stdout)["hookSpecificOutput"]["updatedToolOutput"]
        path = [ln for ln in out["stdout"].splitlines()
                if ln.startswith("path: ")][0][len("path: "):]
        self.assertIn("-git/out", path)

    def test_substitution_has_only_documented_fields(self):
        # Справка харнеса показывает объект из четырёх полей; лишние копия
        # tool_response (noOutputExpected и прочие) не несёт, схема клиента
        # несовпадение полей отбивает молча.
        r = self.hook(self.event(self.long_output()))
        out = json.loads(r.stdout)["hookSpecificOutput"]["updatedToolOutput"]
        self.assertEqual(set(out), {"stdout", "stderr", "interrupted", "isImage"})
        self.assertFalse(out["interrupted"])
        self.assertFalse(out["isImage"])


class TestSilence(Stand):
    """Молчание там, где судить нечем: вывод доходит как есть."""

    def test_no_cmdout_in_path_passes_output(self):
        empty = tempfile.mkdtemp(prefix="longout-empty-")
        self.addCleanup(shutil.rmtree, empty, True)
        env = {"PATH": empty}
        r = self.hook(self.event(self.long_output()), env=env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_outside_git_leaves_output(self):
        # Вне git-дерева полного файла не будет, и подмена потеряла бы текст.
        alien = os.path.realpath(tempfile.mkdtemp(prefix="longout-alien-"))
        self.addCleanup(shutil.rmtree, alien, True)
        data = json.loads(self.event(self.long_output()))
        data["cwd"] = alien
        r = self.hook(json.dumps(data))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_foreign_event_is_silent(self):
        data = json.loads(self.event(self.long_output()))
        data["hook_event_name"] = "PreToolUse"
        r = self.hook(json.dumps(data))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_other_tool_is_silent(self):
        data = json.loads(self.event(self.long_output()))
        data["tool_name"] = "Edit"
        r = self.hook(json.dumps(data))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_broken_event_is_silent(self):
        r = self.hook("это не json")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")


class TestModes(unittest.TestCase):
    def test_unknown_protocol_is_named(self):
        r = run("--hook", "кой-протокол", input="{}")
        self.assertEqual(r.returncode, 2)
        self.assertIn("кой-протокол", r.stderr)

    def test_bare_invocation_prints_doc(self):
        r = run()
        self.assertEqual(r.returncode, 2)
        self.assertIn("longout", r.stderr)


if __name__ == "__main__":
    unittest.main()
