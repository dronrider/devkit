#!/usr/bin/env python3
"""Связи из груминга на доске: вход без маркера «после» это находка
doctor (DK-168). Разбор формул на синтетических досках и прогон doctor
на проекте с доской.
"""
import tempfile
import unittest
from pathlib import Path

import board
from testenv import SandboxCase, executable, write

BOARD_HEAD = """# Задачи проекта (префикс BD)

| ID | Задача |
|--------|--------|
"""


def make_project(root, rows, files):
    """Синтетическая доска и файлы задач: строки как (ID, заголовок)."""
    (root / "docs" / "tasks").mkdir(parents=True)
    write(root / "docs" / "TASKS.md", BOARD_HEAD + "".join("| %s | %s |\n" % r for r in rows))
    for name, text in files.items():
        write(root / "docs" / "tasks" / name, text)


class DepFindingsTest(unittest.TestCase):
    """Разбор связи без doctor: маска входа, живость названной, обе стороны
    маркера и порядок суффиксов заголовка.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def check(self, rows, files):
        make_project(self.root, rows, files)
        return board.check(self.root)

    def test_named_input_without_marker_is_a_finding(self):
        got = self.check(
            [("BD-1", "Первая"), ("BD-2", "Вторая")],
            {"BD-1.md": "# BD-1\n\nРезультат BD-2 нужен как вход: без него порядок не собрать.\n",
             "BD-2.md": "# BD-2\n"},
        )
        self.assertEqual(got, ["docs/tasks/BD-1.md: BD-2 названа входом, а маркера «после» нет "
                               "ни у одной из строк; поставить связь taskctl dep add"],
                         "вход без маркера не дал находку")

    def test_marker_on_own_row_closes_the_finding(self):
        got = self.check(
            [("BD-1", "Первая [после BD-2]"), ("BD-2", "Вторая")],
            {"BD-1.md": "# BD-1\n\nРезультат BD-2 нужен как вход.\n"},
        )
        self.assertEqual(got, [], "маркер у своей строки не погасил связь")

    def test_marker_on_named_row_closes_the_finding(self):
        # Фраза бывает и в обратную сторону: названная задача читает результат
        # этой как вход, и маркер тогда стоит у её строки.
        got = self.check(
            [("BD-1", "Первая"), ("BD-2", "Вторая [после BD-1]")],
            {"BD-1.md": "# BD-1\n\nBD-2 читает журнал как вход.\n"},
        )
        self.assertEqual(got, [], "маркер у названной строки не погасил связь")

    def test_named_input_off_the_board_is_no_finding(self):
        # Вход из закрытой задачи диспетчеру не мешает: живой считается задача
        # со строкой на доске, архив не читается.
        got = self.check(
            [("BD-1", "Первая")],
            {"BD-1.md": "# BD-1\n\nРезультат BD-9 нужен как вход, но BD-9 уже закрыта.\n"},
        )
        self.assertEqual(got, [], "закрытая задача дала находку")

    def test_entry_point_and_verb_are_not_an_input(self):
        # «Вход разговора» и «вход в проект» это точка входа, «входит в цель»
        # глагол: маска обязана отличать их от зависимости.
        got = self.check(
            [("BD-1", "Первая"), ("BD-2", "Вторая")],
            {"BD-1.md": "# BD-1\n\nВход разговора у BD-2, а BD-2 входит в цель.\n"},
        )
        self.assertEqual(got, [], "точка входа или глагол дали находку")

    def test_phrase_wrapped_to_next_line_still_matches(self):
        # Проза переносится, и фраза про вход едет строкой ниже названного ID:
        # совпадение ищется по абзацу, а не по строке.
        got = self.check(
            [("BD-1", "Первая"), ("BD-2", "Вторая")],
            {"BD-1.md": "# BD-1\n\nРезультат BD-2 нужен\nкак вход: без него порядок не собрать.\n"},
        )
        self.assertEqual(got, ["docs/tasks/BD-1.md: BD-2 названа входом, а маркера «после» нет "
                               "ни у одной из строк; поставить связь taskctl dep add"],
                         "перенос фразы на соседнюю строку скрыл вход")

    def test_dep_marker_parsed_before_accept_suffix(self):
        # Порядок суффиксов заголовка как в taskctl: «[после ...]» стоит раньше
        # «[приёмка: ...]», и маркер уходит в конец ячейки только после снятия
        # хвоста.
        got = self.check(
            [("BD-1", "Первая [после BD-2] [приёмка: agent]"), ("BD-2", "Вторая")],
            {"BD-1.md": "# BD-1\n\nРезультат BD-2 нужен как вход.\n"},
        )
        self.assertEqual(got, [], "маркер перед суффиксом приёмки не разобран")

    def test_quoted_phrase_is_citation_not_input(self):
        # Журнал задачи цитирует чужие фразы в «ёлочках»: маска внутри цитаты
        # бьёт файл задачи по нему самому, настоящий вход стоит вне кавычек.
        got = self.check(
            [("BD-1", "Первая"), ("BD-2", "Вторая"), ("BD-3", "Третья")],
            {"BD-1.md": "# BD-1\n\nРазбор назвал входом BD-3 (фраза «результат BD-3 нужен "
                        "как вход»), маркер у строки BD-3.\n",
             "BD-2.md": "# BD-2\n", "BD-3.md": "# BD-3\n"},
        )
        self.assertEqual(got, [], "цитата в «ёлочках» дала находку")

    def test_unquoted_phrase_survives_quoted_span(self):
        # Вырезается сама цитата, а не абзац: фраза вне кавычек в том же
        # абзаце называет вход как обычно.
        got = self.check(
            [("BD-1", "Первая"), ("BD-2", "Вторая")],
            {"BD-1.md": "# BD-1\n\nРезультат BD-2 нужен как вход (в файле это «нужен "
                        "как вход»), без него порядок не собрать.\n",
             "BD-2.md": "# BD-2\n"},
        )
        self.assertEqual(got, ["docs/tasks/BD-1.md: BD-2 названа входом, а маркера «после» нет "
                               "ни у одной из строк; поставить связь taskctl dep add"],
                         "фраза вне кавычек потерялась за цитатой")


class DoctorDepsTest(SandboxCase):
    """Прогон doctor на проекте с доской: находка кодом возврата и строкой,
    погашение маркером в обе стороны. Шаги цепочкой на одном проекте.
    """

    CHAIN = True

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("deps")
        boardbin = cls.box.root / "depsbin"
        boardbin.mkdir()
        executable(boardbin / "taskctl", cls.box.board_taskctl())
        cls.path = "%s:%s" % (boardbin, cls.box.cleanpath)
        cls.box.dkctl_run("new", "--prefix", "BD", "-C", str(cls.proj), path=cls.path)
        # Пустые ключи болванки дали бы свои находки, а тут проверяется только
        # связь из груминга.
        write(cls.proj / ".devkit" / "deploy.local",
              "deploy = echo deploy\ntest = echo test\n")
        cls.tasks = cls.proj / "docs" / "tasks"
        write(cls.tasks / "BD-2.md", "# BD-2: Вторая\n")

    def board(self, first):
        write(self.proj / "docs" / "TASKS.md",
              "# Задачи проекта (префикс BD)\n\n## Backlog\n\n"
              "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
              "|--------|--------|-----|---|---|------|--------|\n"
              "| BD-1 | %s | task | P2 | 30 | S | [tasks/BD-1.md](tasks/BD-1.md) |\n"
              "| BD-2 | Вторая | task | P2 | 30 | S | [tasks/BD-2.md](tasks/BD-2.md) |\n"
              % first)
        write(self.tasks / "BD-1.md",
              "# BD-1: Первая\n\n## Задача\n\nРезультат BD-2 нужен как вход: без него "
              "диспетчер не соберёт порядок работ.\n")

    def test_01_input_without_marker_finds(self):
        self.board("Первая")
        rc, out = self.box.doctor(self.proj, path=self.path)
        self.assertEqual(rc, 1, "doctor не увидел вход без маркера: %s" % out)
        self.assertIn("docs/tasks/BD-1.md: BD-2 названа входом, а маркера «после» нет "
                      "ни у одной из строк; поставить связь taskctl dep add", out,
                      "нет строки находки про связь")

    def test_02_marker_on_own_row_is_clean(self):
        self.board("Первая [после BD-2]")
        rc, out = self.box.doctor(self.proj, path=self.path)
        self.assertEqual(rc, 0, "doctor нашёл находки при маркере у своей строки: %s" % out)

    def test_03_marker_on_named_row_is_clean(self):
        # Фраза называет входом чужой результат для названной задачи: маркер
        # стоит у строки BD-2, и это тоже закрытая связь.
        self.board("Первая")
        path = self.proj / "docs" / "TASKS.md"
        write(path, path.read_text(encoding="utf-8")
                     .replace("| BD-2 | Вторая |", "| BD-2 | Вторая [после BD-1] |"))
        rc, out = self.box.doctor(self.proj, path=self.path)
        self.assertEqual(rc, 0, "doctor нашёл находки при маркере у названной строки: %s" % out)


if __name__ == "__main__":
    unittest.main()
