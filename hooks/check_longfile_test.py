#!/usr/bin/env python3
"""Рубеж длинных чтений (DK-147): PreToolUse-хук на Read отвечает подсказкой
вместо содержимого, когда файл длиннее порога читают целиком (без limit/offset/
pages). Короткий файл и чтение уже куском проходят нетронутыми.

Порог 300 строк выведен из распределения длин чтений (см. шапку check-longfile.py
и docs/tasks/DK-147.md). Состояния у рубежа нет, и каталог состояния хуку не
нужен: решение принимает одно сканирование длины файла."""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-longfile.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "pre-tool-use-read.json")

# Дефис в имени скрипта не годится для import, поэтому модуль грузится по пути.
_spec = importlib.util.spec_from_file_location("check_longfile", TOOL)
check_longfile = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(check_longfile)


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def read_event(path, **window):
    """Событие PreToolUse на Read по пути и параметрам окна. Параметры окна
    опускаются, если их не передали: тогда их нет и в событии, которое
    разбирает хук."""
    ti = {"file_path": path}
    ti.update(window)
    return json.dumps({"session_id": "s1",
                       "hook_event_name": "PreToolUse",
                       "tool_name": "Read",
                       "tool_input": ti})


def write_lines(path, n, line="строка тела файла\n"):
    """Файл из n строк. Перевод строки в конце каждой, как у исходников."""
    with open(path, "w", encoding="utf-8") as f:
        for _ in range(n):
            f.write(line)


class LongFileCase(unittest.TestCase):
    """Базовая фикстура: временный каталог с файлом, путь к которому таскают
    тесты."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.file = os.path.join(self.tmp, "note.md")

    def tearDown(self):
        for root, _, files in os.walk(self.tmp, topdown=False):
            for n in files:
                try:
                    os.remove(os.path.join(root, n))
                except OSError:
                    pass
            try:
                os.rmdir(root)
            except OSError:
                pass

    def hook(self, event):
        return run("--hook", input=event)


class TestShortFilePasses(LongFileCase):
    def test_few_lines_pass(self):
        write_lines(self.file, 10)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_typical_source_file_passes(self):
        # Медиана чтений целиком ~120 строк: обычный исходник рубеж не режет.
        write_lines(self.file, 120)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestLongFileBlocked(LongFileCase):
    def test_long_file_without_limit_is_hint(self):
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn(self.file, r.stderr)
        self.assertIn("DK-147", r.stderr)

    def test_hint_names_limit_and_grep(self):
        # Подсказка должна дать агенту способ обойти рубеж: limit для фрагмента
        # и греп для поиска места.
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file))
        self.assertIn("limit", r.stderr)
        self.assertIn("греп", r.stderr.lower())

    def test_hint_names_threshold(self):
        # Порог в подсказке делает её конкретной: агент понимает масштаб файла.
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file))
        self.assertIn(str(check_longfile.LONG_LINES), r.stderr)

    def test_hint_has_trailing_newline(self):
        # Без него harness склеивает подсказку со следующим выводом, и в
        # транскрипте она читается частью чужой строки.
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file))
        self.assertTrue(r.stderr.endswith("\n"), repr(r.stderr))

    def test_very_long_file_blocked_too(self):
        # Ранний выход на пороге: огромный файл не читается целиком ради счёта,
        # а режется так же. Проверяет, что ранний выход не ломает решение.
        write_lines(self.file, check_longfile.LONG_LINES * 10)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)


class TestBoundary(LongFileCase):
    """Граница порога: LONG_LINES-1 проходит, LONG_LINES режется. Держит
    константу и оператор сравнения."""

    def test_one_below_threshold_passes(self):
        write_lines(self.file, check_longfile.LONG_LINES - 1)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_at_threshold_blocked(self):
        write_lines(self.file, check_longfile.LONG_LINES)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)

    def test_long_lines_spanning_chunk_boundary(self):
        # Длинные строки (по сотне байт), чтобы счётчик переводов строки ушёл за
        # границу одного блока чтения: ранний выход обязан сработать корректно и
        # на файле, где переводы разбросаны реже.
        with open(self.file, "w", encoding="utf-8") as f:
            for _ in range(check_longfile.LONG_LINES + 5):
                f.write("x" * 200 + "\n")
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)


class TestAlreadyChunkPasses(LongFileCase):
    """Чтение с limit/offset/pages это уже фрагмент, а не файл целиком, и
    рубить его не за чем. Развилка из DoD: чтение куском проходит нетронутым."""

    def test_long_file_with_limit_passes(self):
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file, limit=10))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_long_file_with_offset_passes(self):
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file, offset=100))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_long_file_with_pages_passes(self):
        write_lines(self.file, check_longfile.LONG_LINES + 50)
        r = self.hook(read_event(self.file, pages="1-3"))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestBinaryAndMissing(LongFileCase):
    """Бинарный файл и отсутствующий проходят: строки в картинке не считать, а
    у отсутствующего Read сам скажет."""

    def test_binary_file_passes(self):
        # Нуль-байт в теле это признак бинарного: Read отдаёт его как визуальный
        # или двоичный результат, и строк там не считают.
        with open(self.file, "wb") as f:
            f.write(b"\xff\xd8\xff\xe0" + b"\x00" * 100 + b"\n" * 500)
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_missing_file_passes(self):
        absent = os.path.join(self.tmp, "нет.sql")
        r = self.hook(read_event(absent))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestBadInput(LongFileCase):
    def test_bad_json_passes(self):
        r = run("--hook", input="not json")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_missing_tool_input_passes(self):
        r = run("--hook", input=json.dumps({
            "session_id": "s1", "hook_event_name": "PreToolUse",
            "tool_name": "Read"}))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_missing_file_path_passes(self):
        r = run("--hook", input=json.dumps({
            "session_id": "s1", "hook_event_name": "PreToolUse",
            "tool_name": "Read", "tool_input": {}}))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_non_read_tool_passes(self):
        # Матчер стоит на Read, но плохой вход не должен ронять хук.
        r = self.hook(json.dumps({"session_id": "s1",
                                  "hook_event_name": "PreToolUse",
                                  "tool_name": "Bash",
                                  "tool_input": {"command": "ls"}}))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_no_args_prints_doc(self):
        r = run()
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("PreToolUse", r.stderr)


class TestSampleEvent(unittest.TestCase):
    """Живой снимок события из testdata разбирается хуком как есть: форма
    события на стороне инструмента, и в образце хранится её реальный вид, а не
    пересозданный по памяти."""

    def test_sample_passes_without_crash(self):
        # Путь в образце частный (/private/tmp/proj/note.md), и файла там нет:
        # хук уходит нулём, не падая на плохом пути.
        with open(SAMPLE, encoding="utf-8") as f:
            text = f.read()
        r = run("--hook", input=text)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("Traceback", r.stderr)


class TestUnknownProtocol(unittest.TestCase):
    def test_unknown_protocol_refuses(self):
        # Незнакомый протокол это отказ кодом 2, а не молчаливый пропуск:
        # опечатка в settings.json иначе выключила бы рубеж насовсем.
        event = json.dumps({"session_id": "s1",
                            "tool_input": {"file_path": "/tmp"}})
        r = run("--hook", "кодекс", input=event)
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("не заведён", r.stderr)


class TestIsLongUnit(unittest.TestCase):
    """Юнит на is_long: ранний выход, бинарный файл, пустой файл. Хук зовёт
    именно её, и граничное поведение держится на прямых вызовах, а не только
    через подпроцесс."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.file = os.path.join(self.tmp, "f")

    def tearDown(self):
        for root, _, files in os.walk(self.tmp, topdown=False):
            for n in files:
                try:
                    os.remove(os.path.join(root, n))
                except OSError:
                    pass
            try:
                os.rmdir(root)
            except OSError:
                pass

    def test_short_file_is_not_long(self):
        write_lines(self.file, 5)
        self.assertFalse(check_longfile.is_long(self.file))

    def test_at_threshold_is_long(self):
        write_lines(self.file, check_longfile.LONG_LINES)
        self.assertTrue(check_longfile.is_long(self.file))

    def test_empty_file_is_not_long(self):
        with open(self.file, "w") as f:
            f.write("")
        self.assertFalse(check_longfile.is_long(self.file))

    def test_binary_returns_none(self):
        with open(self.file, "wb") as f:
            f.write(b"\x00\x01\x02\n" * 400)
        self.assertIsNone(check_longfile.is_long(self.file))

    def test_missing_returns_none(self):
        self.assertIsNone(check_longfile.is_long(os.path.join(self.tmp, "x")))


if __name__ == "__main__":
    unittest.main(verbosity=0)
