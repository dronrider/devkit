#!/usr/bin/env python3
"""Вход в доке утилиты: первый раздел README это запуск, а не устройство
(DK-421).

Проверка гоняется на синтетическом дереве с двумя README: у одной утилиты
первым разделом вход, у второй устройство. Живой чекаут devkit тут не годится
по той же причине, что и в тестах раскладки: его README правит каждая задача,
и тест говорил бы про сегодняшнее состояние дерева, а не про правило.
"""
import unittest

import entry
from testenv import SandboxCase, git, git_init, write

# У доброй утилиты первым разделом «Запуск» с командой и наблюдаемым
# результатом, у дурной сразу разбор решения, как в README shipctl до DK-420.
GOOD = ("# notectl: очередь заметок\n\n"
        "## Запуск\n\nДо первой команды нужен конфиг:\n\n"
        "```\nnotectl add \"позвонить\" --tag work\n```\n\n"
        "## Как устроено\n\nОчередь лежит файлом.\n")
BAD = ("# queuectl: очередь заметок\n\n"
       "## Как устроена очередь\n\nФайл на диске.\n\n"
       "## Запуск\n\n```\nqueuectl add x\n```\n")
# Заголовок входа внутри забора кода не считается: это текст примера, а не
# раздел файла. Первым разделом тут идёт устройство, и находка обязана быть.
FENCED = ("# fencectl\n\n"
          "Пример чужого README:\n\n"
          "```markdown\n## Запуск\n```\n\n"
          "## Как устроено\n\nТекст.\n")


def fake_tools(root):
    """Синтетический репозиторий: две утилиты рядом, у одной вход, у другой нет."""
    git_init(root)
    write(root / "README.md", "# стенд\n\n## Состав\n\nДве утилиты.\n")
    write(root / "tools" / "notectl" / "notectl.py", "print(1)\n")
    write(root / "tools" / "notectl" / "README.md", GOOD)
    write(root / "tools" / "queuectl" / "queuectl.py", "print(1)\n")
    write(root / "tools" / "queuectl" / "README.md", BAD)
    git(root, "add", "-A")
    git(root, "commit", "-qm", "стенд")
    return root


class EntryTest(SandboxCase):
    """Находка на README, чей первый раздел не вход."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = fake_tools(cls.box.root / "tools-tree")

    def test_utility_without_entry_is_a_finding(self):
        got = entry.check(self.proj)
        self.assertEqual(len(got), 1, "находок не одна: %s" % got)
        self.assertIn("tools/queuectl/README.md", got[0])
        self.assertIn("Как устроена очередь", got[0])

    def test_utility_with_entry_is_silent(self):
        got = "\n".join(entry.check(self.proj))
        self.assertNotIn("notectl", got, "вход первым разделом посчитан находкой")

    def test_root_readme_is_not_a_utility(self):
        # В корне лежит README проекта, а не утилиты: у него первым разделом
        # «Состав», и требовать там запуск не за что.
        got = "\n".join(entry.check(self.proj))
        self.assertNotIn("\nREADME.md", "\n" + got)

    def test_readme_without_sections_is_silent(self):
        write(self.proj / "tools" / "libctl" / "lib.py", "print(1)\n")
        write(self.proj / "tools" / "libctl" / "README.md",
              "# libctl\n\nБиблиотека, зовут её из соседей.\n")
        try:
            got = "\n".join(entry.check(self.proj))
            self.assertNotIn("libctl", got, "README без разделов посчитан находкой")
        finally:
            for name in ("lib.py", "README.md"):
                (self.proj / "tools" / "libctl" / name).unlink()
            (self.proj / "tools" / "libctl").rmdir()

    def test_readme_beside_no_code_is_not_a_utility(self):
        # Каталог материалов без кода докой утилиты не считается: там свой
        # порядок разделов, и запуска у него нет.
        write(self.proj / "kit" / "README.md",
              "# kit\n\n## Что лежит\n\nШаблоны.\n")
        try:
            got = "\n".join(entry.check(self.proj))
            self.assertNotIn("kit/README.md", got)
        finally:
            (self.proj / "kit" / "README.md").unlink()
            (self.proj / "kit").rmdir()

    def test_entry_heading_inside_fence_does_not_count(self):
        write(self.proj / "tools" / "fencectl" / "fence.py", "print(1)\n")
        write(self.proj / "tools" / "fencectl" / "README.md", FENCED)
        try:
            got = "\n".join(entry.check(self.proj))
            self.assertIn("fencectl/README.md", got)
            self.assertIn("Как устроено", got)
        finally:
            for name in ("fence.py", "README.md"):
                (self.proj / "tools" / "fencectl" / name).unlink()
            (self.proj / "tools" / "fencectl").rmdir()

    def test_testdata_readme_is_skipped(self):
        # Стенд тестов это материал прогона, а не дока утилиты.
        write(self.proj / "tools" / "notectl" / "testdata" / "case.py", "print(1)\n")
        write(self.proj / "tools" / "notectl" / "testdata" / "README.md",
              "# случаи\n\n## Что тут лежит\n\nВходные данные.\n")
        try:
            got = "\n".join(entry.check(self.proj))
            self.assertNotIn("testdata", got)
        finally:
            for name in ("case.py", "README.md"):
                (self.proj / "tools" / "notectl" / "testdata" / name).unlink()
            (self.proj / "tools" / "notectl" / "testdata").rmdir()

    def test_doctor_names_the_readme(self):
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 1, "doctor не увидел README без входа")
        self.assertIn("tools/queuectl/README.md", out)

    def test_fix_does_not_touch_the_readme(self):
        before = (self.proj / "tools" / "queuectl" / "README.md").read_text(encoding="utf-8")
        self.box.doctor(self.proj, "--fix")
        after = (self.proj / "tools" / "queuectl" / "README.md").read_text(encoding="utf-8")
        self.assertEqual(before, after, "--fix переписал README, а вход пишется руками")
        rc, out = self.box.doctor(self.proj)
        self.assertIn("tools/queuectl/README.md", out, "после --fix находка пропала")


if __name__ == "__main__":
    unittest.main()
