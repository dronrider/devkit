"""Самопроверка признания python-стенда замером стенного времени (DK-1230).

Хелпера `loadmark.wall_clock` не было никогда: до этой правки декорированный
тест падает импортом (модуля нет), а не сравнением строк, и это ровно то
падение на старом коде, которого просит DoD. Ниже сверяется собственно приём:
декорированное падение несёт признание в своём тексте, недекорированное того
же падения (то, каким его видел бы python без этой правки) признания не
несёт, а сообщение автора и цепочка причины у декорированного падения
сохраняются.
"""
import os
import re
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import loadmark

ROOT = os.path.dirname(os.path.dirname(HERE))


class TestWallClockDecorator(unittest.TestCase):
    def test_decorated_failure_carries_the_mark(self):
        @loadmark.wall_clock
        def failing():
            raise AssertionError("30 секунд не хватило")
        with self.assertRaises(AssertionError) as ctx:
            failing()
        self.assertIn(loadmark.MARK, str(ctx.exception))

    def test_bare_failure_has_no_mark(self):
        # Тот же провал без декоратора: признания нет, и это то, чем стенды
        # болели до правки (DK-1230, «Что происходит»).
        def failing():
            raise AssertionError("30 секунд не хватило")
        with self.assertRaises(AssertionError) as ctx:
            failing()
        self.assertNotIn(loadmark.MARK, str(ctx.exception))

    def test_original_message_survives(self):
        @loadmark.wall_clock
        def failing():
            raise AssertionError("неприбранный процесс считается живым")
        with self.assertRaises(AssertionError) as ctx:
            failing()
        self.assertIn("неприбранный процесс считается живым", str(ctx.exception))

    def test_cause_chain_is_kept(self):
        @loadmark.wall_clock
        def failing():
            raise AssertionError("причина")
        with self.assertRaises(AssertionError) as ctx:
            failing()
        self.assertIsInstance(ctx.exception.__cause__, AssertionError)

    def test_non_assertion_exception_also_gets_the_mark(self):
        # Срок подпроцесса валит тест TimeoutError, не AssertionError:
        # признание обязано дойти и до него, а голова провала в отчёте
        # unittest остаётся уравненной под AssertionError.
        @loadmark.wall_clock
        def failing():
            raise TimeoutError("подпроцесс не ответил за 5 с")
        with self.assertRaises(AssertionError) as ctx:
            failing()
        self.assertIn(loadmark.MARK, str(ctx.exception))

    def test_passing_call_is_untouched(self):
        @loadmark.wall_clock
        def ok():
            return 42
        self.assertEqual(ok(), 42)

    def test_skip_test_is_not_recognized_as_load(self):
        # unittest.SkipTest это подкласс Exception, и до этой правки
        # декоратор ловил его наравне с настоящим падением: пропущенный тест
        # становился красным нагрузочным падением (замечание ревью круга 1).
        @loadmark.wall_clock
        def skipping():
            raise unittest.SkipTest("не на этой машине")
        with self.assertRaises(unittest.SkipTest) as ctx:
            skipping()
        self.assertNotIn(loadmark.MARK, str(ctx.exception))

    def test_mark_matches_go_constant(self):
        # Строка держится вровень с go-стороной руками: разъехавшиеся строки
        # ловит именно эта сверка литерала.
        path = os.path.join(ROOT, "internal", "loadfail", "loadfail.go")
        with open(path, encoding="utf-8") as f:
            src = f.read()
        m = re.search(r'const Mark = "([^"]+)"', src)
        self.assertIsNotNone(m, "литерал Mark не найден в loadfail.go")
        self.assertEqual(loadmark.MARK, m.group(1))


if __name__ == "__main__":
    unittest.main()
