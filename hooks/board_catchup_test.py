#!/usr/bin/env python3
"""SessionStart-хук догоняет боковое дерево доски. Обвязка хука тонкая и
зовёт taskctl catchup --hook, поэтому проверяется она заглушкой taskctl:
состав вызова, тишина без taskctl и у старого бинаря, вывод в stdout.
Сама команда и её гард проверяются go-тестами в tools/taskctl.

Хук на sh по правилу языка там и остаётся, поэтому зовётся он как процесс, а
тест лежит рядом с ним.
"""
import os
import shutil
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "board-catchup.sh")

# Системная часть PATH подставная: проверка «инструментов нет» иначе держалась
# бы на том, чего нет в /usr/bin именно на этой машине.
SYS_TOOLS = ("sh", "command", "dirname")


class TestBoardCatchup(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.home = os.path.join(self.tmp, "home")
        os.makedirs(self.home)
        self.mark = os.path.join(self.tmp, "catchup.mark")
        self.sys = os.path.join(self.tmp, "sys")
        os.makedirs(self.sys)
        for tool in SYS_TOOLS:
            found = shutil.which(tool)
            if found:
                os.symlink(found, os.path.join(self.sys, tool))
        self.bin = os.path.join(self.tmp, "bin")
        os.makedirs(self.bin)

    def stub(self, name, text):
        path = os.path.join(self.bin, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        os.chmod(path, 0o755)

    def run_hook(self, path=None):
        env = {"HOME": self.home,
               "PATH": path or (self.bin + os.pathsep + self.sys)}
        return subprocess.run(["sh", HOOK], env=env,
                              capture_output=True, text=True)

    def test_calls_catchup_hook_mode(self):
        self.stub("taskctl", '#!/bin/sh\necho "$*" >> "%s"\n' % self.mark)
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        with open(self.mark, encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), "catchup --hook")

    def test_taskctl_message_reaches_stdout(self):
        # Подтяг и отказ печатаются в stdout: старт сессии хук не ломает, а
        # сессия видит, что дерево догнано или почему нет.
        self.stub("taskctl", '#!/bin/sh\necho "дерево догнано до abc1234"\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertIn("дерево догнано", done.stdout)

    def test_silent_without_taskctl(self):
        # Хук стоит у всех сессий машины, и ругаться в каждой на отсутствующий
        # taskctl он не должен: про нехватку скажет devkitctl doctor.
        done = self.run_hook(path=self.sys)
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")
        self.assertEqual(done.stderr, "")

    def test_old_binary_noise_is_cut(self):
        # У бинаря, собранного до задачи, нет catchup, и его ругань в stderr
        # не должна шуметь на каждом старте сессии до обновления.
        self.stub("taskctl", '#!/bin/sh\necho "неизвестная команда catchup" >&2\nexit 2\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")
        self.assertEqual(done.stderr, "")

    def test_blank_output_stays_silent(self):
        # Молчаливые деревья получают от утилиты пустую строку (она печатает
        # сообщение даже пустое), и хук обязан съесть её, а не собирать
        # пустые строки в контекст старта сессии.
        self.stub("taskctl", '#!/bin/sh\necho ""\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")
        self.assertEqual(done.stderr, "")


if __name__ == "__main__":
    unittest.main(verbosity=0)
