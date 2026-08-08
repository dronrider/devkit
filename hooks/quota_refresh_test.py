#!/usr/bin/env python3
"""Отцепленный съём снимка квоты на старте сессии. Настоящие tmux и claude тут
не поднимаются, вместо них заглушки: проверяется обвязка хука (условия запуска,
замок, журнал), а не съём панели, у него свои тесты.

Хук на sh по правилу языка там и остаётся, поэтому зовётся он как процесс, а
тест лежит рядом с ним.
"""
import os
import shutil
import subprocess
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "quota-refresh.sh")

# Системная часть PATH подставная: проверка «инструментов нет» иначе держалась
# бы на том, чего нет в /usr/bin именно на этой машине.
SYS_TOOLS = ("sh", "dirname", "mkdir", "rmdir", "date", "find", "sleep")


def awaited(path, timeout=10.0):
    """Дождаться файла, который пишет фоновый процесс."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if os.path.exists(path) and os.path.getsize(path):
            return True
        time.sleep(0.05)
    return False


def read(path):
    if not os.path.exists(path):
        return ""
    with open(path, encoding="utf-8") as f:
        return f.read()


class TestQuotaRefresh(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.mark = os.path.join(self.tmp, "refresh.mark")
        self.home = os.path.join(self.tmp, "home")
        self.lock = os.path.join(self.home, ".devkit", "quota-refresh.lock")
        self.log = os.path.join(self.home, ".devkit", "quota-refresh.log")
        os.makedirs(self.home)
        self.sys = self.stub_dir("qsys")
        for tool in SYS_TOOLS:
            found = shutil.which(tool)
            if found:
                os.symlink(found, os.path.join(self.sys, tool))
        self.bin = self.stub_dir("qbin")
        for tool in ("tmux", "claude"):
            self.stub(tool, "#!/bin/sh\nexit 0\n")
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n' % self.mark)

    def stub_dir(self, name):
        path = os.path.join(self.tmp, name)
        os.makedirs(path)
        return path

    def stub(self, name, text):
        path = os.path.join(self.bin, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        os.chmod(path, 0o755)

    def run_hook(self, home=None, path=None):
        env = {"HOME": home or self.home,
               "PATH": path or (self.bin + os.pathsep + self.sys)}
        return subprocess.run(["sh", HOOK], env=env,
                              capture_output=True, text=True).returncode

    def test_snapshot_is_taken_and_logged(self):
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(awaited(self.mark), "панель не снималась вовсе")
        # Свежий снимок хук не переснимает: порог свежести живёт в agentctl.
        self.assertIn("--if-stale", read(self.mark))
        self.assertTrue(awaited(self.log), "журнала последнего запуска нет")
        self.assertIn("код возврата", read(self.log))

    def test_taken_lock_stops_the_second_run(self):
        # Иначе claude, поднятый в tmux, своим стартом дёрнул бы этот же хук и
        # увёл бы сессии в воронку.
        os.makedirs(self.lock)
        self.assertEqual(self.run_hook(), 0)
        time.sleep(1)
        self.assertEqual(read(self.mark), "")

    def test_harness_without_declared_quota_stays_quiet(self):
        # Вторая подписка едет с пустой секцией [quota], refresh на ней честно
        # отказывает, и журнал из одних отказов перестал бы отвечать на вопрос,
        # ради которого заведён.
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "у харнеса glm-code секция [quota] пуста, '
                              'снимать остаток нечем" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(awaited(self.mark), "хук даже не позвал refresh")
        time.sleep(0.5)
        self.assertEqual(read(self.log), "", "отказ харнеса без квоты попал в журнал")

    def test_a_real_refusal_still_reaches_the_journal(self):
        # Молчание узкое: сюда попадает только харнес без объявленной квоты, а
        # незалогиненный клиент или неузнанная панель остаются в журнале.
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "панель /usage не узналась" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(awaited(self.log), "журнала последнего запуска нет")
        self.assertIn("панель /usage не узналась", read(self.log))
        self.assertIn("код возврата: 1", read(self.log))

    def test_nothing_to_take_a_snapshot_with(self):
        # Уходим молча и следов не оставляем, ругаться на это дело devkitctl
        # doctor, а не каждой сессии.
        bare = os.path.join(self.tmp, "nohome")
        os.makedirs(bare)
        self.assertEqual(self.run_hook(home=bare, path=self.sys), 0)
        self.assertFalse(os.path.isdir(os.path.join(bare, ".devkit")))


if __name__ == "__main__":
    unittest.main(verbosity=0)
