#!/usr/bin/env python3
"""Рубеж связки cd с командой: PreToolUse-хук ловит «cd <каталог> && <команда>»
там, где связка уходит вопросом к человеку (чтение файла по относительному
пути, git после cd, редирект в файл), подсказывает готовую замену, а одинокий
cd, связку с командой без чтения, команду без cd и слово cd внутри кавычек
пропускает."""
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
    def test_reader_with_relative_path_is_caught(self):
        r = run("cd /Users/rider/projects/devkit " + "&&" + " grep -n SECRET_DENY tools/devkitctl/perms.py | head -3")
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertIn("grep -n SECRET_DENY /Users/rider/projects/devkit/tools/devkitctl/perms.py | head -3", r.stdout)

    def test_cat_relative_is_caught(self):
        r = run("cd /tmp && cat a.txt")
        self.assertEqual(r.returncode, 1)
        self.assertIn("cat /tmp/a.txt", r.stdout)

    def test_dot_operand_is_caught(self):
        r = run("cd /tmp && grep -rn pat .")
        self.assertEqual(r.returncode, 1)
        self.assertIn("grep -rn pat /tmp", r.stdout)

    def test_git_after_cd_is_caught(self):
        r = run("cd /tmp && git log -n 1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("git -C /tmp log -n 1", r.stdout)

    def test_file_redirect_is_caught(self):
        r = run("cd /tmp && make > build.log")
        self.assertEqual(r.returncode, 1)
        self.assertIn("make > /tmp/build.log", r.stdout)

    def test_semicolon_is_caught(self):
        self.assertEqual(run("cd /tmp; cat a.txt").returncode, 1)

    def test_subshell_is_caught(self):
        r = run("(cd /tmp && sed -n 1,5p x.py)")
        self.assertEqual(r.returncode, 1)
        self.assertIn("sed -n 1,5p /tmp/x.py", r.stdout)

    def test_substitution_is_caught(self):
        self.assertEqual(run("echo $(cd /tmp && cat a.txt)").returncode, 1)

    def test_newline_is_caught(self):
        r = run("--stdin", input="cd /tmp\ncat a.txt\n")
        self.assertEqual(r.returncode, 1, r.stdout)

    def test_reader_later_in_chain_is_caught(self):
        r = run("cd /tmp && make && tail -n 5 build.log")
        self.assertEqual(r.returncode, 1)
        self.assertIn("make && tail -n 5 /tmp/build.log", r.stdout)

    def test_bare_ls_is_caught_with_dir_in_replacement(self):
        r = run("cd /tmp && ls | head -2")
        self.assertEqual(r.returncode, 1)
        self.assertIn("ls /tmp | head -2", r.stdout)
        self.assertEqual(run("cd /tmp; ls").returncode, 1)

    def test_find_keeps_its_conditions(self):
        r = run("cd /tmp && find . -name '*.py'")
        self.assertEqual(r.returncode, 1)
        self.assertIn("find /tmp -name '*.py'", r.stdout)

    def test_variable_dir_advises_lone_cd(self):
        r = run("cd $ROOT && cat a.txt")
        self.assertEqual(r.returncode, 1)
        self.assertIn("одиноким `cd $ROOT`", r.stdout)
        self.assertNotIn("$ROOT/a.txt", r.stdout)


class TestHarmlessPasses(unittest.TestCase):
    def test_lone_cd_passes(self):
        self.assertEqual(run("cd /Users/rider/projects/devkit").returncode, 0)

    def test_trailing_separator_passes(self):
        self.assertEqual(run("cd /tmp;").returncode, 0)

    def test_cd_with_build_passes(self):
        self.assertEqual(run("cd /tmp && cargo test").returncode, 0)
        self.assertEqual(run("cd /tmp && go test ./...").returncode, 0)
        self.assertEqual(run("cd /tmp && python3 t.py").returncode, 0)

    def test_reader_with_absolute_path_passes(self):
        self.assertEqual(run("cd /tmp && grep pat /abs/file").returncode, 0)

    def test_stdin_reader_in_pipe_passes(self):
        self.assertEqual(run("cd /tmp && go test ./... 2>&1 | tail -1").returncode, 0)

    def test_cd_with_echo_and_make_passes(self):
        self.assertEqual(run("cd /tmp && echo hi").returncode, 0)
        self.assertEqual(run("cd /tmp && make -n 2>&1 | head -1").returncode, 0)

    def test_devnull_redirect_passes(self):
        self.assertEqual(run("cd /tmp && make 2>/dev/null").returncode, 0)
        self.assertEqual(run("cd /tmp && make 2>&1 | tail -3").returncode, 0)

    def test_absolute_path_without_cd_passes(self):
        self.assertEqual(run("grep -rn SECRET_DENY /Users/rider/projects/devkit").returncode, 0)

    def test_relative_path_without_cd_passes(self):
        self.assertEqual(run("grep -n SECRET_DENY tools/devkitctl/perms.py").returncode, 0)

    def test_git_c_passes(self):
        self.assertEqual(run("git -C /tmp log -n 5 | head").returncode, 0)

    def test_cd_last_passes(self):
        self.assertEqual(run("cat /tmp/a && cd /tmp").returncode, 0)

    def test_cd_inside_quotes_passes(self):
        r = run("--stdin", input="claude -p 'run: cd ~/x && grep a b.txt'")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_cd_inside_heredoc_passes(self):
        r = run("--stdin", input="cat > /tmp/f.sh <<'EOF'\ncd /tmp && cat a.txt\nEOF\n")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_word_with_cd_prefix_passes(self):
        self.assertEqual(run("cdk deploy && cat out.txt").returncode, 0)

    def test_broken_quoting_passes(self):
        self.assertEqual(run("--stdin", input="cd /tmp && cat 'unclosed").returncode, 0)


class TestHookMode(unittest.TestCase):
    def test_compound_is_blocked(self):
        r = run("--hook", input=bash_event("cd ~/projects/devkit && grep -rn foo tools"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("DK-770", r.stderr)

    def test_advice_leads_with_replacement(self):
        r = run("--hook", input=bash_event("cd /tmp && cat a.txt"))
        self.assertEqual(r.returncode, 2)
        self.assertTrue(r.stderr.startswith("Связка с cd отбита"), r.stderr)
        self.assertIn("cat /tmp/a.txt", r.stderr.splitlines()[0])

    def test_harmless_compound_passes(self):
        self.assertEqual(run("--hook", input=bash_event("cd /tmp && cargo test")).returncode, 0)

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
        event["tool_input"]["command"] = "cd /tmp && cat a.txt"
        r = run("--hook", input=json.dumps(event))
        self.assertEqual(r.returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=0)
