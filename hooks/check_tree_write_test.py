#!/usr/bin/env python3
"""Проверка сторожа записи мимо дерева задачи (DK-1072): PreToolUse-хук на
записи отбивает правку в чужом дереве того же репозитория, пропускает правку в
своём, сходится с привязкой через симлинк, молчит без привязки и у хода самой
сессии, а в отказе называет оба пути и путь той же правки в своём дереве."""
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-tree-write.py")


def load():
    spec = importlib.util.spec_from_file_location("check_tree_write", TOOL)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, HERE)
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.pop(0)
    return mod


check_tree_write = load()


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


MAIN = "/home/u/proj"
SIDE = "/home/u/proj-dk-1072"


def write_event(path, session="s1", agent="a1", cwd=MAIN, tool="Write"):
    e = {"session_id": session, "hook_event_name": "PreToolUse",
         "tool_name": tool, "tool_input": {"file_path": path, "content": "текст"},
         "cwd": cwd}
    if agent:
        e["agent_id"] = agent
    return json.dumps(e)


class GuardCase(unittest.TestCase):
    def setUp(self):
        self.state = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.state, ignore_errors=True)

    def bind(self, context="s1.a1", task="DK-1072", tree=SIDE, others=(MAIN,)):
        with open(os.path.join(self.state, context + ".json"), "w",
                  encoding="utf-8") as f:
            json.dump({"task": task, "tree": tree, "others": list(others),
                       "ts": 0}, f)

    def hook(self, event):
        return run("--hook", "--state", self.state, input=event)


class TestRefusesStrayWrite(GuardCase):
    def setUp(self):
        super().setUp()
        self.bind()

    def test_write_into_main_checkout_is_refused(self):
        r = self.hook(write_event(MAIN + "/docs/tasks/DK-1072.md"))
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertIn("DK-1072", r.stderr)

    def test_refusal_names_both_paths_and_the_right_one(self):
        r = self.hook(write_event(MAIN + "/docs/tasks/DK-1072.md"))
        self.assertIn(MAIN, r.stderr)
        self.assertIn(SIDE, r.stderr)
        self.assertIn(SIDE + "/docs/tasks/DK-1072.md", r.stderr)

    def test_refusal_ends_with_a_newline(self):
        # Без перевода строки в выводе харнеса за точкой сразу идёт следующая
        # строка (DK-1072).
        r = self.hook(write_event(MAIN + "/docs/tasks/DK-1072.md"))
        self.assertTrue(r.stderr.endswith("\n"), repr(r.stderr[-20:]))

    def test_refusal_names_the_way_back(self):
        # Без способа вернуться отказ оставляет исполнителя догадываться, и
        # промах повторяется тем же относительным путём (DK-1072).
        r = self.hook(write_event(MAIN + "/docs/tasks/DK-1072.md"))
        self.assertIn("-C " + SIDE, r.stderr)

    def test_relative_path_resolved_from_cwd_is_refused(self):
        r = self.hook(write_event("docs/tasks/DK-1072.md"))
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertIn(SIDE + "/docs/tasks/DK-1072.md", r.stderr)

    def test_edit_of_stray_file_is_refused(self):
        r = self.hook(write_event(MAIN + "/hooks/hookio.py", tool="Edit"))
        self.assertEqual(r.returncode, 2, r.stdout)

    def test_notebook_path_is_refused_too(self):
        event = json.loads(write_event(MAIN + "/x.ipynb", tool="NotebookEdit"))
        event["tool_input"] = {"notebook_path": MAIN + "/x.ipynb"}
        r = self.hook(json.dumps(event))
        self.assertEqual(r.returncode, 2, r.stdout)

    def test_write_into_third_tree_is_refused(self):
        self.bind(others=(MAIN, "/home/u/proj-dk-369"))
        r = self.hook(write_event("/home/u/proj-dk-369/README.md"))
        self.assertEqual(r.returncode, 2, r.stdout)


class TestLetsOwnWorkThrough(GuardCase):
    def setUp(self):
        super().setUp()
        self.bind()

    def test_write_into_own_tree_passes(self):
        r = self.hook(write_event(SIDE + "/docs/tasks/DK-1072.md"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_write_into_own_tree_root_passes(self):
        r = self.hook(write_event(SIDE))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_file_outside_every_tree_passes(self):
        r = self.hook(write_event("/tmp/черновик.txt"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_path_next_to_the_tree_is_not_inside_it(self):
        r = self.hook(write_event("/home/u/proj-dk-10720/README.md"))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestQuietWithoutBinding(GuardCase):
    def test_without_binding_write_passes(self):
        r = self.hook(write_event(MAIN + "/README.md"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_session_turn_is_not_guarded(self):
        self.bind(context="s1")
        r = self.hook(write_event(MAIN + "/README.md", agent=None))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_binding_of_another_agent_is_not_taken(self):
        self.bind(context="s1.a2")
        r = self.hook(write_event(MAIN + "/README.md"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_binding_without_tree_is_ignored(self):
        with open(os.path.join(self.state, "s1.a1.json"), "w", encoding="utf-8") as f:
            json.dump({"task": "DK-1072", "ts": 0}, f)
        r = self.hook(write_event(MAIN + "/README.md"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_broken_binding_file_is_ignored(self):
        with open(os.path.join(self.state, "s1.a1.json"), "w", encoding="utf-8") as f:
            f.write("не json")
        r = self.hook(write_event(MAIN + "/README.md"))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestSymlinkedRoots(GuardCase):
    """Стык двух хуков: привязку кладёт tree-mark путями от `git worktree
    list`, где симлинк уже раскрыт, а путь правки приходит таким, каким его
    написал агент. На macOS так расходятся `/private/tmp` и `/tmp`."""

    def setUp(self):
        super().setUp()
        self.home = tempfile.mkdtemp()
        self.real = os.path.join(os.path.realpath(self.home), "real")
        for name in ("proj", "proj-dk-1072"):
            os.makedirs(os.path.join(self.real, name, "docs"))
        self.link = os.path.join(self.home, "link")
        os.symlink(self.real, self.link)
        self.bind(tree=os.path.join(self.real, "proj-dk-1072"),
                  others=(os.path.join(self.real, "proj"),))

    def tearDown(self):
        shutil.rmtree(self.home, ignore_errors=True)
        super().tearDown()

    def test_stray_write_through_a_symlink_is_refused(self):
        stray = os.path.join(self.link, "proj", "docs", "notes.md")
        r = self.hook(write_event(stray, cwd=os.path.join(self.link, "proj")))
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertIn(os.path.join(self.real, "proj-dk-1072", "docs", "notes.md"),
                      r.stderr)

    def test_own_write_through_a_symlink_passes(self):
        own = os.path.join(self.link, "proj-dk-1072", "docs", "notes.md")
        r = self.hook(write_event(own, cwd=os.path.join(self.link, "proj-dk-1072")))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestPathMath(unittest.TestCase):
    def test_inside_counts_the_root_itself(self):
        self.assertTrue(check_tree_write.inside(SIDE, SIDE))

    def test_inside_does_not_count_a_sibling_prefix(self):
        self.assertFalse(check_tree_write.inside(SIDE + "0/x", SIDE))

    def test_inside_with_empty_root_is_false(self):
        self.assertFalse(check_tree_write.inside(SIDE, ""))

    def test_real_file_keeps_a_name_that_does_not_exist_yet(self):
        # Файла правки на диске может ещё не быть, и раскрывается только его
        # каталог (DK-1072).
        d = tempfile.mkdtemp()
        try:
            self.assertEqual(check_tree_write.real_file(os.path.join(d, "нового.md")),
                             os.path.join(os.path.realpath(d), "нового.md"))
        finally:
            shutil.rmtree(d, ignore_errors=True)


class TestBadInput(unittest.TestCase):
    def test_broken_json_does_not_fall(self):
        r = run("--hook", input="не json")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_event_without_write_passes(self):
        r = run("--hook", input=json.dumps(
            {"session_id": "s1", "agent_id": "a1", "hook_event_name": "PreToolUse",
             "tool_name": "Bash", "tool_input": {"command": "ls"}}))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_unknown_protocol_says_so(self):
        r = run("--hook", "чужой", input="{}")
        self.assertEqual(r.returncode, 2)
        self.assertIn("чужой", r.stderr)

    def test_without_mode_prints_help(self):
        r = run()
        self.assertEqual(r.returncode, 2)
        self.assertIn("сторож", r.stderr.lower())


if __name__ == "__main__":
    unittest.main()
