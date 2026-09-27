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
import unittest.mock
from pathlib import Path

import lift


def board(rows, key="in-progress", backlog=None):
    """Ответ `taskctl list --json` с названными строками."""
    return json.dumps({"prefix": "DK", "sections": [
        {"key": "backlog", "title": "Backlog", "rows": backlog or []},
        {"key": key, "title": "In progress", "rows": rows},
    ]}, ensure_ascii=False)


def stamp(age=0):
    """Метка времени строки входа, той же формой, какой её пишет панель: сейчас
    либо на age секунд раньше."""
    return time.strftime(lift.LINE_STAMP, time.localtime(time.time() - age))


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

    def test_check_under_human_left_alone(self):
        """Строка Check с приёмкой человека подъёму не подлежит: голова на ней
        останавливается сама. Агентская приёмка в Check поднимается по-прежнему."""
        human = row("DK-30", "проверка", "сессии нет, брошена")
        human.update({"_section": "check", "accept": "mixed"})
        agent = row("DK-31", "проверка", "сессии нет, брошена")
        agent.update({"_section": "check", "accept": "agent"})
        work = row("DK-32", "разработка", "сессии нет, брошена")
        work.update({"_section": "in-progress", "accept": "user"})
        self.assertEqual([r["id"] for r in lift.orphan_rows([human, agent, work])],
                         ["DK-31", "DK-32"])

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

    def test_pattern_spares_work_tree_build(self):
        """Прогон человека в дереве задачи под образец не идёт.

        Рабочее дерево зовётся devkit-dk-<ID>, и его имя лежит в аргументах
        обычного `go test`. Образец по голому имени унёс бы живой прогон под
        сигнал, стоит его родителю уйти в pid 1.
        """
        for line in ("go test ./... /Users/x/projects/devkit-dk-1157/tools/devkitctl",
                     "go build github.com/x/devkit-dk-1157/tools/taskctl"):
            self.assertFalse(any(rx.search(line) for rx in lift.ORPHAN_PATTERNS), line)

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


class TickCase(unittest.TestCase):
    """Врезка в тик сторожка отдаёт строки отчёта, а не пару.

    Тик складывает ответы работ в один список и пишет их построчно. Пара,
    возвращённая туда как есть, роняла весь тик на конкатенации списка со
    строкой, и сторожок молчал до починки.
    """

    def test_watch_work_returns_lines(self):
        import watch
        lines = watch.lift_rows("/root", call=Fake(board([])), taskctl="taskctl")
        self.assertIsInstance(lines, list)
        for ln in lines:
            self.assertIsInstance(ln, str)


class TreesCase(unittest.TestCase):
    """Уборка деревьев прогона: находки считает разбор доктора, снимает он же.

    Уборка необратима, каталог уходит с диска вместе с записью о дереве, и
    поведение сухого прогона тут важнее прочего.
    """

    def setUp(self):
        import runtrees
        self.mod = runtrees
        self.found = [("/T/shipctl-merge-1/tree", "/repo", "/T/shipctl-merge-1/tree")]
        self.dropped = []
        self.old_ab = runtrees.abandoned
        self.old_drop = runtrees.drop
        runtrees.abandoned = lambda *a, **kw: self.found
        runtrees.drop = lambda path, repo, tree: self.dropped.append(path)

        def back():
            runtrees.abandoned = self.old_ab
            runtrees.drop = self.old_drop
        self.addCleanup(back)

    def test_act_drops_trees(self):
        lines = lift.stale_trees()
        self.assertEqual(self.dropped, ["/T/shipctl-merge-1/tree"])
        self.assertIn("снято брошенных деревьев прогона 1", " ".join(lines))

    def test_dry_run_keeps_trees(self):
        lines = lift.stale_trees(act=False)
        self.assertEqual(self.dropped, [])
        self.assertIn("снялись бы", " ".join(lines))

    def test_nothing_found(self):
        self.found = []
        self.assertIn("брошенных деревьев прогона нет", " ".join(lift.stale_trees()))

    def test_broken_discovery_does_not_break_sweep(self):
        """Отказ разбора уборку не роняет: замки и процессы убираются дальше."""
        def boom(*a, **kw):
            raise RuntimeError("разбор отказал")
        self.mod.abandoned = boom
        lines = lift.stale_trees()
        self.assertIn("посчитать не вышло", " ".join(lines))


class ReplyCase(unittest.TestCase):
    """Лежащая во входе чата реплика человека это повод для подъёма в любом
    статусе строки (DK-1194).

    Прежде такую строку не брал никто. Реплика в задачу, стоящую в Check с
    приёмкой человека, пролежала во входе двадцать минут: панель зовёт подъём
    только у припаркованной вопросом строки, обход ждущих смотрит на Blocked, а
    подъём сирот проверенную строку с приёмкой человека пропускает нарочно.
    """

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home), str(self.root)]))
        self.hook = lift.chat_hook()
        self.assertIsNotNone(self.hook, "подхват реплики hooks/chat-in.py не загрузился")

    def lying(self, tid, text="продолжай, пожалуйста", age=0, head=True):
        """Реплика во входе чата задачи, той же строкой, какую пишет панель.
        Возраст двигает метку времени в начале строки, а без head строка идёт
        рукописной, без метки вовсе."""
        d = self.root / ".devkit" / "chat"
        d.mkdir(parents=True, exist_ok=True)
        line = ("%s, из дашборда: %s" % (stamp(age), text)) if head else text
        (d / ("task-%s.in" % tid)).write_text(line + "\n", encoding="utf-8")

    def addressed(self, tid, sid="aaaa-1111"):
        """Реплика с адресатом: написана живому окну, а не задаче."""
        d = self.root / ".devkit" / "chat"
        d.mkdir(parents=True, exist_ok=True)
        (d / ("task-%s.in" % tid)).write_text(
            "%s, сессии %s, из дашборда: продолжай\n" % (stamp(), sid), encoding="utf-8")

    def checked(self, tid="DK-40", **extra):
        """Строка в Check с приёмкой человека: ровно та, что лежала молча."""
        r = row(tid, "проверка", "сессии нет, брошена")
        r.update({"_section": "check", "accept": "mixed"})
        r.update(extra)
        return r

    def find(self, rows):
        return [r["id"] for r in lift.reply_rows(str(self.root), rows,
                                                home=str(self.home), hook=self.hook)]

    def test_reply_in_check_under_human(self):
        rows = [self.checked()]
        self.assertEqual(lift.orphan_rows(rows), [],
                         "строка Check с приёмкой человека сиротой не считается")
        self.lying("DK-40")
        self.assertEqual(self.find(rows), ["DK-40"])

    def test_empty_input_is_not_a_reason(self):
        self.assertEqual(self.find([self.checked()]), [])

    def test_addressed_line_is_not_a_reason(self):
        """Адрес у реплики это адресат разговора: строка живому окну подъёма не
        просит, его ждёт та сессия, которой она написана."""
        self.addressed("DK-40")
        self.assertEqual(self.find([self.checked()]), [])

    def test_live_head_left_alone(self):
        """Живая голова прочитает реплику сама, и второй ей не надо. Замок тут
        точнее выдержки: панель поднимает голову сразу и следа не оставляет."""
        self.lying("DK-40")
        lock = self.home / "task-DK-40.lock"
        lock.mkdir(parents=True)
        (lock / "pid").write_text("%d\n" % os.getpid(), encoding="utf-8")
        self.assertEqual(self.find([self.checked()]), [])

    def test_dead_head_lets_lift(self):
        """Замок мёртвого владельца подъёму не мешает: работы за ним нет."""
        self.lying("DK-40")
        lock = self.home / "task-DK-40.lock"
        lock.mkdir(parents=True)
        (lock / "pid").write_text("999999\n", encoding="utf-8")
        self.assertEqual(self.find([self.checked()]), ["DK-40"])

    def test_backlog_row_not_read(self):
        """Строку Backlog реплика не поднимает: работу по ней никто не брал, и
        голова на ней шла бы мимо вердикта и перевода в работу. Секции те же,
        что у сирот: доска отдаёт заходу только In progress и Check, и до
        разбора входов строка Backlog не доезжает."""
        self.lying("DK-41")
        call = Fake(board([], backlog=[row("DK-41", "постановка", "сессии нет")]))
        lines, raised = lift.lift_root(str(self.root), call=call, taskctl="taskctl",
                                       home=str(self.home))
        self.assertEqual(raised, 0)
        self.assertFalse([a for a in call.calls if "run" in a], "строку Backlog подняли")
        self.assertIn("реплик без адресата нет", " ".join(lines))

    def test_stale_reply_left_alone(self):
        """Реплика старше суток головы не поднимает. Первый тик после выката
        нашёл бы во входах реплики недельной давности к строкам, которые давно
        никто не ведёт, и потратил бы сессии на ответы, которых человек уже не
        ждёт."""
        self.lying("DK-40", age=lift.REPLY_TTL + 60)
        fresh, stale = lift.split_reply_rows(str(self.root), [self.checked()],
                                             home=str(self.home), hook=self.hook)
        self.assertEqual([r["id"] for r in fresh], [])
        self.assertEqual([r["id"] for r in stale], ["DK-40"])

    def test_reply_within_a_day_is_fresh(self):
        self.lying("DK-40", age=lift.REPLY_TTL - 60)
        self.assertEqual(self.find([self.checked()]), ["DK-40"])

    def test_line_without_a_stamp_is_fresh(self):
        """Рукописной строке без метки срок мерить нечем, и она считается
        свежей: прочитает её первая же поднятая голова."""
        self.lying("DK-40", text="продолжай", head=False)
        self.assertEqual(self.find([self.checked()]), ["DK-40"])

    def test_stale_reply_named_in_report(self):
        """О реплике, пролежавшей срок, заход не молчит: строка отчёта называет
        её, а подъёма не заказывает."""
        self.lying("DK-40", age=lift.REPLY_TTL + 60)
        # Строка под приёмкой человека: сиротой её подъём не считает, и поднять
        # её мог бы только повод реплики.
        r = dict(row("DK-40", "проверка", "сессии нет, брошена"), accept="mixed")
        call = Fake(board([r], key="check"))
        lines, raised = lift.lift_root(str(self.root), call=call, taskctl="taskctl",
                                       home=str(self.home))
        self.assertEqual(raised, 0)
        self.assertFalse([a for a in call.calls if "run" in a], "устаревшую реплику подняли")
        said = " ".join(lines)
        self.assertIn("реплики старше суток", said)
        # Обещание «прочитает ближайшая голова» тут не годится: на строке в Check
        # обычная голова до реплики не доходит, и отчёт даёт готовую команду.
        self.assertIn("taskctl run DK-40 -C %s --reply" % self.root, said)

    def test_goal_row_left_alone(self):
        """У цели своя оболочка, переписку она читает сама."""
        self.lying("DK-40")
        self.assertEqual(self.find([self.checked(title="Цель: пробный цикл")]), [])

    def test_lift_root_raises_with_reply_order(self):
        """Заход поднимает строку с репликой своим заказом: «продолжай» увело бы
        голову в работу мимо слов человека."""
        self.lying("DK-40")
        call = Fake(board([row("DK-40", "проверка", "сессии нет, брошена")], key="check"))
        lines, raised = lift.lift_root(str(self.root), call=call, taskctl="taskctl",
                                       home=str(self.home))
        self.assertEqual(raised, 1)
        orders = [a for a in call.calls if "run" in a]
        self.assertTrue(orders, "заказа не было")
        argv = orders[0]
        # Текст заказа собирает сама лестница по флагу: у захода копии нет.
        self.assertIn("--reply", argv)
        self.assertNotIn("--order", argv)
        self.assertIn("во входе чата лежит реплика человека", " ".join(lines))


class ReplyCallCase(unittest.TestCase):
    """Зов человеку к реплике, которой не досталось головы за три попытки.

    Молчание в ответ человеку неотличимо от штатного ожидания, и об исчерпанном
    потолке зовут громко, с готовой командой подъёма. Зовут один раз: троттлинг
    уведомителя короче тика, и без своей памяти баннер повторялся бы каждые пять
    минут.
    """

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home), str(self.root)]))
        d = self.root / ".devkit" / "chat"
        d.mkdir(parents=True)
        (d / "task-DK-50.in").write_text(
            "%s, из дашборда: продолжай\n" % stamp(), encoding="utf-8")
        # Потолок попыток уже исчерпан, и отметка старше выдержки: иначе строка
        # считалась бы только что поднятой.
        old = time.time() - lift.SETTLE - 60
        for _ in range(lift.LIFT_TRIES):
            lift.mark_lifted(str(self.root), "DK-50", home=str(self.home), now=old)

    def lift(self):
        call = Fake(board([row("DK-50", "проверка", "сессии нет, брошена")], key="check"))
        with unittest.mock.patch("watch.shout", return_value="отправлено") as shout:
            lines, raised = lift.lift_root(str(self.root), call=call, taskctl="taskctl",
                                          home=str(self.home))
        return lines, raised, shout

    def test_spent_reply_calls_human_once(self):
        lines, raised, shout = self.lift()
        self.assertEqual(raised, 0)
        self.assertIn("поднималась %d раза подряд" % lift.LIFT_TRIES, " ".join(lines))
        self.assertEqual(shout.call_count, 1, "зова не было либо он не один")
        title, body = shout.call_args[0][0], shout.call_args[0][1]
        self.assertIn("DK-50", title)
        # Команда с --reply: без флага голова на строке в Check с приёмкой встала
        # бы стопом до первого хода, и реплика лежала бы дальше.
        self.assertIn("taskctl run DK-50 -C %s --reply" % self.root, body)
        # Второй заход о том же молчит: зов состоялся, а строка так и лежит.
        _, _, again = self.lift()
        self.assertEqual(again.call_count, 0, "баннер повторился на следующем тике")


class ReplyRefusalCase(unittest.TestCase):
    """Отказ подъёма по реплике: зовут не на всякий код возврата.

    Занятый замок значит, что голову задачи держит живая работа, и реплику она
    прочитает подхватом. Баннер «реплика лежит недоставленной» там был бы
    неправдой, а сломанная раскладка машины это настоящее молчание в ответ.
    """

    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.home), str(self.root)]))
        d = self.root / ".devkit" / "chat"
        d.mkdir(parents=True)
        (d / "task-DK-60.in").write_text(
            "%s, из дашборда: продолжай\n" % stamp(), encoding="utf-8")

    def lift(self, code):
        """Заход, где заказ подъёма отвечает названным кодом."""
        rows = board([row("DK-60", "проверка", "сессии нет, брошена")], key="check")
        call = Fake(rows)

        def run(argv, **kw):
            call.calls.append(argv)
            if "run" in argv:
                return subprocess.CompletedProcess(argv, code, "слова заказа", "")
            return subprocess.CompletedProcess(argv, 0, rows, "")

        with unittest.mock.patch("watch.shout", return_value="отправлено") as shout:
            lines, raised = lift.lift_root(str(self.root), call=run, taskctl="taskctl",
                                          home=str(self.home))
        return lines, raised, shout

    def test_busy_lock_calls_nobody(self):
        lines, raised, shout = self.lift(3)
        self.assertEqual(raised, 0)
        self.assertIn("подъём отбит кодом 3", " ".join(lines))
        self.assertEqual(shout.call_count, 0, "баннер ушёл при живой голове задачи")

    def test_ladder_call_is_not_doubled(self):
        """Кодом 1 лестница говорит, что человека позвала сама."""
        _, _, shout = self.lift(1)
        self.assertEqual(shout.call_count, 0, "баннер о том же молчании ушёл вторым")

    def test_broken_setup_calls_human(self):
        lines, _, shout = self.lift(2)
        self.assertEqual(shout.call_count, 1, "сломанная раскладка осталась без зова")
        self.assertIn("человек позван к лежащей реплике", " ".join(lines))


if __name__ == "__main__":
    unittest.main()
