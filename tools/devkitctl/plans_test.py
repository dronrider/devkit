#!/usr/bin/env python3
"""Набор шаблонов плана глазами доктора: разбор встроенных файлов и сверка
проектной копии со встроенным шаблоном того же имени.
"""
import shutil
import tempfile
import unittest
from pathlib import Path

import plans
from testenv import DEVKIT_SRC, write

KIT = DEVKIT_SRC / "kit" / "plans"

BASE = ('title = "Встроенный"\n[work]\ntitle = "разработка"\nby = "сам"\n'
        'trace = "слово"\n[tests]\ntitle = "тесты"\nby = "сам"\ntrace = "tests"\n'
        'gate = ["merge"]\n')


class BuiltinTemplatesTest(unittest.TestCase):
    """Встроенные шаблоны читаются тем же разбором, что и на стороне Go.
    Разъедься две реализации, и доктор молчал бы на файле, на котором утилита
    встаёт.
    """

    def test_builtin_templates_parse(self):
        names = sorted(p.stem for p in KIT.glob("*.toml"))
        self.assertEqual(names, ["bug", "lld", "poc", "task"],
                         "набор встроенных шаблонов сменился: %s" % names)
        task = plans.read_template(str(KIT / "task.toml"))
        self.assertEqual(len(task["stages"]), 13,
                         "у task тринадцать этапов живого материала, вижу %d" % len(task["stages"]))
        self.assertEqual(task["stages"][0][0], "plan")
        poc = plans.read_template(str(KIT / "poc.toml"))
        self.assertIn("tests", poc["dropped"], "у poc тесты сняты записью с причиной")

    def test_builtin_set_without_project_layer_is_clean(self):
        self.assertEqual(plans.check_plans(str(KIT), "/несуществующий/слой"), [],
                         "проект без своего слоя живёт на встроенном наборе")


class ProjectLayerTest(unittest.TestCase):
    """Копия проекта сверяется со встроенным файлом того же имени: встроенный
    этап обязан быть в ней либо секцией, либо снятым с причиной.
    """

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="plans-test-"))
        self.kit = self.root / "kit"
        self.proj = self.root / "proj"
        self.kit.mkdir()
        self.proj.mkdir()
        write(self.kit / "task.toml", BASE)

    def tearDown(self):
        shutil.rmtree(str(self.root), ignore_errors=True)

    def test_stale_copy_is_a_finding(self):
        write(self.proj / "task.toml",
              'title = "Копия"\n[work]\ntitle = "разработка"\nby = "сам"\ntrace = "слово"\n')
        got = plans.check_plans(str(self.kit), str(self.proj))
        self.assertEqual(len(got), 1, "жду одну находку, вижу %s" % got)
        self.assertIn("[tests]", got[0])

    def test_dropped_with_reason_passes(self):
        write(self.proj / "task.toml",
              'title = "Копия"\ndropped = ["tests: у проекта нет кода, документация правится ревью"]\n'
              '[work]\ntitle = "разработка"\nby = "сам"\ntrace = "слово"\n')
        self.assertEqual(plans.check_plans(str(self.kit), str(self.proj)), [],
                         "снятие с причиной проходит доктора")

    def test_dropped_without_reason_is_a_finding(self):
        write(self.proj / "task.toml",
              'title = "Копия"\ndropped = ["tests"]\n'
              '[work]\ntitle = "разработка"\nby = "сам"\ntrace = "слово"\n')
        got = plans.check_plans(str(self.kit), str(self.proj))
        self.assertEqual(len(got), 1, "жду одну находку, вижу %s" % got)
        self.assertIn("без причины", got[0])

    def test_broken_template_is_a_finding_not_a_crash(self):
        write(self.proj / "task.toml", 'title = "Копия"\n[work]\ntitle = "разработка"\nby = "робот"\ntrace = "слово"\n')
        got = plans.check_plans(str(self.kit), str(self.proj))
        self.assertTrue(got and "битый" in got[0], "битый шаблон это находка, вижу %s" % got)

    def test_own_template_of_project_is_not_compared(self):
        write(self.proj / "security.toml",
              'title = "Разбор безопасности"\n[audit]\ntitle = "аудит"\nby = "субагент"\ntrace = "слово"\n')
        self.assertEqual(plans.check_plans(str(self.kit), str(self.proj)), [],
                         "свой шаблон проекта сверять не с чем")


if __name__ == "__main__":
    unittest.main()
