#!/usr/bin/env python3
"""Слова отчёта: падеж после числа и свёртка однотипного в строку."""
import unittest

import say

SKILL = ("скилл", "скилла", "скиллов")


class CountedTest(unittest.TestCase):
    """Ожидание тут выписано руками, а не собрано тем же правилом: правило и
    проверяется, и повтор его в тесте проверял бы сам себя.
    """

    def test_three_forms(self):
        for n, want in ((1, "1 скилл"), (2, "2 скилла"), (4, "4 скилла"), (5, "5 скиллов"),
                        (8, "8 скиллов"), (21, "21 скилл"), (22, "22 скилла"),
                        (25, "25 скиллов"), (101, "101 скилл"), (104, "104 скилла")):
            self.assertEqual(say.counted(n, SKILL), want)

    def test_the_eleventh_and_its_neighbours(self):
        # Одиннадцать и три его соседа идут по третьей форме, хотя по последней
        # цифре просились бы в первую и вторую.
        for n, want in ((11, "11 скиллов"), (12, "12 скиллов"), (13, "13 скиллов"),
                        (14, "14 скиллов"), (111, "111 скиллов"), (112, "112 скиллов")):
            self.assertEqual(say.counted(n, SKILL), want)

    def test_zero(self):
        self.assertEqual(say.counted(0, SKILL), "0 скиллов")


class FoldedTest(unittest.TestCase):

    VERBS = ("установлен", "установлено")

    def test_one_does_not_say_the_number(self):
        self.assertEqual(say.folded(self.VERBS, SKILL, ["board-task"], "~/.claude/skills"),
                         "установлен скилл в ~/.claude/skills: board-task")

    def test_many_are_counted_and_named(self):
        self.assertEqual(say.folded(self.VERBS, SKILL, ["board-task", "goal-loop", "live-core"],
                                    "~/.claude/skills"),
                         "установлено 3 скилла в ~/.claude/skills: board-task, goal-loop, "
                         "live-core")

    def test_nothing_done_says_nothing(self):
        # Пустой список это пустая строка, а не «установлено 0 скиллов в ...: »:
        # такую строку зовущий не напечатает, а бессмысленной ей быть незачем.
        self.assertEqual(say.folded(self.VERBS, SKILL, [], "~/.claude/skills"), "")

    def test_the_reason_goes_after_the_place(self):
        self.assertEqual(say.folded(("обновлён", "обновлено"), SKILL, ["goal-loop"],
                                    "~/.claude/skills", ", devkit ушёл вперёд"),
                         "обновлён скилл в ~/.claude/skills, devkit ушёл вперёд: goal-loop")


if __name__ == "__main__":
    unittest.main()
