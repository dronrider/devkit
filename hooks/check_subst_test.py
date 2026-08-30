#!/usr/bin/env python3
"""Рубеж подстановки в свободном тексте (DK-452): вызов утилиты devkit с
обратными кавычками или $(...) в текстовом аргументе отбивается, служебная
подстановка целым аргументом и чужие утилиты проходят. Одинарные кавычки и
heredoc с кавычками у делимитера безопасны, и рубеж их не трогает."""
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-subst.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "pre-tool-use-bash.json")

# Команда инцидента DK-438: текст замечания в двойных кавычках, внутри имя
# команды в обратных кавычках, bash запустил её до вызова taskctl.
INJECTION = 'taskctl review add DK-438 "позвать `devkitctl update` до отказа"'


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def bash_event(command):
    return json.dumps({"tool_name": "Bash", "tool_input": {"command": command}})


class TestCommandMode(unittest.TestCase):
    def test_backticks_in_note_are_caught(self):
        r = run(INJECTION)
        self.assertEqual(r.returncode, 1)
        self.assertIn("heredoc", r.stdout)
        self.assertIn("stdin", r.stdout)

    def test_dollar_paren_in_note_is_caught(self):
        r = run('taskctl draft "снять снимок $(date) и приложить"')
        self.assertEqual(r.returncode, 1)

    def test_all_devkit_tools_are_covered(self):
        for tool in ("shipctl", "agentctl", "devkitctl", "secretctl"):
            r = run('%s something "имя `x`"' % tool)
            self.assertEqual(r.returncode, 1, tool)
            self.assertIn(tool, r.stdout)

    def test_foreign_tool_passes(self):
        self.assertEqual(run('git commit -m "правка `main`"').returncode, 0)

    def test_plain_note_passes(self):
        self.assertEqual(
            run('taskctl review add DK-438 "замечание без подстановок"').returncode, 0)

    def test_single_quotes_pass(self):
        self.assertEqual(
            run("taskctl review add DK-438 'позвать `devkitctl update`'").returncode, 0)

    def test_commit_key_with_rev_parse_passes(self):
        self.assertEqual(
            run('shipctl merge DK-1 --commit "$(git rev-parse HEAD)"').returncode, 0)
        self.assertEqual(
            run('shipctl merge DK-1 --commit=$(git rev-parse HEAD)').returncode, 0)

    def test_c_key_with_pwd_passes(self):
        self.assertEqual(run('taskctl -C "$(pwd)" list').returncode, 0)

    def test_quoted_heredoc_passes(self):
        cmd = "taskctl review add DK-438 <<'EOF'\nпозвать `devkitctl update`\nEOF\n"
        self.assertEqual(run(cmd).returncode, 0)

    def test_unquoted_heredoc_is_caught(self):
        cmd = "taskctl review add DK-438 <<EOF\nпозвать `devkitctl update`\nEOF\n"
        self.assertEqual(run(cmd).returncode, 1)

    def test_devkit_call_inside_compound_is_caught(self):
        r = run('cd /tmp && taskctl draft "имя `x`" && git log')
        self.assertEqual(r.returncode, 1)

    def test_env_wrapper_is_seen_through(self):
        r = run('env GOWORK=off taskctl draft "имя `x`"')
        self.assertEqual(r.returncode, 1)

    def test_stdin_mode(self):
        r = run("--stdin", input=INJECTION)
        self.assertEqual(r.returncode, 1)


class TestHookMode(unittest.TestCase):
    def test_injection_is_blocked(self):
        r = run("--hook", input=bash_event(INJECTION))
        self.assertEqual(r.returncode, 2)
        self.assertIn("DK-452", r.stderr)
        self.assertIn("heredoc", r.stderr)

    def test_neutral_command_passes(self):
        r = run("--hook", input=bash_event("taskctl list"))
        self.assertEqual(r.returncode, 0)

    def test_missing_command_passes(self):
        r = run("--hook", input=json.dumps({"tool_name": "Edit", "tool_input": {}}))
        self.assertEqual(r.returncode, 0)

    def test_bad_json_passes(self):
        r = run("--hook", input="not json")
        self.assertEqual(r.returncode, 0)


class TestSampleEvent(unittest.TestCase):
    """Живой снимок события из testdata разбирается хуком как есть: форма
    лежит на стороне инструмента, и фиксирует её образец, а не память. Команда
    в снимке заменяется на инъекцию, остальные поля не трогаются."""

    def test_sample_shape_blocks_injection(self):
        with open(SAMPLE, encoding="utf-8") as f:
            event = json.load(f)
        event["tool_input"]["command"] = INJECTION
        r = run("--hook", input=json.dumps(event))
        self.assertEqual(r.returncode, 2)
        self.assertIn("DK-452", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
