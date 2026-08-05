#!/usr/bin/env python3
"""Проверка индекса памяти: короткие строки-указатели проходят, жир и проза
ловятся, а где лежит сам индекс, знает профиль харнеса, а не хук.
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-memory.py")
LONG = "x" * 170
OWN_INDEX = "/заметки/ИНДЕКС.md"  # свой хвост пути индекса из профиля


def run(*args, **kw):
    env = dict(os.environ, **kw.pop("env", {}))
    return subprocess.run([sys.executable, TOOL] + list(args), env=env,
                          capture_output=True, text=True, **kw)


def hook_event(path, text):
    return json.dumps({"tool_input": {"file_path": path, "new_string": text}})


class TestFiles(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_clean_index_passes(self):
        path = self.write("mem_ok.md", "- [Запись](file.md) - крючок\n\n"
                                       "- [Вторая](f2.md) - тоже коротко\n")
        self.assertEqual(run(path).returncode, 0)

    def test_fat_index_is_caught(self):
        path = self.write("mem_bad.md", "- [Журнал](file.md) - %s\n"
                                        "проза без указателя\n" % LONG)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("длина", r.stdout)
        self.assertIn("не строка-указатель", r.stdout)


class TestHookMode(unittest.TestCase):
    def test_fat_line_in_index_is_refused(self):
        r = run("--hook", input=hook_event("/a/memory/MEMORY.md",
                                           "- [Журнал](f.md) - %s" % LONG))
        self.assertEqual(r.returncode, 2)

    def test_alien_file_is_left_alone(self):
        r = run("--hook", input=hook_event("/a/b/notes.md", LONG))
        self.assertEqual(r.returncode, 0)


class TestHarnessProfile(unittest.TestCase):
    """Хвост пути индекса берётся из профиля харнеса, а без ключа memory_index
    проверка молчит вовсе: у инструмента без памяти находки про индекс это шум.
    """

    def setUp(self):
        self.own = tempfile.mkdtemp()
        self.none = tempfile.mkdtemp()
        with open(os.path.join(self.own, "claude-code.toml"), "w",
                  encoding="utf-8") as f:
            f.write('[hooks]\nprotocol = "claude-code"\n'
                    'memory_index = "%s"\n' % OWN_INDEX)
        with open(os.path.join(self.none, "claude-code.toml"), "w",
                  encoding="utf-8") as f:
            f.write('[hooks]\nprotocol = "claude-code"\n')

    def test_own_tail_from_profile(self):
        r = run("--hook", env={"DEVKIT_HARNESS_DIR": self.own},
                input=hook_event("/a" + OWN_INDEX, "проза без указателя"))
        self.assertEqual(r.returncode, 2)

    def test_hardcoded_path_is_not_looked_at(self):
        r = run("--hook", env={"DEVKIT_HARNESS_DIR": self.own},
                input=hook_event("/a/memory/MEMORY.md", "проза без указателя"))
        self.assertEqual(r.returncode, 0)

    def test_tool_without_memory_index_is_quiet(self):
        r = run("--hook", env={"DEVKIT_HARNESS_DIR": self.none},
                input=hook_event("/a/memory/MEMORY.md", "проза без указателя"))
        self.assertEqual(r.returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
