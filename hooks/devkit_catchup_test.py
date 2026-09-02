#!/usr/bin/env python3
"""SessionStart-хук догоняет чекаут devkit. Обвязка тонкая и зовёт
devkitctl catchup --hook python-частью по пути рядом с собой, поэтому
проверяется она подставным деревом: состав вызова, вывод в stdout, тишина без
python3 и у старого чекаута без подкоманды. Сама команда и её гард проверяются
в tools/devkitctl/catchup_test.py.

Хук на sh по правилу языка там и остаётся, поэтому зовётся он как процесс, а
тест лежит рядом с ним.
"""
import os
import shutil
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "devkit-catchup.sh")

# Системная часть PATH подставная: проверка «python3 нет» иначе держалась бы на
# том, чего нет в /usr/bin именно на этой машине.
SYS_TOOLS = ("sh", "command", "dirname", "pwd")


class TestDevkitCatchup(unittest.TestCase):
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
        # Подставное дерево devkit: хук ищет python-часть по пути от себя, и
        # копия хука тут стоит ровно там, где стоит настоящая.
        self.tree = os.path.join(self.tmp, "devkit")
        os.makedirs(os.path.join(self.tree, "hooks"))
        os.makedirs(os.path.join(self.tree, "tools", "devkitctl"))
        self.hook = os.path.join(self.tree, "hooks", "devkit-catchup.sh")
        shutil.copy(HOOK, self.hook)
        self.cli = os.path.join(self.tree, "tools", "devkitctl", "devkitctl.py")

    def stub(self, name, text):
        path = os.path.join(self.bin, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        os.chmod(path, 0o755)

    def write_cli(self, text):
        with open(self.cli, "w", encoding="utf-8") as f:
            f.write(text)

    def run_hook(self, path=None):
        env = {"HOME": self.home,
               "PATH": path or (self.bin + os.pathsep + self.sys)}
        return subprocess.run(["sh", self.hook], env=env,
                              capture_output=True, text=True)

    def test_calls_catchup_hook_mode(self):
        self.write_cli("")
        self.stub("python3", '#!/bin/sh\nshift\necho "$*" >> "%s"\n' % self.mark)
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        with open(self.mark, encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), "catchup --hook")

    def test_message_reaches_stdout(self):
        # Подтяг и отказ печатаются в stdout: старт сессии хук не ломает, а
        # сессия видит, что чекаут догнан или почему нет.
        self.write_cli("")
        self.stub("python3", '#!/bin/sh\necho "devkit подтянут до abc1234, 3 коммита"\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertIn("devkit подтянут до", done.stdout)

    def test_silent_without_python(self):
        # Хук стоит у всех сессий машины, и ругаться в каждой на отсутствующий
        # python3 он не должен: про нехватку скажет devkitctl doctor.
        self.write_cli("")
        done = self.run_hook(path=self.sys)
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")
        self.assertEqual(done.stderr, "")

    def test_silent_without_python_part(self):
        # Чекаут без python-части (обрезанная копия дерева): звать нечего.
        self.stub("python3", '#!/bin/sh\necho "звать было нечего"\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")

    def test_old_checkout_noise_is_cut(self):
        # У чекаута, снятого до задачи, нет подкоманды catchup, и ругань
        # argparse в stderr не должна шуметь на каждом старте сессии.
        self.write_cli("")
        self.stub("python3", '#!/bin/sh\necho "invalid choice: catchup" >&2\nexit 2\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")
        self.assertEqual(done.stderr, "")

    def test_blank_output_stays_silent(self):
        # Молчаливый чекаут получает от команды пустой вывод, и хук обязан
        # съесть его, а не собирать пустые строки в контекст старта сессии.
        self.write_cli("")
        self.stub("python3", '#!/bin/sh\necho ""\n')
        done = self.run_hook()
        self.assertEqual(done.returncode, 0)
        self.assertEqual(done.stdout, "")
        self.assertEqual(done.stderr, "")


if __name__ == "__main__":
    unittest.main(verbosity=0)
