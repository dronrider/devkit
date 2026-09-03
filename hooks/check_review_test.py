#!/usr/bin/env python3
"""Рубеж ревью: пуш и создание MR без следа ревью отбиваются, всё прочее
проходит. Приговор выносит shipctl, поэтому в PATH прогона кладётся не
настоящая утилита, а скрипт с заданным исходом: предмет тут не разбор
диапазона (он проверяется в tools/shipctl), а то, какие команды рубеж отдаёт
на суд и как молчит там, где судить нечем."""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
TOOL = os.path.join(HERE, "check-review.py")

_spec = importlib.util.spec_from_file_location("check_review", TOOL)
hook = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(hook)

REFUSAL = "нет следа ревью у кода в диапазоне"
# Системные каталоги для PATH прогона: git рубеж зовёт сам, а вот shipctl тут
# кладётся стендом, и настоящий из ~/go/bin в этот PATH не попадает.
SYSTEM_PATH = "/usr/bin:/bin"


def event(command, cwd):
    return json.dumps({"tool_name": "Bash", "tool_input": {"command": command}, "cwd": cwd})


class TestJudged(unittest.TestCase):
    """Что рубеж берётся судить. Ожидание выписано руками по формам команд,
    которыми код уходит наружу."""

    def test_push_forms(self):
        for command in ("git push", "git push --force origin main",
                        "git -C /tmp/x push", "cd /tmp && git push"):
            self.assertEqual(hook.judged(command), "git push", command)

    def test_requests(self):
        self.assertEqual(hook.judged("glab mr create --title x"), "glab mr create")
        self.assertEqual(hook.judged("gh pr create"), "gh pr create")
        self.assertEqual(hook.judged("tea pr create --base main"), "tea pr create")

    def test_permission_lifts(self):
        self.assertEqual(hook.judged("DEVKIT_PUSH_OK=1 git push"), "")

    def test_own_call_is_not_judged(self):
        self.assertEqual(hook.judged("shipctl push --check-only a1b2c3d e4f5a6b"), "")

    def test_foreign_commands_pass(self):
        for command in ("git status", "git log --grep=push", "echo push",
                        "gh pr list", "glab mr view 12"):
            self.assertEqual(hook.judged(command), "", command)


class TestHook(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = os.path.join(self.tmp.name, "repo")
        os.makedirs(self.repo)
        self.git("init", "-q", "-b", "main")
        with open(os.path.join(self.repo, "code.txt"), "w") as f:
            f.write("код\n")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "feat: правка")
        head = self.git("rev-parse", "HEAD").stdout.strip()
        # Коммит ремоута, от которого рубеж считает диапазон: без него сравнить
        # HEAD не с чем, и рубеж молчит по другой причине, чем предмет теста.
        self.git("update-ref", "refs/remotes/origin/main", head)
        self.bin = os.path.join(self.tmp.name, "bin")
        os.makedirs(self.bin)
        self.calls = os.path.join(self.tmp.name, "calls.log")

    def git(self, *args):
        env = dict(os.environ,
                   GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null",
                   GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        return subprocess.run(["git", "-C", self.repo] + list(args), env=env,
                              capture_output=True, text=True, check=True)

    def shipctl(self, code, text=REFUSAL):
        path = os.path.join(self.bin, "shipctl")
        with open(path, "w") as f:
            f.write('#!/bin/sh\necho "$@" >> "%s"\n[ %d -eq 0 ] && exit 0\n'
                    'echo "ошибка: %s" >&2\nexit 1\n' % (self.calls, code, text))
        os.chmod(path, 0o755)

    def called(self):
        if not os.path.exists(self.calls):
            return []
        with open(self.calls) as f:
            return [ln.strip() for ln in f if ln.strip()]

    def fire(self, command, cwd=None, path=None):
        # PATH собирается тут же и целиком: git рубежу нужен всегда, а
        # установленный на машине shipctl подменяется скриптом стенда, иначе
        # исход тестов зависел бы от состояния хоста.
        env = {"PATH": (self.bin if path is None else path) + os.pathsep + SYSTEM_PATH,
               "HOME": os.environ.get("HOME", "")}
        return subprocess.run([sys.executable, TOOL, "--hook"], input=event(command, cwd or self.repo),
                              capture_output=True, text=True, env=env)

    def test_push_without_trace_is_refused(self):
        self.shipctl(1)
        r = self.fire("git push")
        self.assertEqual(r.returncode, 2)
        self.assertIn(REFUSAL, r.stderr)
        self.assertIn("рубежом ревью", r.stderr)
        self.assertIn("по скиллу review", r.stderr)
        self.assertEqual(len(self.called()), 1)

    def test_push_with_trace_passes(self):
        self.shipctl(0)
        r = self.fire("git push")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")
        self.assertEqual(len(self.called()), 1)

    def test_check_only_call_is_skipped(self):
        self.shipctl(1)
        r = self.fire("shipctl push --check-only a1b2c3d e4f5a6b")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.called(), [])

    def test_mr_create_is_refused(self):
        self.shipctl(1)
        r = self.fire("glab mr create --title 'правка' --description x")
        self.assertEqual(r.returncode, 2)
        self.assertIn("glab mr create", r.stderr)

    def test_command_without_push_passes(self):
        self.shipctl(1)
        r = self.fire("git status --short")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.called(), [])

    def test_permission_lifts_the_gate(self):
        self.shipctl(1)
        r = self.fire("DEVKIT_PUSH_OK=1 git push origin main")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.called(), [])

    def test_missing_shipctl_is_silent(self):
        # PATH без shipctl это чужой проект без установленного devkit: судить
        # нечем, и ломать там сессию рубеж не вправе.
        r = self.fire("git push", path=os.devnull)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")

    def test_outside_git_is_silent(self):
        self.shipctl(1)
        r = self.fire("git push", cwd=self.tmp.name)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.called(), [])

    def test_broken_event_is_silent(self):
        env = {"PATH": self.bin, "HOME": os.environ.get("HOME", "")}
        r = subprocess.run([sys.executable, TOOL, "--hook"], input="не json",
                           capture_output=True, text=True, env=env)
        self.assertEqual(r.returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
