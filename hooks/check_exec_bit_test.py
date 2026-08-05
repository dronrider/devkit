#!/usr/bin/env python3
"""Проверка бита исполнения: test.sh без бита x в индексе это находка, а
посторонний .sh рядом молчит. Отдельно проверяется поведение на чужой
директории: чистая диагностика одной строкой, а не traceback (DK-072).
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-exec-bit.py")


def run(*args):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True)


class TestExecBit(unittest.TestCase):
    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)
        os.makedirs(os.path.join(self.repo, "pkg"))

    def commit(self, name, text, executable=False):
        path = os.path.join(self.repo, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        if executable:
            os.chmod(path, 0o755)
        subprocess.run(["git", "-C", self.repo, "add", name], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", name], check=True)

    def test_nested_test_sh_without_bit(self):
        self.commit("pkg/test.sh", "#!/bin/sh\necho ok\n")
        r = run("-C", self.repo)
        self.assertEqual(r.returncode, 1)
        self.assertTrue(r.stdout.startswith("pkg/test.sh: режим 100644"), r.stdout)

    def test_executable_test_sh_is_quiet(self):
        self.commit("pkg/test.sh", "#!/bin/sh\necho ok\n", executable=True)
        self.assertEqual(run("-C", self.repo).returncode, 0)

    def test_alien_sh_without_bit_is_quiet(self):
        self.commit("pkg/other.sh", "echo other\n")
        self.assertEqual(run("-C", self.repo).returncode, 0)

    def test_test_sh_in_root_without_bit(self):
        self.commit("test.sh", "#!/bin/sh\necho ok\n")
        self.assertEqual(run("-C", self.repo).returncode, 1)


class TestBadDir(unittest.TestCase):
    """Не git-репозиторий и несуществующий -C дают код 2 и внятную строку, а не
    traceback."""

    def setUp(self):
        self.notrepo = tempfile.mkdtemp()

    def check(self, path):
        r = run("-C", path)
        self.assertEqual(r.returncode, 2)
        out = r.stdout + r.stderr
        self.assertNotIn("raceback", out)
        self.assertTrue(out.startswith("check-exec-bit: "), out)

    def test_not_a_repo(self):
        self.check(self.notrepo)

    def test_missing_dir(self):
        self.check(os.path.join(self.notrepo, "nope"))


if __name__ == "__main__":
    unittest.main(verbosity=0)
