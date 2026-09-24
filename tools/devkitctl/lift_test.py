#!/usr/bin/env python3
"""Подъём работы, оставшейся без живой сессии (DK-1157).

Доска тут подделана выводом `taskctl list --json`, потому что проверяется
именно разбор чужого ответа и решения по нему. Настоящая доска дала бы те же
поля дороже. Процессы и замки настоящие: мера смерти владельца это сигнал
нулём, и подделать её нечем.
"""
import json
import os
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

import lift


def board(rows, key="in-progress"):
    """Ответ `taskctl list --json` с названными строками."""
    return json.dumps({"prefix": "DK", "sections": [
        {"key": "backlog", "title": "Backlog", "rows": []},
        {"key": key, "title": "In progress", "rows": rows},
    ]}, ensure_ascii=False)


def row(tid, stage, session):
    return {"id": tid, "title": "строка " + tid, "stage": stage,
            "stage_session": session}


class Fake:
    """Подстава подпроцесса: помнит вызовы и отдаёт заготовленные ответы."""

    def __init__(self, out="", code=0):
        self.calls = []
        self.out = out
        self.code = code

    def __call__(self, argv, **kw):
        self.calls.append(argv)
        return subprocess.CompletedProcess(argv, self.code, self.out, "")


class OrphanRowsCase(unittest.TestCase):
    """Выбор строк: поднимается работа без сессии, ожидание не трогается."""

    def test_work_without_session(self):
        rows = [row("DK-1", "слияние", "сессии нет, брошена")]
        self.assertEqual([r["id"] for r in lift.orphan_rows(rows)], ["DK-1"])

    def test_wait_stages_left_alone(self):
        rows = [row("DK-2", "ждёт очереди", "сессии нет, брошена"),
                row("DK-3", "ждёт человека", "сессии нет, брошена"),
                row("DK-4", "ждёт события", "сессии нет, брошена")]
        self.assertEqual(lift.orphan_rows(rows), [])

    def test_live_session_left_alone(self):
        rows = [row("DK-5", "разработка", "сессия жива")]
        self.assertEqual(lift.orphan_rows(rows), [])

    def test_busy_counted(self):
        rows = [row("DK-6", "разработка", "сессия жива"),
                row("DK-7", "слияние", "сессии нет, брошена")]
        self.assertEqual(lift.busy_count(rows), 1)


class SettleCase(unittest.TestCase):
    """Выдержка: строка, поднятая только что, второй раз не поднимается.

    Сессия харнеса заводит транскрипт не мгновенно, и до первой записи доска
    честно говорит «сессии нет». На живом прогоне это подняло одну строку
    дважды за минуту.
    """

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home)]))

    def test_fresh_mark_holds_second_lift(self):
        call = Fake(board([row("DK-8", "слияние", "сессии нет, брошена")]))
        lift.mark_lifted("/root", "DK-8", home=str(self.home))
        lines, raised = lift.lift_root("/root", call=call, taskctl="taskctl",
                                       home=str(self.home))
        self.assertEqual(raised, 0)
        self.assertIn("подняты недавно", " ".join(lines))

    def test_stale_mark_lets_lift(self):
        old = time.time() - lift.SETTLE - 60
        lift.mark_lifted("/root", "DK-9", home=str(self.home), now=old)
        self.assertEqual(lift.recent(home=str(self.home)), {})

    def test_log_does_not_grow(self):
        """Забытые строки журнала уносит та же запись следа. Запись живёт сутки,
        а не выдержку: по ней считается число попыток подряд."""
        old = time.time() - lift.FORGET - 60
        for n in range(5):
            lift.mark_lifted("/root", "DK-%d" % n, home=str(self.home), now=old)
        lift.mark_lifted("/root", "DK-new", home=str(self.home))
        with open(lift.log_path(str(self.home)), encoding="utf-8") as fh:
            text = fh.read()
        self.assertEqual(len(text.strip().splitlines()), 1)


class OrderCase(unittest.TestCase):
    """Реплика подъёма называет строку и запрещает брать другую работу.

    На живом прогоне поднятая сессия вместо своей строки взяла с доски ту,
    которую вёл человек: слова «продолжай» ей хватило, чтобы пойти за работой
    самой.
    """

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home)]))

    def test_order_names_the_row(self):
        call = Fake(board([row("DK-10", "проверка", "сессии нет, брошена")]))
        lift.lift_root("/root", call=call, taskctl="taskctl", home=str(self.home))
        orders = [a for a in call.calls if "run" in a]
        self.assertTrue(orders, "заказа не было")
        argv = orders[0]
        self.assertIn("DK-10", argv[argv.index("--order") + 1])
        self.assertIn("Другую работу с доски не бери", argv[argv.index("--order") + 1])


class CapacityCase(unittest.TestCase):
    """Ёмкость: поднимается не больше свободных мест."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home)]))

    def test_busy_rows_eat_capacity(self):
        rows = [row("DK-11", "разработка", "сессия жива"),
                row("DK-12", "разработка", "сессия жива")]
        free, why = lift.capacity("/root", rows, call=Fake("batch: 2"),
                                  agentctl="agentctl")
        self.assertEqual(free, 0)
        self.assertIn("живых сессий 2", why)

    def test_machine_limit_caps_lift(self):
        """Потолок на машину режет подъём, даже когда у корня места есть."""
        rows = [row("DK-13", "слияние", "сессии нет, брошена"),
                row("DK-14", "слияние", "сессии нет, брошена")]
        call = Fake(board(rows))
        lines, raised = lift.lift_root("/root", call=call, taskctl="taskctl",
                                       home=str(self.home), left=1)
        self.assertEqual(raised, 1)
        self.assertIn("место кончилось", " ".join(lines))


class LocksCase(unittest.TestCase):
    """Замки задач: снимается замок мёртвого владельца, живой не трогается."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home)]))

    def lock(self, name, pid):
        d = self.home / ("task-%s.lock" % name)
        d.mkdir()
        (d / "pid").write_text(str(pid), encoding="utf-8")
        return d

    def test_dead_owner_lock_dropped(self):
        p = subprocess.Popen(["true"])
        p.wait()
        d = self.lock("DK-15", p.pid)
        lift.stale_locks(str(self.home))
        self.assertFalse(d.exists())

    def test_live_owner_lock_kept(self):
        d = self.lock("DK-16", os.getpid())
        lift.stale_locks(str(self.home))
        self.assertTrue(d.exists())

    def test_dry_run_keeps_lock(self):
        p = subprocess.Popen(["true"])
        p.wait()
        d = self.lock("DK-17", p.pid)
        lines = lift.stale_locks(str(self.home), act=False)
        self.assertTrue(d.exists())
        self.assertIn("снялся бы", " ".join(lines))


class OrphanProcCase(unittest.TestCase):
    """Образец сиротского процесса ловит нагрузку сценария."""

    def test_pattern_matches_load(self):
        line = ("/Library/Developer/CommandLineTools/Library/Frameworks/"
                "Python3.framework/Versions/3.9/Resources/Python.app/Contents/"
                "MacOS/Python -c while True: pass")
        self.assertTrue(any(rx.search(line) for rx in lift.ORPHAN_PATTERNS))

    def test_pattern_matches_run_tree(self):
        line = "go test ./... /private/var/folders/x1/T/shipctl-merge-546185521/tree"
        self.assertTrue(any(rx.search(line) for rx in lift.ORPHAN_PATTERNS))

    def test_pattern_spares_ordinary_python(self):
        self.assertFalse(any(rx.search("python3 manage.py runserver")
                             for rx in lift.ORPHAN_PATTERNS))


class TriesCase(unittest.TestCase):
    """Потолок попыток: строку, чья сессия не удерживается, заход бросает.

    На живом прогоне сессия строки в Check гибла сразу после старта, и заказ
    при этом уходил успешно. Строка возвращалась в находки следующего захода, и
    без потолка её дёргало бы вечно.
    """

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home)]))

    def test_tries_counted(self):
        for n in range(3):
            got = lift.mark_lifted("/root", "DK-20", home=str(self.home),
                                   now=time.time() - lift.SETTLE * (3 - n) - 60)
        self.assertEqual(got, 3)

    def test_spent_row_left_alone(self):
        for n in range(lift.LIFT_TRIES):
            lift.mark_lifted("/root", "DK-21", home=str(self.home),
                             now=time.time() - lift.SETTLE - 60)
        call = Fake(board([row("DK-21", "проверка", "сессии нет, брошена")]))
        lines, raised = lift.lift_root("/root", call=call, taskctl="taskctl",
                                       home=str(self.home))
        self.assertEqual(raised, 0)
        self.assertIn("сессия не удержалась", " ".join(lines))
        self.assertIn("ждёт человека", " ".join(lines))

    def test_forgotten_mark_starts_over(self):
        lift.mark_lifted("/root", "DK-22", home=str(self.home),
                         now=time.time() - lift.FORGET - 60)
        self.assertEqual(lift.marks(home=str(self.home)), {})


if __name__ == "__main__":
    unittest.main()
