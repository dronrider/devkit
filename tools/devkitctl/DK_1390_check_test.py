#!/usr/bin/env python3
"""Стенд для docs/tasks/archive/2026/DK-1390-check.sh.

Сценарий проверки задачи по раскладке (README.md, «Раскладка», DK-139)
остаётся в `docs/tasks/`, а python это код, не материал, и доктор метит
`.py` под `docs/` находкой MATERIAL: стенд лежит здесь, рядом с
devkitctl.py, и на сам сценарий ссылается путём вверх по дереву.

Сценарий самодостаточен: строит синтетический дом под `$HOME`, читает
слепок путей и служебную реплику из кода дерева и зовёт devkitctl.py из
него же. Внешних зависимостей нет, поэтому стенд гонит сценарий целиком
по-настоящему в песочнице с чистым HOME и судит по коду возврата и выводу:
на чистом доме сценарий зеленеет, а дом с заранее положенным стоп-краном
ломает первый шаг, потому что решения продлевать уже нет.
"""
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent.parent / "docs" / "tasks" / "archive" / "2026" / "DK-1390-check.sh"
ROOT = Path(__file__).resolve().parent.parent.parent


class CheckScriptTest(unittest.TestCase):
    """Прогон сценария целиком: исход говорит громче разбора вывода."""

    def run_script(self, crane=False, harness=None):
        home = tempfile.mkdtemp(prefix="dk-1390-check-test-")
        self.addCleanup(shutil.rmtree, home, True)
        if crane:
            devkit = Path(home) / ".devkit"
            devkit.mkdir()
            (devkit / "watch.local").write_text("cachekeep = off\n", encoding="utf-8")
        env = dict(os.environ)
        env["HOME"] = home
        # Наследованные указатели харнеса ломали бы изоляцию прогона.
        for key in list(env):
            if key.startswith("DEVKIT_"):
                del env[key]
        if harness is not None:
            env["DEVKIT_HARNESS"] = harness
        return subprocess.run(["sh", str(SCRIPT)], cwd=str(ROOT), env=env,
                              capture_output=True, text=True, timeout=120)

    def test_green_on_clean_home(self):
        p = self.run_script()
        self.assertEqual(p.returncode, 0,
                         "сценарий красный на чистом доме:\n%s%s" % (p.stdout, p.stderr))
        self.assertIn("продлено 1", p.stdout, "без крана решение продлевать есть")
        self.assertIn("продлено 0", p.stdout, "со стоп-краном продлений ноль")
        self.assertIn("398.5K", p.stdout, "drain считает дубли одним запросом")
        self.assertIn("OK", p.stdout)

    def test_red_when_crane_planted_before_the_run(self):
        p = self.run_script(crane=True)
        self.assertNotEqual(p.returncode, 0,
                            "сценарий зеленеет при заранее выключенном продлении:\n%s%s"
                            % (p.stdout, p.stderr))
        self.assertIn("FAILED", p.stdout)

    def test_green_with_inherited_harness_pointer(self):
        # Обкатка наследует среду поднявшей её сессии, и указатель харнеса
        # сужал обход cachekeep до одного дома, мимо сток-дома с
        # синтетическим потоком. Сценарий снимает указатель сам и зеленеет
        # в любой среде.
        p = self.run_script(harness="glm-code")
        self.assertEqual(p.returncode, 0,
                         "сценарий красный при унаследованном DEVKIT_HARNESS:\n%s%s"
                         % (p.stdout, p.stderr))
        self.assertIn("OK", p.stdout)


if __name__ == "__main__":
    unittest.main()
