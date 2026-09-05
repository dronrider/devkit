#!/usr/bin/env python3
"""Сторожок стыка фаз: подсказка при занятом окне, тишина на пустом журнале,
оценка фазы по записям задач той же цены. Окружение каждому прогону своё:
временный журнал, своя доска и транскрипт из образца, снятого живьём."""
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
TOOL = os.path.join(HERE, "phase-budget.py")
DATA = os.path.join(HERE, "testdata", "claude-code")
TRANSCRIPT = os.path.join(DATA, "transcript-usage.jsonl")

_spec = importlib.util.spec_from_file_location("phase_budget", TOOL)
hook = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(hook)

# Последний ход ассистента в образце: 2 свежих, 338 в запись кеша, 147 988
# из кеша. Числа сняты с самого файла глазами, а не пересчитаны кодом.
SAMPLE_USED = 2 + 338 + 147988
SESSION = "5a2d4b71-9c30-42ef-8f6d-cc0011223366"

BOARD = """# proj: задачи

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-11 | разработка идёт | task | P1 | 60 (50+5+2+0+3) | M | - |

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-12 | сдана на проверку | task | P1 | 60 (50+5+2+0+3) | M | - |
| DK-13 | сдана, цена не названа | bug | P2 | 40 (25+5+1+5+4) | - | - |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-14 | лежит | task | P2 | 30 (25+2+0+0+3) | S | - |

## Blocked

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-15 | ждёт | task | P2 | 30 (25+2+0+0+3) | M | - |
"""

ARCHIVE = """# proj: архив

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-16 | закрыта | task | P1 | 60 (50+5+2+0+3) | M | - |
"""


def sample_event(command, transcript=TRANSCRIPT, session=SESSION):
    """Событие PostToolUse Bash из живого образца с подменённой командой."""
    with open(os.path.join(DATA, "tool-done-bash.json"), encoding="utf-8") as f:
        event = json.load(f)
    event["tool_input"]["command"] = command
    event["transcript_path"] = transcript
    event["session_id"] = session
    return event


class Box(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="phase-budget-")
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        docs = os.path.join(self.tmp, "docs")
        os.makedirs(docs)
        # Дерево хода это ближайший предок с .git: без него доска от cwd не ищется.
        os.makedirs(os.path.join(self.tmp, ".git"))
        self.board = os.path.join(docs, "TASKS.md")
        with open(self.board, "w", encoding="utf-8") as f:
            f.write(BOARD)
        with open(os.path.join(docs, "TASKS-archive.md"), "w", encoding="utf-8") as f:
            f.write(ARCHIVE)
        self.log = os.path.join(self.tmp, "phase.log")
        self.env = {"PATH": os.environ.get("PATH", ""), "HOME": self.tmp,
                    "DEVKIT_PHASE_LOG": self.log, "DEVKIT_PHASE_BOARD": self.board}

    def run_hook(self, event, *args):
        return subprocess.run([sys.executable, TOOL, "--hook"] + list(args),
                              input=json.dumps(event), capture_output=True, text=True,
                              env=self.env)

    def log_lines(self):
        if not os.path.exists(self.log):
            return []
        with open(self.log, encoding="utf-8") as f:
            return f.read().splitlines()

    def write_log(self, lines):
        with open(self.log, "w", encoding="utf-8") as f:
            f.write("".join(l + "\n" for l in lines))

    def said(self, r):
        self.assertEqual(r.returncode, 0, r.stderr)
        if not r.stdout.strip():
            return ""
        return json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]


class TestTransition(unittest.TestCase):
    """Какая команда считается переходом. Ожидание выписано по форме вызовов
    из скилла board-task: move с ID и статусом, close с ID."""

    def test_move_with_status(self):
        self.assertEqual(hook.transition("taskctl move DK-12 check"), ("DK-12", "check"))

    def test_move_with_dir_and_flags(self):
        self.assertEqual(hook.transition("taskctl -C /tmp/x move XR-7 in-progress -m 'в работу' --push"),
                         ("XR-7", "in-progress"))

    def test_close(self):
        self.assertEqual(hook.transition("taskctl close DK-16 -m 'закрыта'"), ("DK-16", "close"))

    def test_inside_compound(self):
        self.assertEqual(hook.transition("git -C /x pull -q && taskctl move DK-12 check"),
                         ("DK-12", "check"))

    def test_other_commands_are_not_transitions(self):
        for cmd in ("taskctl show DK-12", "taskctl move", "taskctl move DK-12 --dry-run",
                    "echo taskctl move DK-12 check", "mytaskctl move DK-12 check", ""):
            self.assertIsNone(hook.transition(cmd), cmd)


class TestUsage(unittest.TestCase):
    """Занятость окна снимается с последнего хода ассистента живого образца."""

    def test_last_assistant_usage(self):
        self.assertEqual(hook.usage_of(TRANSCRIPT), (SAMPLE_USED, hook.WINDOW, "claude-opus-5"))

    def test_empty_and_missing_are_none(self):
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as f:
            path = f.name
        self.addCleanup(os.unlink, path)
        self.assertIsNone(hook.usage_of(path))
        self.assertIsNone(hook.usage_of(path + ".нет"))
        self.assertIsNone(hook.usage_of(""))

    def test_broken_lines_are_skipped(self):
        with open(TRANSCRIPT, encoding="utf-8") as f:
            good = f.read()
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False, encoding="utf-8") as f:
            f.write(good + '{"type":"assistant","message":{"usage":"x"}}\n{не json "usage"\n')
            path = f.name
        self.addCleanup(os.unlink, path)
        self.assertEqual(hook.usage_of(path)[0], SAMPLE_USED)

    def test_million_window_by_model_suffix(self):
        self.assertEqual(hook.window_of("claude-opus-5[1m]"), hook.WINDOW_1M)
        self.assertEqual(hook.window_of("claude-opus-5"), hook.WINDOW)
        self.assertEqual(hook.window_of(""), hook.WINDOW)


class TestBoard(Box):
    """Строка задачи ищется в той секции, куда вёл переход, цена берётся из
    шестой колонки."""

    def test_row_section_and_price(self):
        self.assertEqual(hook.board_row(self.board, "DK-12"), ("check", "M"))
        self.assertEqual(hook.board_row(self.board, "DK-11"), ("in-progress", "M"))
        self.assertEqual(hook.board_row(self.board, "DK-14"), ("backlog", "S"))
        self.assertEqual(hook.board_row(self.board, "DK-13"), ("check", "-"))
        self.assertIsNone(hook.board_row(self.board, "DK-99"))

    def test_archive_row_is_closed(self):
        self.assertEqual(hook.find_row(self.tmp, "DK-16", "close"), ("close", "M"))
        self.assertIsNone(hook.find_row(self.tmp, "DK-12", "close"))

    def test_refused_move_is_not_a_junction(self):
        self.assertIsNone(hook.find_row(self.tmp, "DK-14", "check"))

    def test_outside_git_tree_finds_nothing(self):
        self.assertEqual(hook.board_files(tempfile.gettempdir()), [])


class TestEstimate(unittest.TestCase):
    """Оценка по записям той же цены и той же фазы; медиана и третья четверть
    посчитаны руками по значениям ниже."""

    RECS = [
        {"закрыл": "проверка", "цена": "M", "рост": "10000"},
        {"закрыл": "проверка", "цена": "M", "рост": "40000"},
        {"закрыл": "проверка", "цена": "M", "рост": "20000"},
        {"закрыл": "проверка", "цена": "M", "рост": "30000"},
        {"закрыл": "проверка", "цена": "S", "рост": "5000"},
        {"закрыл": "разработка", "цена": "M", "рост": "90000"},
        {"закрыл": "проверка", "цена": "M", "рост": "-"},
    ]

    def test_same_price_same_phase(self):
        self.assertEqual(hook.estimate(self.RECS, "M", "проверка"), (25000, 40000, 4))

    def test_single_record(self):
        self.assertEqual(hook.estimate(self.RECS, "S", "проверка"), (5000, 5000, 1))

    def test_no_records(self):
        self.assertIsNone(hook.estimate(self.RECS, "L", "проверка"))
        self.assertIsNone(hook.estimate([], "M", "проверка"))

    def test_threshold_falls_back_to_share(self):
        limit, source = hook.threshold([], "M", "проверка", 200000)
        self.assertEqual(limit, 30000)
        self.assertIn("по умолчанию", source)
        limit, source = hook.threshold(self.RECS, "M", "проверка", 200000)
        self.assertEqual(limit, 40000)
        self.assertIn("записей 4", source)

    def test_growth_within_session_only(self):
        prev = {"сессия": "s1", "окно": "100000", "фаза": "разработка"}
        self.assertEqual(hook.growth(prev, "s1", 130000), ("разработка", 30000))
        self.assertEqual(hook.growth(prev, "s2", 130000), (None, None))
        self.assertEqual(hook.growth(None, "s1", 130000), (None, None))
        self.assertEqual(hook.growth({"сессия": "s1", "окно": "100000", "фаза": "-"}, "s1", 1),
                         (None, None))
        # Сжатие между переходами уменьшает окно, отрицательный рост не пишется.
        self.assertEqual(hook.growth(prev, "s1", 50000), ("разработка", 0))


class TestHook(Box):
    """Хук как процесс: подсказка при занятом окне, тишина на пустом журнале и
    на чужих ходах, запись перехода в журнал."""

    def test_busy_window_prints_hint_with_actions(self):
        # Образец занят на 148 340 из 200 000: до черты харнеса (180 000)
        # остаётся 31 660, а порог проверки по умолчанию 30 000. Одна запись
        # по цене M на 45 000 поднимает порог выше остатка.
        self.write_log(["2026-09-01T10:00:00 сессия s0 задача DK-1 цена M статус close фаза - "
                        "окно 90000 из 200000 закрыл проверка рост 45000"])
        said = self.said(self.run_hook(sample_event("taskctl move DK-12 check")))
        self.assertIn("DK-12 -> check", said)
        self.assertIn("остаток до черты харнеса 32 тыс", said)
        self.assertIn("до 45 тыс токенов", said)
        for action in hook.ACTIONS:
            self.assertIn(action, said)
        self.assertLess(said.index("продолжить"), said.index("субагента"))
        self.assertLess(said.index("субагента"), said.index("файлом задачи"))
        self.assertLess(said.index("файлом задачи"), said.index("сжать"))
        self.assertIn("--show DK-12", said)

    def test_default_threshold_when_log_is_empty(self):
        # Записей нет: порог разработки по умолчанию 70 000, остаток 31 660.
        said = self.said(self.run_hook(sample_event("taskctl move DK-11 in-progress")))
        self.assertIn("по умолчанию 70 тыс", said)
        self.assertIn("остаток", said)

    def test_room_enough_is_silent_but_recorded(self):
        said = self.said(self.run_hook(sample_event("taskctl move DK-12 check")))
        self.assertEqual(said, "")
        lines = self.log_lines()
        self.assertEqual(len(lines), 1, lines)
        self.assertIn("задача DK-12 цена M статус check фаза проверка окно %d из 200000 "
                      "закрыл - рост -" % SAMPLE_USED, lines[0])
        self.assertIn("сессия %s" % SESSION, lines[0])

    def test_empty_transcript_is_silent(self):
        with open(os.path.join(self.tmp, "пусто.jsonl"), "w"):
            pass
        r = self.run_hook(sample_event("taskctl move DK-12 check",
                                       transcript=os.path.join(self.tmp, "пусто.jsonl")))
        self.assertEqual((r.returncode, r.stdout), (0, ""), r.stderr)
        self.assertEqual(self.log_lines(), [])

    def test_other_tool_and_other_command_are_silent(self):
        event = sample_event("taskctl move DK-12 check")
        event["tool_name"] = "Edit"
        r = self.run_hook(event)
        self.assertEqual((r.returncode, r.stdout), (0, ""), r.stderr)
        with open(os.path.join(DATA, "tool-done-bash.json"), encoding="utf-8") as f:
            r = subprocess.run([sys.executable, TOOL, "--hook"], input=f.read(),
                               capture_output=True, text=True, env=self.env)
        self.assertEqual((r.returncode, r.stdout), (0, ""), r.stderr)
        self.assertEqual(self.log_lines(), [])

    def test_refused_move_is_silent(self):
        r = self.run_hook(sample_event("taskctl move DK-14 check"))
        self.assertEqual((r.returncode, r.stdout), (0, ""), r.stderr)
        self.assertEqual(self.log_lines(), [])

    def test_blocked_records_growth_without_hint(self):
        self.write_log(["2026-09-01T10:00:00 сессия %s задача DK-15 цена M статус in-progress "
                        "фаза разработка окно 100000 из 200000 закрыл - рост -" % SESSION])
        said = self.said(self.run_hook(sample_event("taskctl move DK-15 blocked")))
        self.assertEqual(said, "")
        lines = self.log_lines()
        self.assertEqual(len(lines), 2)
        self.assertIn("статус blocked фаза - окно %d из 200000 закрыл разработка рост %d"
                      % (SAMPLE_USED, SAMPLE_USED - 100000), lines[1])

    def test_close_records_check_growth(self):
        self.write_log(["2026-09-01T10:00:00 сессия %s задача DK-16 цена M статус check "
                        "фаза проверка окно 140000 из 200000 закрыл разработка рост 50000" % SESSION])
        said = self.said(self.run_hook(sample_event("taskctl close DK-16")))
        self.assertEqual(said, "")
        self.assertIn("статус close фаза - окно %d из 200000 закрыл проверка рост %d"
                      % (SAMPLE_USED, SAMPLE_USED - 140000), self.log_lines()[1])

    def test_unknown_protocol_is_refused(self):
        r = self.run_hook(sample_event("taskctl move DK-12 check"), "кодекс")
        self.assertEqual(r.returncode, 2)
        self.assertIn("не заведён", r.stderr)

    def test_bad_input_is_silent(self):
        r = subprocess.run([sys.executable, TOOL, "--hook"], input="не json",
                           capture_output=True, text=True, env=self.env)
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))


class TestShow(Box):
    """Показ снаружи: записи задачи, оценки по цене, остаток по транскрипту."""

    def test_show_lists_records_and_estimates(self):
        self.write_log(["2026-09-01T10:00:00 сессия s0 задача DK-12 цена M статус check "
                        "фаза проверка окно 140000 из 200000 закрыл разработка рост 50000",
                        "2026-09-01T11:00:00 сессия s0 задача DK-12 цена M статус close "
                        "фаза - окно 160000 из 200000 закрыл проверка рост 20000"])
        r = subprocess.run([sys.executable, TOOL, "--show", "DK-12", TRANSCRIPT],
                           capture_output=True, text=True, env=self.env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("статус check окно 140000 из 200000 закрыл разработка рост 50000", r.stdout)
        self.assertIn("занято 148 тыс из 200 тыс", r.stdout)
        self.assertIn("фаза «проверка» у задач цены M брала до 20 тыс", r.stdout)
        self.assertIn("фаза «разработка» у задач цены M брала до 50 тыс", r.stdout)
        r = subprocess.run([sys.executable, TOOL, "--show", "DK-12"],
                           capture_output=True, text=True, env=self.env)
        self.assertNotIn("транскрипт", r.stdout)
        self.assertIn("статус close окно 160000", r.stdout)

    def test_show_without_records_says_so(self):
        r = subprocess.run([sys.executable, TOOL, "--show", "DK-77"],
                           capture_output=True, text=True, env=self.env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("записей по DK-77", r.stdout)

    def test_usage_without_mode_prints_doc(self):
        r = subprocess.run([sys.executable, TOOL], capture_output=True, text=True, env=self.env)
        self.assertEqual(r.returncode, 2)
        self.assertIn("--show", r.stderr)


if __name__ == "__main__":
    unittest.main()
