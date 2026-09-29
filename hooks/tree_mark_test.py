#!/usr/bin/env python3
"""Проверка привязки «субагент -> дерево задачи» (DK-1072): PostToolUse-хук на
Bash кладёт привязку по команде субагента, назвавшей ID задачи, не метит ход
самой сессии, не метит команду без утилиты доски и молчит, когда дерева задачи
среди рабочих деревьев репозитория нет."""
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "tree-mark.py")


def load():
    spec = importlib.util.spec_from_file_location("tree_mark", TOOL)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, HERE)
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.pop(0)
    return mod


tree_mark = load()


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def git(*args, cwd=None):
    out = subprocess.run(["git", "-c", "user.email=stand@example.com",
                          "-c", "user.name=stand"] + list(args),
                         cwd=cwd, capture_output=True, text=True)
    if out.returncode != 0:
        raise AssertionError("git %s: %s" % (" ".join(args), out.stderr))
    return out.stdout


def bash_event(command, session="s1", agent="a1", cwd=""):
    e = {"session_id": session, "hook_event_name": "PostToolUse",
         "tool_name": "Bash", "tool_input": {"command": command}, "cwd": cwd}
    if agent:
        e["agent_id"] = agent
    return json.dumps(e)


class TreeCase(unittest.TestCase):
    """Репозиторий с основным чекаутом и боковым деревом ветки dk-1072: по нему
    хук ищет дерево задачи, и подменить git тут нечем, ответ нужен настоящий."""

    def setUp(self):
        self.state = tempfile.mkdtemp()
        self.home = tempfile.mkdtemp()
        self.main = os.path.join(self.home, "proj")
        os.makedirs(self.main)
        git("init", "-b", "main", self.main)
        with open(os.path.join(self.main, "README.md"), "w", encoding="utf-8") as f:
            f.write("проект\n")
        git("add", "README.md", cwd=self.main)
        git("commit", "-m", "первый", cwd=self.main)
        self.side = os.path.join(self.home, "proj-dk-1072")
        git("worktree", "add", "-b", "dk-1072", self.side, cwd=self.main)

    def tearDown(self):
        shutil.rmtree(self.state, ignore_errors=True)
        shutil.rmtree(self.home, ignore_errors=True)

    def hook(self, event):
        return run("--hook", "--state", self.state, input=event)

    def binding(self, context="s1.a1"):
        path = os.path.join(self.state, context + ".json")
        if not os.path.exists(path):
            return None
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)


class TestMarksTaskTree(TreeCase):
    def test_plan_command_binds_side_tree(self):
        r = self.hook(bash_event("agentctl plan set --label DK-1072-exec «этап»",
                                 cwd=self.main))
        self.assertEqual(r.returncode, 0, r.stderr)
        row = self.binding()
        self.assertIsNotNone(row)
        self.assertEqual(row["task"], "DK-1072")
        self.assertEqual(os.path.realpath(row["tree"]), os.path.realpath(self.side))

    def test_binding_lists_other_trees(self):
        self.hook(bash_event("taskctl show DK-1072", cwd=self.main))
        row = self.binding()
        others = [os.path.realpath(p) for p in row["others"]]
        self.assertIn(os.path.realpath(self.main), others)
        self.assertNotIn(os.path.realpath(self.side), others)

    def test_branch_with_tail_counts_as_task_tree(self):
        side = os.path.join(self.home, "proj-dk-470-lld-link")
        git("worktree", "add", "-b", "dk-470-lld-link", side, cwd=self.main)
        self.hook(bash_event("taskctl show DK-470", cwd=self.main))
        row = self.binding()
        self.assertEqual(row["task"], "DK-470")
        self.assertEqual(os.path.realpath(row["tree"]), os.path.realpath(side))

    def test_call_from_side_tree_finds_same_repo(self):
        r = self.hook(bash_event("taskctl elapsed DK-1072", cwd=self.side))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNotNone(self.binding())


class TestKeepsQuiet(TreeCase):
    def test_session_turn_without_agent_is_not_marked(self):
        r = self.hook(bash_event("taskctl show DK-1072", agent=None, cwd=self.main))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNone(self.binding("s1"))

    def test_command_without_board_tool_is_not_marked(self):
        r = self.hook(bash_event("grep -rn DK-1072 docs", cwd=self.main))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNone(self.binding())

    def test_command_without_task_id_is_not_marked(self):
        r = self.hook(bash_event("taskctl list", cwd=self.main))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNone(self.binding())

    def test_task_without_tree_is_not_marked(self):
        r = self.hook(bash_event("taskctl show DK-999", cwd=self.main))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNone(self.binding())

    def test_other_tool_is_not_marked(self):
        event = json.loads(bash_event("taskctl show DK-1072", cwd=self.main))
        event["tool_name"] = "Read"
        r = self.hook(json.dumps(event))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNone(self.binding())

    def test_outside_git_tree_does_not_fall(self):
        r = self.hook(bash_event("taskctl show DK-1072", cwd=self.state))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIsNone(self.binding())


class TestCommandParsing(unittest.TestCase):
    def test_task_taken_from_plan_label_tail(self):
        self.assertEqual(tree_mark.task_of("agentctl plan step --label DK-1072-exec «этап»"),
                         "DK-1072")

    def test_task_of_requires_board_tool(self):
        self.assertEqual(tree_mark.task_of("echo DK-1072"), "")

    def test_task_of_takes_first_id(self):
        self.assertEqual(tree_mark.task_of("taskctl dep add DK-100 DK-200"), "DK-100")

    def test_foreign_prefix_counts(self):
        self.assertEqual(tree_mark.task_of("taskctl show XR-42"), "XR-42")


class TestBadInput(unittest.TestCase):
    def test_broken_json_does_not_fall(self):
        r = run("--hook", input="не json")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_unknown_protocol_says_so(self):
        r = run("--hook", "чужой", input="{}")
        self.assertEqual(r.returncode, 2)
        self.assertIn("чужой", r.stderr)

    def test_without_mode_prints_help(self):
        r = run()
        self.assertEqual(r.returncode, 2)
        self.assertIn("привязка", r.stderr.lower())


if __name__ == "__main__":
    unittest.main()
