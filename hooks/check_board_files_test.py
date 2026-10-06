#!/usr/bin/env python3
"""Критерий доски подкаталога (DK-796): матрица путей и код выхода.

Матрица зеркалит TestBoardOnlyAtCorpNested (internal/merged) и прогоны
TestBoardGate (pre_push_test.py), чтобы копии критерия не разъехались по
набору путей.
"""
import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-board-files.py")

_spec = importlib.util.spec_from_file_location("check_board_files", TOOL)
mod = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mod)


class TestIsBoardFile(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = self.tmp.name
        for p in (
            "authn/docs/TASKS.md",
            "authn/docs/tasks/AU-001.md",
            "authn/.devkit/tracker.local",
            "cap_autotests/docs/TASKS.md",
            "tools/obeycheck/testdata/project/docs/TASKS.md",
        ):
            path = os.path.join(self.root, p)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w") as f:
                f.write("x\n")

    def test_matrix(self):
        cases = {
            "docs/TASKS.md": True,
            "docs/TASKS-archive.md": True,
            "docs/tasks/DK-001.md": True,
            "authn/docs/TASKS.md": True,
            "authn/docs/tasks/AU-001.md": True,
            "authn/docs/TASKS-archive.md": True,
            "authn/src/main.go": False,
            "tools/obeycheck/testdata/project/docs/TASKS.md": False,
            "cap_autotests/docs/TASKS.md": False,
            "docs/TASKS.md.bak": False,
        }
        for f, want in cases.items():
            self.assertEqual(mod.is_board_file(self.root, f), want, f)

    def run_cli(self, text):
        return subprocess.run([sys.executable, TOOL], input=text, cwd=self.root,
                              capture_output=True, text=True)

    def test_cli_all_board_passes(self):
        r = self.run_cli("authn/docs/TASKS.md\nauthn/docs/tasks/AU-001.md\n")
        self.assertEqual(r.returncode, 0)

    def test_cli_code_path_refused(self):
        r = self.run_cli("authn/docs/TASKS.md\nauthn/src/main.go\n")
        self.assertEqual(r.returncode, 1)

    def test_cli_empty_input_passes(self):
        self.assertEqual(self.run_cli("").returncode, 0)


if __name__ == "__main__":
    unittest.main()
