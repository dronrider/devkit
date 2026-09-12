#!/usr/bin/env python3
"""Уборка временных деревьев, брошенных упавшим прогоном (DK-968).

Репозиторий тут настоящий, а дерево заводится тем же `git worktree add`, каким
его заводит прогон. Находка ценна тем, что после уборки запись пропадает из
`git worktree list`, и проверить это подделкой нечем.
"""
import os
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

import runtrees
from testenv import git, write


class RunTreesCase(unittest.TestCase):

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.tmp)]))
        self.repo = self.tmp / "repo"
        self.repo.mkdir()
        git(self.repo, "init", "-q", "-b", "main")
        git(self.repo, "config", "user.name", "t")
        git(self.repo, "config", "user.email", "t@t")
        write(self.repo / "file.txt", "первая строка\n")
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-qm", "seed")
        # Каталог временных файлов у теста свой. Уборка не должна ходить по
        # машине, на которой идёт прогон.
        self.temp = self.tmp / "T"
        self.temp.mkdir()

    def run_dir(self, name, pid, alive_tree=True):
        """Каталог прогона с деревом и меткой владельца."""
        d = self.temp / name
        d.mkdir()
        tree = d / "tree"
        if alive_tree:
            git(self.repo, "worktree", "add", "-q", "--detach", str(tree), "main")
        write(d / runtrees.OWNER, "pid = %s\nroot = %s\ntree = %s\n"
              % (pid, self.repo, tree))
        return d

    def trees(self):
        out = subprocess.run(["git", "-C", str(self.repo), "worktree", "list"],
                             capture_output=True, text=True).stdout
        return [ln for ln in out.strip().splitlines() if "/tree" in ln]

    def dead_pid(self):
        """Номер вышедшего процесса: он заведомо не занят живым."""
        p = subprocess.Popen(["true"])
        p.wait()
        return p.pid

    def test_dead_owner_tree_is_swept(self):
        d = self.run_dir("shipctl-merge-1", self.dead_pid())
        self.assertEqual(len(self.trees()), 1)
        findings, fixed = runtrees.check(False, root=self.temp)
        self.assertTrue(findings and "1 дерево" in findings[0], findings)
        self.assertFalse(fixed)
        findings, fixed = runtrees.check(True, root=self.temp)
        self.assertFalse(findings)
        self.assertTrue(fixed and "1 дерево" in fixed[0], fixed)
        self.assertEqual(self.trees(), [])
        self.assertFalse(d.exists())

    def test_live_owner_is_left_alone(self):
        self.run_dir("regcheck-2", os.getpid())
        findings, fixed = runtrees.check(True, root=self.temp)
        self.assertFalse(findings)
        self.assertFalse(fixed)
        self.assertEqual(len(self.trees()), 1)

    def test_alien_temp_dir_is_not_touched(self):
        alien = self.temp / "com.apple.something"
        alien.mkdir()
        write(alien / "payload", "чужое\n")
        runtrees.check(True, root=self.temp)
        self.assertTrue(alien.exists())

    def test_stale_record_is_pruned(self):
        """Каталог без метки унёс с собой и запись. Из каталога не видно,
        чей это репозиторий, и висячую запись снимает уборка по подсказке."""
        d = self.temp / "regcheck-4"
        d.mkdir()
        git(self.repo, "worktree", "add", "-q", "--detach", str(d / "tree"), "main")
        later = time.time() + runtrees.NO_OWNER_AGE + 60
        findings, _ = runtrees.check(False, root=self.temp, now=later, repo=self.repo)
        self.assertTrue(findings and "1 дерево" in findings[0], findings)
        runtrees.check(True, root=self.temp, now=later, repo=self.repo)
        self.assertEqual(self.trees(), [])
        self.assertEqual(runtrees.prunable(self.repo), 0)

    def test_dir_without_owner_waits_for_age(self):
        old = self.temp / "taskctl-rehearse-3"
        old.mkdir()
        write(old / "note", "каталог от сборки до метки\n")
        self.assertEqual(runtrees.abandoned(self.temp), [])
        later = time.time() + runtrees.NO_OWNER_AGE + 60
        self.assertEqual(len(runtrees.abandoned(self.temp, now=later)), 1)
        runtrees.check(True, root=self.temp, now=later)
        self.assertFalse(old.exists())


if __name__ == "__main__":
    unittest.main()
