#!/usr/bin/env python3
"""Хук ask-panel.py: в сессии, поднятой панелью (DEVKIT_TMUX chat-*/goal-*/
task-*), перехватывает AskUserQuestion и зовёт внутренний писатель признака
`taskctl ask`; обычный терминал и разговор без задачи хук пропускает молча, и
диалог харнеса там работает как раньше."""
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "ask-panel.py")
DATA = os.path.join(HERE, "testdata", "claude-code")

ONE = os.path.join(DATA, "pre-tool-use-ask.json")
PACK = os.path.join(DATA, "pre-tool-use-ask-pack.json")


def sample(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def event_with(path, **fields):
    """Образец события с подменёнными полями верхнего уровня: тесту нужен
    свой cwd или session_id, а форма остального события остаётся снятой
    живьём."""
    data = json.loads(sample(path))
    data.update(fields)
    return json.dumps(data)


def stub_taskctl(bin_dir, calls_path, exit_code=0, stdout="писатель отработал"):
    """Кладёт фальшивый taskctl на PATH: он не пишет признак по-настоящему, а
    только запоминает, какими аргументами и с каким stdin его позвали, и
    отвечает заданным кодом. Настоящий признак и настоящая парковка это дело
    Go-тестов (tools/taskctl/ask_test.go), тут проверяется только сам вызов
    хука: гейт, разбор задачи, пачка вопросов и отказ исходному инструменту."""
    path = os.path.join(bin_dir, "taskctl")
    with open(path, "w", encoding="utf-8") as f:
        f.write("#!/bin/sh\n")
        f.write('printf \'%%s\\n\' "$*" >> %s\n' % calls_path)
        f.write("cat >> %s.in\n" % calls_path)
        f.write("printf '%s'\n" % stdout)
        f.write("exit %d\n" % exit_code)
    st = os.stat(path)
    os.chmod(path, st.st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)


def run(stdin_text, env):
    full_env = dict(env)
    full_env.setdefault("PATH", os.environ.get("PATH", ""))
    full_env.setdefault("HOME", os.environ.get("HOME", ""))
    return subprocess.run([sys.executable, TOOL, "--hook"],
                          input=stdin_text, capture_output=True, text=True, env=full_env)


class TestGatePassesThrough(unittest.TestCase):
    """Три случая, где хук ничего не трогает: диалог харнеса остаётся
    диалогом, и вывода у хука нет вовсе."""

    def test_plain_terminal_passes(self):
        r = run(sample(ONE), {})
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr, "")

    def test_tmux_name_without_panel_prefix_passes(self):
        r = run(sample(ONE), {"DEVKIT_TMUX": "какое-то-окно"})
        self.assertEqual(r.returncode, 0)

    def test_conversation_without_task_passes(self):
        # DEVKIT_TMUX похож на панель, но ни заказа, ни реестра, ни хвоста
        # дерева с задачей нет: разговор личный, парковать нечего.
        with tempfile.TemporaryDirectory() as home:
            r = run(event_with(ONE, cwd="/private/tmp"), {"DEVKIT_TMUX": "chat-7", "HOME": home})
            self.assertEqual(r.returncode, 0)


class TestOtherToolsPass(unittest.TestCase):
    def test_bash_pre_tool_use_passes(self):
        r = run(json.dumps({"hook_event_name": "PreToolUse", "tool_name": "Bash",
                            "tool_input": {"command": "ls"}}), {"DEVKIT_TMUX": "task-XR-1-1"})
        self.assertEqual(r.returncode, 0)

    def test_post_tool_use_ask_event_passes(self):
        data = json.loads(sample(ONE))
        data["hook_event_name"] = "PostToolUse"
        r = run(json.dumps(data), {"DEVKIT_TMUX": "task-XR-1-1", "DEVKIT_TASK": "XR-1"})
        self.assertEqual(r.returncode, 0)


class TestInterceptInPanelSession(unittest.TestCase):
    def test_panel_session_blocks_and_calls_writer(self):
        with tempfile.TemporaryDirectory() as bin_dir, tempfile.TemporaryDirectory() as home:
            calls = os.path.join(bin_dir, "calls.log")
            stub_taskctl(bin_dir, calls, stdout="XR-1: вопрос припаркован")
            env = {"DEVKIT_TMUX": "task-XR-1-1", "DEVKIT_TASK": "XR-1",
                   "PATH": bin_dir + os.pathsep + os.environ.get("PATH", ""), "HOME": home}
            r = run(sample(ONE), env)
            self.assertEqual(r.returncode, 2, r.stderr)
            self.assertIn("не покажется", r.stderr)
            self.assertIn("XR-1: вопрос припаркован", r.stderr)
            with open(calls, encoding="utf-8") as f:
                argline = f.read()
            self.assertIn("ask XR-1 --session 3f0b1572-ce3b-4012-a67b-447f6f3a52cb", argline)
            with open(calls + ".in", encoding="utf-8") as f:
                pack = json.load(f)
            self.assertEqual(len(pack["questions"]), 1)
            self.assertEqual(pack["questions"][0]["text"], "продолжать?")
            self.assertEqual([o["label"] for o in pack["questions"][0]["options"]], ["да", "нет"])
            self.assertEqual(pack["questions"][0]["options"][0]["note"], "Продолжить")

    def test_pack_of_multiple_questions_with_multiselect(self):
        with tempfile.TemporaryDirectory() as bin_dir, tempfile.TemporaryDirectory() as home:
            calls = os.path.join(bin_dir, "calls.log")
            stub_taskctl(bin_dir, calls)
            env = {"DEVKIT_TMUX": "goal-XR-2", "DEVKIT_TASK": "XR-2",
                   "PATH": bin_dir + os.pathsep + os.environ.get("PATH", ""), "HOME": home}
            r = run(sample(PACK), env)
            self.assertEqual(r.returncode, 2)
            with open(calls + ".in", encoding="utf-8") as f:
                pack = json.load(f)
            self.assertEqual(len(pack["questions"]), 2)
            self.assertNotIn("multi", pack["questions"][0])
            self.assertTrue(pack["questions"][1]["multi"])
            self.assertEqual(len(pack["questions"][1]["options"]), 3)

    def test_task_from_registry_when_order_is_missing(self):
        with tempfile.TemporaryDirectory() as bin_dir, tempfile.TemporaryDirectory() as home:
            calls = os.path.join(bin_dir, "calls.log")
            stub_taskctl(bin_dir, calls)
            os.makedirs(os.path.join(home, ".devkit"), exist_ok=True)
            with open(os.path.join(home, ".devkit", "sessions.log"), "w", encoding="utf-8") as f:
                f.write("2026-01-01T00:00:00 сессия 3f0b1572-ce3b-4012-a67b-447f6f3a52cb задача "
                        "XR-9 проект x дерево /tmp транскрипт /tmp/t.jsonl источник заказ "
                        "повод startup tmux task-XR-9-1\n")
            env = {"DEVKIT_TMUX": "task-XR-9-1",
                   "PATH": bin_dir + os.pathsep + os.environ.get("PATH", ""), "HOME": home}
            r = run(sample(ONE), env)
            self.assertEqual(r.returncode, 2, r.stderr)
            with open(calls, encoding="utf-8") as f:
                argline = f.read()
            self.assertIn("ask XR-9 --session", argline)

    def test_task_from_tree_tail_when_order_and_registry_are_missing(self):
        with tempfile.TemporaryDirectory() as bin_dir, tempfile.TemporaryDirectory() as home:
            calls = os.path.join(bin_dir, "calls.log")
            stub_taskctl(bin_dir, calls)
            env = {"DEVKIT_TMUX": "task-XR-8-1",
                   "PATH": bin_dir + os.pathsep + os.environ.get("PATH", ""), "HOME": home}
            r = run(event_with(ONE, cwd="/nonexistent/devkit-xr-8"), env)
            self.assertEqual(r.returncode, 2, r.stderr)
            with open(calls, encoding="utf-8") as f:
                argline = f.read()
            self.assertIn("ask XR-8 --session", argline)

    def test_writer_failure_still_blocks(self):
        # Писатель отказал (доска не читается, потолок вопросов): диалог
        # харнеса панель всё равно не покажет, и молчать про отказ нельзя.
        with tempfile.TemporaryDirectory() as bin_dir, tempfile.TemporaryDirectory() as home:
            calls = os.path.join(bin_dir, "calls.log")
            stub_taskctl(bin_dir, calls, exit_code=1, stdout="доска не читается")
            env = {"DEVKIT_TMUX": "task-XR-3-1", "DEVKIT_TASK": "XR-3",
                   "PATH": bin_dir + os.pathsep + os.environ.get("PATH", ""), "HOME": home}
            r = run(sample(ONE), env)
            self.assertEqual(r.returncode, 2)
            self.assertIn("не покажется", r.stderr)
            self.assertIn("доска не читается", r.stderr)

    def test_missing_taskctl_still_blocks(self):
        # taskctl не стоит в PATH вовсе: заход всё равно узнаёт, что диалог
        # не покажется, а не проваливается молча в тишину дашборда.
        with tempfile.TemporaryDirectory() as bin_dir, tempfile.TemporaryDirectory() as home:
            env = {"DEVKIT_TMUX": "task-XR-4-1", "DEVKIT_TASK": "XR-4",
                   "PATH": bin_dir, "HOME": home}
            r = run(sample(ONE), env)
            self.assertEqual(r.returncode, 2)
            self.assertIn("не покажется", r.stderr)


if __name__ == "__main__":
    unittest.main()
