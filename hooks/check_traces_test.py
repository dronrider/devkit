#!/usr/bin/env python3
"""Рубеж следов devkit в корп-клоне: локальный ID задачи, путь боковой
директории с доской и слова контура не должны уезжать в чужой репозиторий.

Стенд везде один: боковая директория с доской и корп-клон с редиректом на неё.
Путь редиректа относительный, как его кладёт подключение, заодно проверяется,
что он считается от чекаута.
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-traces.py")

# Пометка про префикс стоит и ниже секции, в строке задачи: шапка кончается на
# первой секции, и такая пометка за префикс доски сойти не должна.
BOARD = ("# проба: задачи (префикс %s)\n\n## In progress\n\n"
         "| XR-1 | задача (префикс QQ) |\n")


class TracesCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.side = os.path.join(self.tmp, "proj-local")
        os.makedirs(os.path.join(self.side, "docs"))
        os.makedirs(os.path.join(self.side, ".devkit"))
        self.board("XR")
        self.corp = os.path.join(self.tmp, "corp")
        subprocess.run(["git", "init", "-q", self.corp], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.corp, "config", key, value], check=True)

    def board(self, prefix):
        with open(os.path.join(self.side, "docs", "TASKS.md"), "w",
                  encoding="utf-8") as f:
            f.write(BOARD % prefix)

    def tracker(self, text):
        with open(os.path.join(self.side, ".devkit", "tracker.local"), "w",
                  encoding="utf-8") as f:
            f.write(text)

    def redirect(self):
        subprocess.run(["git", "-C", self.corp, "config", "devkit.local",
                        "../proj-local"], check=True)

    def stage(self, text, name="note.txt"):
        with open(os.path.join(self.corp, name), "w", encoding="utf-8") as f:
            f.write(text + "\n")
        subprocess.run(["git", "-C", self.corp, "add", name], check=True)

    def staged(self):
        return subprocess.run([sys.executable, TOOL, "--staged"], cwd=self.corp,
                              capture_output=True, text=True)

    def message(self, text):
        path = os.path.join(self.tmp, "cmsg")
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return subprocess.run([sys.executable, TOOL, "--msg", path], cwd=self.corp,
                              capture_output=True, text=True)


class TestStagedDiff(TracesCase):
    def test_without_redirect_the_line_is_quiet(self):
        # Домашние проекты рубежа не замечают.
        self.stage("правка по XR-007")
        self.assertEqual(self.staged().returncode, 0)

    def test_local_id_is_caught_with_advice(self):
        self.redirect()
        self.stage("правка по XR-007")
        r = self.staged()
        self.assertEqual(r.returncode, 1)
        self.assertIn("локальный ID", r.stdout + r.stderr)

    def test_alien_prefix_passes(self):
        # Префикс берётся из шапки боковой доски, а не из головы, и пометка
        # ниже секции за префикс не считается.
        self.redirect()
        self.stage("правка по DK-007 и QQ-1")
        self.assertEqual(self.staged().returncode, 0)

    def test_header_change_moves_the_catch(self):
        self.redirect()
        self.board("ZZ")
        self.stage("правка по ZZ-9")
        self.assertEqual(self.staged().returncode, 1)

    def test_side_path_is_a_trace(self):
        self.redirect()
        self.stage("смотри %s/docs/TASKS.md" % self.side)
        r = self.staged()
        self.assertEqual(r.returncode, 1)
        self.assertIn("путь боковой", r.stdout + r.stderr)

    def test_side_name_is_a_trace(self):
        self.redirect()
        self.stage("лежит в proj-local рядом")
        self.assertEqual(self.staged().returncode, 1)

    def test_word_from_the_binding(self):
        # Перечень расширяется словами контура из привязки.
        self.redirect()
        self.tracker("repo = %s\ntraces = ковчег\n" % self.corp)
        self.stage("внутренний Ковчег проекта")
        self.assertEqual(self.staged().returncode, 1)

    def test_local_id_in_the_file_name(self):
        # Имя добавленного файла смотрится наравне с его строками.
        self.redirect()
        self.stage("обычная строка", name="XR-012.md")
        self.assertEqual(self.staged().returncode, 1)


class TestKeyEqualsPrefix(TracesCase):
    """Префикс доски, совпавший с ключом проекта в привязке: локальный ID и
    ключ тикета там одна и та же строка, отличить их нечем, и правило про ID
    снимается целиком, иначе рубеж валил бы каждый коммит по конвенции компании
    (DK-124)."""

    def setUp(self):
        super().setUp()
        self.redirect()
        self.tracker("repo = %s\ntraces = ковчег\nkey = XR\n" % self.corp)

    def test_ticket_key_passes(self):
        self.stage("правка по XR-007")
        self.assertEqual(self.staged().returncode, 0)

    def test_side_path_is_still_watched(self):
        self.stage("смотри %s/docs/TASKS.md" % self.side)
        self.assertEqual(self.staged().returncode, 1)

    def test_word_of_the_circuit_is_still_watched(self):
        self.stage("внутренний Ковчег проекта")
        self.assertEqual(self.staged().returncode, 1)


class TestKeyApartFromPrefix(TracesCase):
    """Ключ проекта, разведённый с префиксом доски: правило про ID работает как
    работало, а ключ тикета проходит."""

    def setUp(self):
        super().setUp()
        self.redirect()
        self.tracker("repo = %s\ntraces = ковчег\nkey = TR\n" % self.corp)

    def test_local_id_is_still_caught(self):
        self.stage("правка по XR-007")
        self.assertEqual(self.staged().returncode, 1)

    def test_ticket_key_passes(self):
        self.stage("правка по TR-007")
        self.assertEqual(self.staged().returncode, 0)


class TestCommitMessage(TracesCase):
    """Сообщение коммита смотрится отдельно от диффа: чистый дифф от следа в
    тексте не спасает."""

    def setUp(self):
        super().setUp()
        self.redirect()
        self.stage("обычная правка")

    def test_local_id_in_the_message(self):
        self.assertEqual(self.message("feat: правка по XR-007\n").returncode, 1)

    def test_clean_message_passes(self):
        self.assertEqual(self.message("feat: обычная правка\n").returncode, 0)

    def test_git_comments_are_not_read(self):
        self.assertEqual(self.message("feat: чисто\n"
                                      "# ветка dk-086 в XR-007\n").returncode, 0)

    def test_scissors_tail_is_not_read(self):
        self.assertEqual(self.message(
            "feat: чисто\n"
            "# ------------------------ >8 ------------------------\n"
            "diff по XR-007\n").returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
