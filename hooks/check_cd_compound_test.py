#!/usr/bin/env python3
"""Рубеж связки cd с командой: PreToolUse-хук ловит «cd <каталог> && <команда>»
во всех разделителях, включая подшелл и перевод строки, а одинокий cd, команду
без cd и слово cd внутри кавычек пропускает."""
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-cd-compound.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "pre-tool-use-bash.json")


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def bash_event(command):
    return json.dumps({"tool_name": "Bash", "tool_input": {"command": command}})


class TestCompoundIsCaught(unittest.TestCase):
    def test_and_is_caught(self):
        r = run("cd ~/projects/devkit && grep -rn SECRET_DENY tools/devkitctl/perms.py")
        self.assertEqual(r.returncode, 1, r.stdout)

    def test_semicolon_is_caught(self):
        self.assertEqual(run("cd /tmp; ls").returncode, 1)

    def test_or_is_caught(self):
        self.assertEqual(run("cd /tmp || echo no").returncode, 1)

    def test_pipe_is_caught(self):
        self.assertEqual(run("cd /tmp | cat").returncode, 1)

    def test_subshell_is_caught(self):
        self.assertEqual(run("(cd /tmp && ls)").returncode, 1)

    def test_substitution_is_caught(self):
        self.assertEqual(run("echo $(cd /tmp && ls)").returncode, 1)

    def test_newline_is_caught(self):
        r = run("--stdin", input="cd /tmp\ngrep -rn foo bar\n")
        self.assertEqual(r.returncode, 1, r.stdout)

    def test_second_cd_is_caught(self):
        self.assertEqual(run("cd /tmp && cd /var").returncode, 1)


class TestCleanCommandPasses(unittest.TestCase):
    def test_lone_cd_passes(self):
        self.assertEqual(run("cd /Users/rider/projects/devkit").returncode, 0)

    def test_trailing_separator_passes(self):
        self.assertEqual(run("cd /tmp;").returncode, 0)

    def test_absolute_path_passes(self):
        self.assertEqual(run("grep -rn SECRET_DENY /Users/rider/projects/devkit").returncode, 0)

    def test_git_c_passes(self):
        self.assertEqual(run("git -C /tmp log -n 5 | head").returncode, 0)

    def test_cd_last_passes(self):
        # cd в хвосте связки читать по относительному пути нечему: вторая
        # команда идёт до него, а не после.
        self.assertEqual(run("ls /tmp && cd /tmp").returncode, 0)

    def test_cd_inside_quotes_passes(self):
        # Текст промпта у headless-прогона: слово cd тут аргумент, а не команда.
        r = run("--stdin", input="claude -p 'cd ~/projects/devkit && grep foo bar' --model haiku")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_cd_inside_heredoc_passes(self):
        r = run("--stdin", input="cat > /tmp/f <<'EOF'\ncd /tmp && ls\nEOF\n")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_word_with_cd_prefix_passes(self):
        self.assertEqual(run("cdk deploy && echo ok").returncode, 0)

    def test_broken_quoting_passes(self):
        # Неразобранный shell рубеж пропускает молча: ложный отказ на каждом
        # ходе Bash дороже пропущенной связки.
        self.assertEqual(run("--stdin", input="cd /tmp && echo 'unclosed").returncode, 0)


class TestHookMode(unittest.TestCase):
    def test_compound_is_blocked(self):
        r = run("--hook", input=bash_event("cd ~/projects/devkit && grep -rn foo tools"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("DK-770", r.stderr)

    def test_advice_names_the_way_out(self):
        r = run("--hook", input=bash_event("cd /tmp && ls"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("git -C", r.stderr)
        self.assertIn("отдельным вызовом", r.stderr)

    def test_lone_cd_passes(self):
        self.assertEqual(run("--hook", input=bash_event("cd /tmp")).returncode, 0)

    def test_missing_command_passes(self):
        r = run("--hook", input=json.dumps({"tool_name": "Edit", "tool_input": {}}))
        self.assertEqual(r.returncode, 0)

    def test_bad_json_passes(self):
        self.assertEqual(run("--hook", input="not json").returncode, 0)


class TestSampleEvent(unittest.TestCase):
    """Живой снимок события из testdata разбирается хуком как есть: форма лежит
    на стороне инструмента, и фиксирует её образец, а не память."""

    def test_sample_event_is_read(self):
        with open(SAMPLE, encoding="utf-8") as f:
            event = json.load(f)
        event["tool_input"]["command"] = "cd /tmp && ls"
        r = run("--hook", input=json.dumps(event))
        self.assertEqual(r.returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=0)
