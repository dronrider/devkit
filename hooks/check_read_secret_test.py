#!/usr/bin/env python3
"""Рубеж чтения секретов через Bash: PreToolUse-хук ловит обращение команды к
файлу доступов, приватным ключам, хранилищу secretctl и local-docs, нейтральная
команда проходит. Разворот ~ матчит и знак в команде, и абсолютный путь; обход
через $HOME хуком не ловится, и это ограничение надёжного минимума, а не баг."""
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-read-secret.py")
HOME = os.path.expanduser("~")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "pre-tool-use-bash.json")


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def bash_event(command):
    return json.dumps({"tool_name": "Bash", "tool_input": {"command": command}})


class TestCommandMode(unittest.TestCase):
    def test_access_file_is_caught(self):
        self.assertEqual(run("cat ~/.claude/access.local.md").returncode, 1)

    def test_ssh_key_is_caught(self):
        self.assertEqual(run("grep foo ~/.ssh/id_rsa").returncode, 1)

    def test_secret_store_is_caught(self):
        self.assertEqual(run("cat ~/.devkit/secrets/token").returncode, 1)

    def test_local_docs_is_caught(self):
        self.assertEqual(run("head local-docs/hosts.md").returncode, 1)

    def test_absolute_path_is_caught(self):
        self.assertEqual(run("cat %s/.claude/access.local.md" % HOME).returncode, 1)

    def test_neutral_command_passes(self):
        self.assertEqual(run("ls /tmp").returncode, 0)

    def test_unrelated_tilde_passes(self):
        self.assertEqual(run("ls ~/projects").returncode, 0)

    def test_dollar_home_is_not_expanded(self):
        # Развилка постановки: переменные хук не разворачивает. Тест держит
        # контракт: если кто-то добавит разворот $HOME, проверка тут упадёт и
        # заставит пересмотреть рубеж, а не протащить тихо.
        self.assertEqual(run("cat $HOME/.claude/access.local.md").returncode, 0)


class TestHookMode(unittest.TestCase):
    def test_access_file_is_blocked(self):
        r = run("--hook", input=bash_event("cat ~/.claude/access.local.md"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("access.local.md", r.stderr)

    def test_neutral_command_passes(self):
        r = run("--hook", input=bash_event("ls /tmp"))
        self.assertEqual(r.returncode, 0)

    def test_advice_mentions_secretctl(self):
        r = run("--hook", input=bash_event("cat ~/.ssh/id_rsa"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("secretctl", r.stderr)

    def test_missing_command_passes(self):
        r = run("--hook", input=json.dumps({"tool_name": "Edit", "tool_input": {}}))
        self.assertEqual(r.returncode, 0)

    def test_bad_json_passes(self):
        r = run("--hook", input="not json")
        self.assertEqual(r.returncode, 0)


class TestSampleEvent(unittest.TestCase):
    """Живой снимок события из testdata разбирается хуком как есть: форма
    лежит на стороне инструмента, и фиксирует её образец, а не память."""

    def test_sample_blocks_on_access_file(self):
        with open(SAMPLE, encoding="utf-8") as f:
            r = run("--hook", input=f.read())
        self.assertEqual(r.returncode, 2)
        self.assertIn("access.local.md", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
