#!/usr/bin/env python3
"""Стенд для docs/tasks/DK-1168-spawns.sh.

Сам сценарий по правилу раскладки (README.md, «Раскладка», DK-139) остаётся в
`docs/tasks/`, а python это код, не материал: доктор
(`tools/devkitctl/layout.py`) метит `.py` под `docs/` находкой MATERIAL, поэтому
стенд лежит здесь и ссылается на `.sh` путём вверх по дереву.

Настоящий прогон считает подпроцессы git у выкаченного `taskctl` на живой
доске. Тест подменяет `taskctl` в голове PATH стендовым скриптом с известным
числом вызовов git: так проверяется сама механика счёта (подставной git на PATH,
разбор журнала, потолок, код возврата), а не число, которое даёт живая утилита.
"""
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent.parent / "docs" / "tasks" / "DK-1168-spawns.sh"


class SpawnCountTest(unittest.TestCase):

    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="dk1168-")
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)
        self.bin = Path(self.dir) / "bin"
        self.bin.mkdir()
        self.root = Path(self.dir) / "root"
        self.root.mkdir()

    def _taskctl(self, calls):
        """Подставной taskctl: зовёт git ровно calls раз и печатает доску."""
        path = self.bin / "taskctl"
        path.write_text(
            "#!/bin/sh\n"
            "i=0\n"
            "while [ $i -lt %d ]; do git --version >/dev/null; i=$((i + 1)); done\n"
            'printf \'{"sections":[{"rows":[{"id":"XR-001"}]}]}\\n\'\n' % calls)
        path.chmod(0o755)

    def _run(self, limit):
        env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"])
        return subprocess.run(
            [str(SCRIPT), str(limit), str(self.root)],
            capture_output=True, text=True, env=env)

    def test_under_limit_is_ok(self):
        """Вызовов меньше потолка: сценарий зелёный и печатает число."""
        self._taskctl(4)
        got = self._run(12)
        self.assertEqual(got.returncode, 0, got.stdout + got.stderr)
        self.assertIn("подпроцессов git: 4", got.stdout)
        self.assertIn("OK", got.stdout)

    def test_over_limit_fails(self):
        """Вызовов больше потолка: сценарий красный и называет оба числа.

        Это и есть предмет DoD DK-1168: до правки обход доски поднимал 87
        подпроцессов git при потолке 12.
        """
        self._taskctl(20)
        got = self._run(12)
        self.assertEqual(got.returncode, 1, got.stdout + got.stderr)
        self.assertIn("подпроцессов git: 20", got.stdout)
        self.assertIn("FAILED: 20 подпроцессов git при потолке 12", got.stdout)

    def test_counts_rows_of_board(self):
        """Число строк доски печатается рядом с числом вызовов. По нему видно,
        что потолок взят не пустой доской."""
        self._taskctl(1)
        got = self._run(12)
        self.assertIn("строк доски: 1", got.stdout)

    def test_real_git_still_works(self):
        """Подставной git отдаёт работу настоящему, а не изображает её.

        Иначе счёт шёл бы по команде, которая ничего не делает, и сценарий
        зеленел бы на сломанном taskctl.
        """
        path = self.bin / "taskctl"
        path.write_text(
            "#!/bin/sh\n"
            "git --version > \"$(dirname \"$0\")/../seen.txt\"\n"
            'printf \'{"sections":[]}\\n\'\n')
        path.chmod(0o755)
        got = self._run(12)
        self.assertEqual(got.returncode, 0, got.stdout + got.stderr)
        seen = (Path(self.dir) / "seen.txt").read_text()
        self.assertIn("git version", seen)


if __name__ == "__main__":
    unittest.main()
