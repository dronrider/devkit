#!/usr/bin/env python3
"""Раннер сюиты: перечень классов, счёт тестов и поведение на провале.

Сюита живая слишком тяжела для тестов раннера, поэтому здесь синтетический
каталог с короткими модулями: проверяется сам раннер, а не классы поверх
него. Отдельный модуль с процессом, умирающим без строки итога, держит
рубеж сверки счёта: потерянный тест это находка прогона, а не молчание.
"""
import contextlib
import io
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

import suite

GREEN = '''import unittest


class AlphaTest(unittest.TestCase):
    def test_one(self):
        self.assertTrue(True)

    def test_two(self):
        self.assertEqual(1 + 1, 2)


class BetaTest(unittest.TestCase):
    def test_third(self):
        self.assertIn("a", "abc")
'''

FAILING = '''import unittest


class GammaTest(unittest.TestCase):
    def test_ok(self):
        self.assertTrue(True)

    def test_breaks(self):
        self.fail("ломается нарочно")
'''

# Процесс умирает на тесте, не добравшись до строки «Ran N tests»: счёт этого
# класса раннер читает как ноль, и расхождение с discover обязано стать
# находкой, иначе потерянный тест молчал бы под зелёным итогом.
CRASHING = '''import os
import unittest


class DeltaTest(unittest.TestCase):
    def test_dies(self):
        os._exit(3)
'''


class Stand(unittest.TestCase):
    """Синтетическая сюита в своей директории: файлы на класс теста."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="devkitctl-suite-test-"))

    def tearDown(self):
        shutil.rmtree(str(self.dir), ignore_errors=True)
        # Модули синтетики залипают в sys.modules, и следующий тест ловил бы
        # их из чужой директории: discover сверяет, откуда пришёл модуль.
        for name in [n for n in sys.modules if n.endswith("_test")]:
            del sys.modules[name]

    def grow(self, name, body):
        (self.dir / name).write_text(body, encoding="utf-8")


class EnumerateTest(Stand):

    def test_classes_come_from_the_loader(self):
        self.grow("alpha_test.py", GREEN)
        self.grow("gamma_test.py", FAILING)
        # Порядок отдаёт загрузчик, поэтому сверяются множеством: раннеру
        # важен состав, а не последовательность.
        self.assertEqual(set(suite.enumerate_classes(self.dir)),
                         {"alpha_test.AlphaTest", "alpha_test.BetaTest",
                          "gamma_test.GammaTest"})

    def test_count_matches_the_tests(self):
        self.grow("alpha_test.py", GREEN)
        self.assertEqual(suite.count_discover_tests(self.dir), 3)


class RunClassTest(Stand):

    def test_green_class_counts_its_tests(self):
        self.grow("alpha_test.py", GREEN)
        rc, out, count = suite.run_class("alpha_test.AlphaTest", self.dir)
        self.assertEqual(rc, 0)
        self.assertEqual(count, 2)

    def test_failing_class_keeps_its_output(self):
        self.grow("gamma_test.py", FAILING)
        rc, out, count = suite.run_class("gamma_test.GammaTest", self.dir)
        self.assertNotEqual(rc, 0)
        self.assertEqual(count, 2)
        self.assertIn("AssertionError", out)
        self.assertIn("Ran 2 tests", out)

    def test_died_process_counts_zero(self):
        self.grow("delta_test.py", CRASHING)
        rc, out, count = suite.run_class("delta_test.DeltaTest", self.dir)
        self.assertNotEqual(rc, 0)
        self.assertEqual(count, 0, "погибший без итога процесс обязан терять счёт, "
                                   "чтобы расхождение увидела сверка")


class RunSuiteTest(Stand):

    def test_suite_passes_with_the_same_count(self):
        self.grow("alpha_test.py", GREEN)
        classes = suite.enumerate_classes(self.dir)
        ran, passed, _, results = suite.run_suite(classes, workers=2, cwd=self.dir)
        self.assertTrue(passed)
        self.assertEqual(ran, 3)
        self.assertEqual(set(results), set(classes))

    def test_failing_class_fails_the_suite(self):
        self.grow("alpha_test.py", GREEN)
        self.grow("gamma_test.py", FAILING)
        ran, passed, _, _ = suite.run_suite(suite.enumerate_classes(self.dir),
                                            workers=2, cwd=self.dir)
        self.assertFalse(passed)
        self.assertEqual(ran, 5)

    def test_heavy_classes_go_first(self):
        # Порядок очереди проверяется подменой прогона: раннер запускает
        # тяжёлые классы первыми, и один воркер вынимает их из головы очереди.
        # Вес берётся загрузчиком из живых модулей, поэтому тяжёлым назначен
        # класс этой самой сюиты: он весит больше синтетического.
        order = []
        was = suite.run_class
        try:
            suite.run_class = lambda name, cwd=None: (order.append(name), (0, "", 1))[1]
            suite.run_suite(["alpha_test.AlphaTest", "suite_test.RunSuiteTest",
                             "alpha_test.BetaTest"], workers=1, cwd=self.dir)
        finally:
            suite.run_class = was
        self.assertEqual(order[0], "suite_test.RunSuiteTest",
                         "тяжёлый класс обязан уходить в очередь раньше лёгких")
        self.assertEqual(set(order), {"alpha_test.AlphaTest", "suite_test.RunSuiteTest",
                                      "alpha_test.BetaTest"})


class MainTest(Stand):

    def run_main(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = suite.main(["-j", "2"], start_dir=self.dir)
        return rc, out.getvalue()

    def test_green_run_exits_zero(self):
        self.grow("alpha_test.py", GREEN)
        rc, out = self.run_main()
        self.assertEqual(rc, 0)
        self.assertIn("Ran 3 tests", out)
        self.assertIn("OK", out)

    def test_failure_is_printed_in_full(self):
        self.grow("gamma_test.py", FAILING)
        rc, out = self.run_main()
        self.assertEqual(rc, 1)
        self.assertIn("FAILED", out)
        # Провал печатается целиком: без трейсбука класс в параллельном
        # прогоне не отличить от чужого.
        self.assertIn("AssertionError: ломается нарочно", out)

    def test_lost_count_is_a_finding(self):
        self.grow("delta_test.py", CRASHING)
        rc, out = self.run_main()
        self.assertEqual(rc, 1)
        self.assertIn("счёт обязан совпадать", out)

    def test_list_names_the_classes(self):
        self.grow("alpha_test.py", GREEN)
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = suite.main(["--list"], start_dir=self.dir)
        self.assertEqual(rc, 0)
        self.assertIn("alpha_test.AlphaTest", out.getvalue())
        self.assertIn("классов: 2", out.getvalue())


if __name__ == "__main__":
    unittest.main()
