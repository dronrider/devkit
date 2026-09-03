#!/usr/bin/env python3
"""Самопроверка отметки хода (DK-724): строка ~/.devkit/turns.log про то, чем
кончился ход живой сессии. Разбор идёт с живых образцов событий харнеса, а не с
сочинённого JSON, а прогон хука подпроцессом со подсунутым stdin проверяет то
же, что стоит в settings.json: команда пишет строку и уходит нулём.
"""
import importlib
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "turn-mark.py")
DATA = os.path.join(HERE, "testdata", "claude-code")

sys.path.insert(0, HERE)
import hookio  # noqa: E402
turn_mark = importlib.import_module("turn-mark")

KEYS = ("сессия", "ход", "повод", "дерево")


def sample(name):
    with open(os.path.join(DATA, name), encoding="utf-8") as f:
        return json.load(f)


def fields(line):
    """Строка журнала парами «ключ значение»: значение идёт до следующего
    ключевого слова, и пробел в пути дерева строку не рассыпает."""
    words = line.split()[1:]
    out, key = {}, None
    for w in words:
        if w in KEYS:
            key, out[key] = w, ""
        elif key:
            out[key] = (out[key] + " " + w).strip()
    return out


def run(event, env=None):
    """Прогон хука подпроцессом. Возврат (код, строки журнала)."""
    with tempfile.TemporaryDirectory(prefix="turn-mark-") as tmp:
        log = os.path.join(tmp, "turns.log")
        run_env = dict(os.environ, DEVKIT_TURN_MARK_LOG=log)
        run_env.pop("DEVKIT_RUN_DEPTH", None)
        run_env.pop("DEVKIT_SILENT", None)
        run_env.update(env or {})
        p = subprocess.run([sys.executable, HOOK, "--hook"], input=json.dumps(event),
                           capture_output=True, text=True, env=run_env)
        lines = []
        if os.path.exists(log):
            with open(log, encoding="utf-8") as f:
                lines = [l for l in f.read().split("\n") if l.strip()]
        return p.returncode, lines


class TestMark(unittest.TestCase):
    def test_turn_done_is_written(self):
        code, lines = run(sample("turn-done.json"))
        self.assertEqual(code, 0)
        self.assertEqual(len(lines), 1)
        self.assertEqual(fields(lines[0])["ход"], "кончен")

    def test_turn_failed_says_the_api_error(self):
        """Ход, убитый ошибкой API, тоже кончился: оболочка конвейера поднимает
        по нему следующий заказ, иначе сессия стоит до ручного «продолжай»."""
        code, lines = run(sample("turn-failed.json"))
        self.assertEqual(code, 0)
        self.assertEqual(fields(lines[0])["ход"], "упал")

    def test_permission_prompt_is_a_waiting_turn(self):
        """Запрос разрешения это стоячая сессия: по этой отметке оболочка зовёт
        человека, пока он не смотрит на окно."""
        code, lines = run(sample("notify-permission.json"))
        self.assertEqual(code, 0)
        got = fields(lines[0])
        self.assertEqual(got["ход"], "ждёт")
        self.assertEqual(got["повод"], "permission_prompt")

    def test_prompt_submit_marks_a_started_turn(self):
        """Реплика человека, поданная панелью в то же окно, начинает ход. Без
        этой отметки оболочка послала бы свой заказ поверх чужого."""
        code, lines = run(sample("prompt-submit.json"))
        self.assertEqual(code, 0)
        self.assertEqual(fields(lines[0])["ход"], "начат")

    def test_subagent_stop_is_not_a_turn(self):
        """Конец субагента это не конец хода сессии: за ним смотрит свой
        сторож, и отметка тут сбила бы счёт проходов."""
        code, lines = run(sample("subagent-done.json"))
        self.assertEqual(code, 0)
        self.assertEqual(lines, [])

    def test_delegated_subprocess_keeps_quiet(self):
        """Сессия подпроцесса делегирования наследует окно хозяина вместе с
        переменными, и её конец хода оболочка приняла бы за конец своего."""
        code, lines = run(sample("turn-done.json"), env={"DEVKIT_RUN_DEPTH": "1"})
        self.assertEqual(code, 0)
        self.assertEqual(lines, [])

    def test_broken_event_does_not_break_the_session(self):
        with tempfile.TemporaryDirectory(prefix="turn-mark-") as tmp:
            log = os.path.join(tmp, "turns.log")
            env = dict(os.environ, DEVKIT_TURN_MARK_LOG=log)
            env.pop("DEVKIT_RUN_DEPTH", None)
            p = subprocess.run([sys.executable, HOOK, "--hook"], input="не json",
                               capture_output=True, text=True, env=env)
            self.assertEqual(p.returncode, 0)
            self.assertFalse(os.path.exists(log))

    def test_short_session_id_is_kept_as_is(self):
        """Харнес отдаёт этому событию первые восемь знаков ID, и обрезать их
        дальше нельзя: читатель журнала сводит отметку с реестром чатов началом
        строки."""
        sess = hookio.Session(kind=hookio.TURN_DONE, reason="", session="5a750327",
                              cwd="/tmp", transcript="", message="", agent=None)
        self.assertIn(" сессия 5a750327 ход кончен ", turn_mark.record(sess))

    def test_event_without_a_session_writes_nothing(self):
        sess = hookio.Session(kind=hookio.TURN_DONE, reason="", session="-",
                              cwd="/tmp", transcript="", message="", agent=None)
        self.assertEqual(turn_mark.record(sess), "")


if __name__ == "__main__":
    unittest.main()
