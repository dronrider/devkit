#!/usr/bin/env python3
"""Рубеж прямого прогона тестов: хук ловит голый `go test` и
`python3 -m unittest` в дереве проекта devkit, печатает замену обёрткой
`devkitctl test` и пропускает прогон под обёрткой, чужое дерево и всё, что
прогоном тестов не является."""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-bare-test.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "pre-tool-use-bash.json")


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


class Stand(unittest.TestCase):
    """Два дерева: одно с каталогом `.devkit`, второе без него."""

    def setUp(self):
        # realpath: на macOS /var это симлинк на /private/var, и хук печатает
        # в замене разрешённый путь, а стенд сравнивает с ним строку.
        self.base = os.path.realpath(tempfile.mkdtemp(prefix="check-bare-test-"))
        self.proj = os.path.join(self.base, "proj")
        os.makedirs(os.path.join(self.proj, ".devkit"))
        self.pkg = os.path.join(self.proj, "tools", "taskctl")
        os.makedirs(self.pkg)
        self.alien = os.path.join(self.base, "alien", "tools", "x")
        os.makedirs(self.alien)

    def tearDown(self):
        shutil.rmtree(self.base, ignore_errors=True)

    def event(self, command, cwd=None):
        return json.dumps({"tool_name": "Bash", "cwd": cwd or self.pkg,
                           "tool_input": {"command": command}})


class TestBareRunIsCaught(Stand):
    def test_go_test_in_cwd_is_caught(self):
        r = run("go test ./...", cwd=self.pkg)
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertIn("devkitctl test " + self.pkg, r.stdout)

    def test_go_test_with_dash_c_names_that_directory(self):
        r = run("go test -C %s ./..." % self.pkg, cwd=self.base)
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertIn("devkitctl test " + self.pkg, r.stdout)

    def test_run_flag_moves_into_the_replacement(self):
        r = run("go test ./... -run TestBoardMove", cwd=self.pkg)
        self.assertEqual(r.returncode, 1)
        self.assertIn("devkitctl test %s -run TestBoardMove" % self.pkg, r.stdout)

    def test_own_flags_of_the_wrapper_are_dropped(self):
        # Доля бюджета и потолок кэша это расчёт самой обёртки, и в замену они
        # не переезжают. Потолок времени переезжает, он остаётся за агентом.
        r = run("go test -count=1 -p 4 ./... -v", cwd=self.pkg)
        self.assertEqual(r.returncode, 1)
        self.assertIn("devkitctl test %s -v" % self.pkg, r.stdout)
        first = r.stdout.splitlines()[0]
        self.assertNotIn("-count", first)
        self.assertNotIn("-p 4", first)

    def test_gowork_assignment_before_the_command_is_caught(self):
        r = run("GOWORK=off go test ./...", cwd=self.pkg)
        self.assertEqual(r.returncode, 1, r.stdout)

    def test_wrapper_prefix_with_its_own_flags_is_caught(self):
        # Ключ обёртки со значением отдельным словом читался бы как имя
        # команды, и прогон уходил бы мимо рубежа (замечание ревью круга 1).
        for cmd in ("nice go test ./...",
                    "nice -n 19 go test ./...",
                    "nice -19 go test ./...",
                    "env -u GOWORK go test ./...",
                    "env -i GOWORK=off go test ./...",
                    "timeout 600 go test ./...",
                    "timeout -k 5 20m go test ./...",
                    "sudo -u t go test ./...",
                    "stdbuf -oL go test ./...",
                    "nice -n 19 python3 -m unittest discover -p '*_test.py'"):
            r = run(cmd, cwd=self.pkg)
            self.assertEqual(r.returncode, 1, "%s: %s" % (cmd, r.stdout))

    def test_own_timeout_moves_into_the_replacement(self):
        # Потолок времени обёртка отдаёт агенту: пакет, которому двадцати минут
        # мало, иначе не прогнать вовсе (замечание ревью круга 1).
        r = run("go test -timeout=40m ./...", cwd=self.pkg)
        self.assertEqual(r.returncode, 1)
        self.assertIn("devkitctl test %s -timeout=40m" % self.pkg, r.stdout)

    def test_unittest_is_caught(self):
        r = run("python3 -m unittest discover -p '*_test.py'", cwd=self.pkg)
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertIn("devkitctl test " + self.pkg, r.stdout)

    def test_unittest_module_name_moves_into_the_replacement(self):
        r = run("python3 -m unittest board_test", cwd=self.pkg)
        self.assertEqual(r.returncode, 1)
        self.assertIn("devkitctl test %s board_test" % self.pkg, r.stdout)

    def test_second_segment_of_a_chain_is_caught(self):
        r = run("cd %s && go test ./..." % self.pkg, cwd=self.base)
        self.assertEqual(r.returncode, 1, r.stdout)

    def test_pipe_to_tail_is_caught(self):
        r = run("go test ./... 2>&1 | tail -3", cwd=self.pkg)
        self.assertEqual(r.returncode, 1, r.stdout)

    def test_reason_names_the_ceiling(self):
        r = run("go test ./...", cwd=self.pkg)
        self.assertIn("parallel-slots", r.stdout)


class TestWrappedAndForeignPass(Stand):
    def test_run_under_regcheck_passes(self):
        self.assertEqual(run("regcheck -- go test ./...", cwd=self.pkg).returncode, 0)

    def test_run_under_the_wrapper_passes(self):
        r = run("devkitctl test %s" % self.pkg, cwd=self.base)
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_full_run_passes(self):
        self.assertEqual(run("python3 tools/devkitctl/parallel.py",
                             cwd=self.proj).returncode, 0)

    def test_alien_tree_passes(self):
        r = run("go test ./...", cwd=self.alien)
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_other_go_commands_pass(self):
        self.assertEqual(run("go build ./...", cwd=self.pkg).returncode, 0)
        self.assertEqual(run("go vet ./...", cwd=self.pkg).returncode, 0)

    def test_other_python_modules_pass(self):
        self.assertEqual(run("python3 -m json.tool a.json", cwd=self.pkg).returncode, 0)
        self.assertEqual(run("python3 suite.py", cwd=self.pkg).returncode, 0)

    def test_word_with_go_prefix_passes(self):
        self.assertEqual(run("golangci-lint run", cwd=self.pkg).returncode, 0)

    def test_quoted_command_in_a_prompt_passes(self):
        r = run("--stdin", input="claude -p 'gone: go test ./...'", cwd=self.pkg)
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_broken_quoting_passes(self):
        r = run("--stdin", input="go test 'unclosed", cwd=self.pkg)
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_command_inside_heredoc_passes(self):
        # Пример прямой команды, записываемый в файл, ловил бы сам себя, и ни
        # сценарий стенда, ни раздел доки с таким примером в дереве devkit было
        # бы не написать (замечание ревью круга 1).
        body = "cat > doc.md <<'EOF'\ngo test ./... -run TestX\nEOF\n"
        r = run("--stdin", input=body, cwd=self.pkg)
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_command_after_heredoc_is_caught(self):
        body = ("cat > doc.md <<'EOF'\nпример\nEOF\n"
                "go test ./...\n")
        r = run("--stdin", input=body, cwd=self.pkg)
        self.assertEqual(r.returncode, 1, r.stdout)


class TestHookMode(Stand):
    def test_bare_run_is_blocked(self):
        r = run("--hook", input=self.event("go test ./..."))
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertIn("DK-1219", r.stderr)
        self.assertTrue(r.stderr.startswith("Прямой go test отбит"), r.stderr)
        self.assertIn("devkitctl test " + self.pkg, r.stderr.splitlines()[0])

    def test_unittest_is_blocked(self):
        r = run("--hook", input=self.event("python3 -m unittest discover -p '*_test.py'"))
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertTrue(r.stderr.startswith("Прямой python3 -m unittest отбит"),
                        r.stderr)

    def test_cwd_outside_devkit_tree_passes(self):
        r = run("--hook", input=self.event("go test ./...", cwd=self.alien))
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_wrapped_run_passes(self):
        r = run("--hook", input=self.event("regcheck -- go test ./..."))
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_missing_command_passes(self):
        r = run("--hook", input=json.dumps({"tool_name": "Edit", "tool_input": {}}))
        self.assertEqual(r.returncode, 0)

    def test_bad_json_passes(self):
        self.assertEqual(run("--hook", input="not json").returncode, 0)


class TestSampleEvent(Stand):
    """Живой снимок события из testdata разбирается хуком как есть: форма лежит
    на стороне инструмента, и фиксирует её образец, а не память."""

    def test_sample_event_is_read(self):
        with open(SAMPLE, encoding="utf-8") as f:
            event = json.load(f)
        event["cwd"] = self.pkg
        event["tool_input"]["command"] = "go test ./..."
        r = run("--hook", input=json.dumps(event))
        self.assertEqual(r.returncode, 2, r.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=0)
