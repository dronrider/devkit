"""Тесты сторожка цикла цели.

Стенд тут маленький и свой: дом с реестром, проект с доской и журналом
запусков. Уведомитель и launchctl зовутся подставным запускателем, потому что
проверяется решение сторожка (звать или молчать), а не то, как выглядит баннер;
живой канал уведомлений проверяет notify_test.py.
"""
import io
import os
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta
from pathlib import Path

import testenv
import watch

GOAL = "DK-900"
BOARD = """# Задачи стенда

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
%s

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
%s
"""
ROW = "| %s | Цель: стенд | task | P1 | 60 (50+5+3+0+2) | XL | [tasks/%s.md](tasks/%s.md) |"


def stamp(when):
    return when.strftime(watch.STAMP)


class Fake:
    """Подставной запускатель: помнит вызовы и отвечает заданным кодом."""

    def __init__(self, code=0, out=""):
        self.calls = []
        self.code = code
        self.out = out

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        return subprocess.CompletedProcess(argv, self.code, self.out, None)

    def argv_with(self, needle):
        return [a for a in self.calls if any(needle in str(x) for x in a)]


class Stand(unittest.TestCase):
    """Дом с реестром и проект с доской, целью в In progress и журналом."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="watch-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit" / "goals").mkdir(parents=True)
        self.proj = self.dir / "proj"
        (self.proj / ".devkit").mkdir(parents=True)
        (self.proj / "docs" / "tasks").mkdir(parents=True)
        self.now = datetime(2026, 8, 7, 12, 0, 0)
        self.board(in_progress=True)

    def board(self, in_progress=True, goal=GOAL):
        row = ROW % (goal, goal, goal)
        text = BOARD % (row if in_progress else "", "" if in_progress else row)
        (self.proj / "docs" / "TASKS.md").write_text(text, encoding="utf-8")

    def runlog(self, ago_minutes, tool="agentctl", cmd="spend"):
        when = stamp(self.now - timedelta(minutes=ago_minutes))
        with open(str(self.proj / ".devkit" / "log"), "a", encoding="utf-8") as f:
            f.write("%s\t%s\t%s\t0\n" % (when, tool, cmd))

    def entry(self, seen_minutes=None, goal=GOAL, root=None, **extra):
        data = {"goal": goal, "root": str(self.proj if root is None else root),
                "file": str(self.proj / "docs" / "tasks" / ("%s.md" % goal))}
        if seen_minutes is not None:
            data["seen"] = stamp(self.now - timedelta(minutes=seen_minutes))
        data.update(extra)
        path = self.home / ".devkit" / "goals" / ("%s-стенд.watch" % goal)
        watch.write_entry(path, data)
        return path

    def look(self, path, idle=45 * 60, call=None):
        call = Fake() if call is None else call
        called, line = watch.look(path, self.now, idle, call)
        return called, line, call


class LookTest(Stand):

    def test_stopped_loop_shouts(self):
        # Инцидент DK-153: цель в работе, а движения нет часами. Сторожок обязан
        # позвать громко и назвать цель со временем простоя.
        path = self.entry(seen_minutes=200)
        self.runlog(200)
        called, line, call = self.look(path)
        self.assertTrue(called, "вставший цикл не позвал: %s" % line)
        self.assertIn(GOAL, line)
        self.assertIn("3ч", line, "в отчёте нет времени простоя: %s" % line)
        notify = call.argv_with("notify.py")
        self.assertEqual(len(notify), 1, "уведомитель позван не один раз: %s" % call.calls)
        self.assertNotIn("--quiet", notify[0], "зов вставшего цикла обязан быть громким")
        self.assertIn(GOAL, notify[0][4], "в заголовке уведомления нет цели: %s" % notify[0])
        self.assertIn(str(self.proj.name), notify[0][5])
        # Цель едет и полем строки журнала (DK-323): по нему лента дашборда
        # ведёт от вставшего цикла к строке цели, а заголовок она не разбирает.
        self.assertEqual(notify[0][2:4], ["--task", GOAL],
                         "зов ушёл без поля цели: %s" % notify[0])

    def test_shout_names_the_whole_resume_command(self):
        # Сам цикл сторожок не поднимает, поэтому зов обязан нести команду
        # продолжения целиком: путь до оболочки, цель и корень проекта. Человек
        # её выполняет, а не собирает по доке, и путь тут абсолютный, потому
        # что оболочка на машину не уезжает и в PATH её нет.
        path = self.entry(seen_minutes=200)
        self.runlog(200)
        called, line, call = self.look(path)
        self.assertTrue(called, line)
        body = call.argv_with("notify.py")[0][5]
        want = "python3 %s %s -C %s" % (
            watch.DEVKIT / "kit" / "skills" / "goal-loop" / "goal-run.py", GOAL, self.proj)
        self.assertIn(want, body, "в зове нет готовой команды продолжения: %s" % body)

    def test_stop_marked_in_entry(self):
        # Отметка стопа это и защита от баннера каждые пять минут, и тот самый
        # факт «стоп замечен», на который встаёт перезапуск витка.
        path = self.entry(seen_minutes=200)
        self.look(path)
        self.assertEqual(watch.read_entry(path)["stopped"], stamp(self.now))

    def test_second_pass_keeps_quiet(self):
        path = self.entry(seen_minutes=200)
        self.look(path)
        called, line, call = self.look(path)
        self.assertFalse(called, "по тому же стопу позвали второй раз: %s" % line)
        self.assertEqual(call.calls, [])
        self.assertIn("уже позвали", line)

    def test_live_loop_is_silent(self):
        # Живой цикл дёргать нельзя: утилита звалась только что.
        path = self.entry(seen_minutes=1)
        self.runlog(1)
        called, line, call = self.look(path)
        self.assertFalse(called, "живой цикл позвал: %s" % line)
        self.assertEqual(call.calls, [])
        self.assertIn("тихо", line)

    def test_movement_clears_the_mark(self):
        path = self.entry(seen_minutes=200, stopped=stamp(self.now - timedelta(minutes=100)))
        self.runlog(1)
        called, line, _ = self.look(path)
        self.assertFalse(called)
        self.assertNotIn("stopped", watch.read_entry(path),
                         "цикл поехал, а отметка стопа осталась: %s" % line)

    def test_run_log_beats_stale_gate_line(self):
        # Движение меряется по журналу запусков: строка гейта старая, но виток
        # только что звал taskctl, и это движение.
        path = self.entry(seen_minutes=200)
        self.runlog(200)
        self.runlog(2, tool="taskctl", cmd="move")
        called, line, _ = self.look(path)
        self.assertFalse(called, "виток двигался, а сторожок позвал: %s" % line)

    def test_no_run_log_falls_back_to_gate(self):
        path = self.entry(seen_minutes=200)
        called, line, _ = self.look(path)
        self.assertTrue(called, "без журнала запусков цель осталась без надзора: %s" % line)

    def test_goal_out_of_progress_drops_the_entry(self):
        path = self.entry(seen_minutes=200)
        self.board(in_progress=False)
        called, line, call = self.look(path)
        self.assertFalse(called, "цель уже не в работе, а сторожок позвал: %s" % line)
        self.assertFalse(path.exists(), "запись снятой цели осталась в реестре")
        self.assertIn("Check", line)
        self.assertEqual(call.calls, [])

    def test_goal_off_the_board_drops_the_entry(self):
        path = self.entry(seen_minutes=200)
        self.board(in_progress=False, goal="DK-901")
        called, _, _ = self.look(path)
        self.assertFalse(called)
        self.assertFalse(path.exists(), "закрытая цель осталась в реестре")

    def test_missing_root_drops_the_entry(self):
        path = self.entry(seen_minutes=200, root=str(self.dir / "нет"))
        called, line, _ = self.look(path)
        self.assertFalse(called)
        self.assertFalse(path.exists())
        self.assertIn("снята", line)

    def test_broken_board_still_watched(self):
        # Доски нет вовсе: проект сломан, и это тем более повод звать, а не
        # снимать цель с надзора.
        os.remove(str(self.proj / "docs" / "TASKS.md"))
        path = self.entry(seen_minutes=200)
        called, line, _ = self.look(path)
        self.assertTrue(called, "проект без доски остался без надзора: %s" % line)
        self.assertTrue(path.exists())


class RunTest(Stand):

    def sweep(self, idle=None, call=None):
        call = Fake() if call is None else call
        out = io.StringIO()
        rc = watch.run(now=self.now, idle=idle, home=self.home, out=out, call=call,
                       shipctl=SHIPCTL)
        return rc, out.getvalue(), call

    def test_exit_code_names_the_find(self):
        self.entry(seen_minutes=200)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 1, out)
        self.entry(seen_minutes=1)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 0, out)

    def test_empty_registry_says_so(self):
        rc, out, call = self.sweep()
        self.assertEqual(rc, 0)
        self.assertIn("целей под надзором нет", out)
        # Единственный вызов пустого тика это съём квоты (DK-633): снимок
        # свежеет и на машине, где целей под надзором нет.
        self.assertEqual([c for c in call.calls if "quota" not in c], [])

    def test_heartbeat_written(self):
        # По этой строке доктор судит, работает ли носитель сторожка.
        self.entry(seen_minutes=1)
        self.sweep()
        beat = (self.home / ".devkit" / "goal-watch.log").read_text(encoding="utf-8")
        self.assertIn("целей под надзором 1", beat)
        self.assertIsNotNone(watch.heartbeat(self.home))

    def test_threshold_from_config(self):
        # Порог настраиваемый: тот же простой при большем пороге зова не даёт.
        self.entry(seen_minutes=50)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 1, out)
        (self.home / ".devkit" / "watch.local").write_text("idle = 90\n", encoding="utf-8")
        self.entry(seen_minutes=50)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 0, "порог из ~/.devkit/watch.local не подхвачен: %s" % out)


TASKCTL = "/bin/подставной-taskctl"
SHIPCTL = "/bin/подставной-shipctl"

PARK_HEAD = """# Задачи стенда

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
%s

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
%s
"""

PARK_ROW = "| %s | %s | task | P1 | 60 (50+5+3+0+2) | XL | [tasks/%s.md](tasks/%s.md) |"


class WakeTest(Stand):
    """Тик будит припаркованные вопросом строки по ответу, лежащему в разговоре."""

    def setUp(self):
        super().setUp()
        self.call = Fake()
        # Цель жива и движется: тик не кричит о простое, будить ему не мешает.
        self.entry(seen_minutes=1)
        self.runlog(1)

    def board_with(self, parked, candidates=()):
        """Доска с припаркованными вопросом строками и кандидатами в работе."""
        progress = [ROW % (GOAL, GOAL, GOAL)] + [
            PARK_ROW % (tid, "Кандидат %s" % tid, tid, tid) for tid in candidates]
        (self.proj / "docs" / "TASKS.md").write_text(
            PARK_HEAD % ("\n".join(progress), "\n".join(parked)), encoding="utf-8")

    def said(self, tree, tid, text="вот схема, продолжай", old=False):
        """Лежащая строка-ответ во входе разговора задачи tid дерева tree.
        Ключ old кладёт её прежними именами DK-440: ответ, написанный до
        выката, обязан будить задачу так же, как написанный после."""
        sub, name = ("mail", "task-%s.inbox") if old else ("chat", "task-%s.in")
        d = Path(tree) / ".devkit" / sub
        d.mkdir(parents=True, exist_ok=True)
        (d / (name % tid)).write_text(
            "2026-08-07 12:00, из дашборда: %s\n" % text, encoding="utf-8")

    def said_to(self, tree, tid, sid, text="это тебе, окно"):
        """Лежащая строка с адресатом сессии: реплика живому окну человека в
        дереве задачи, а не ответ самой задаче."""
        d = Path(tree) / ".devkit" / "chat"
        d.mkdir(parents=True, exist_ok=True)
        with (d / ("task-%s.in" % tid)).open("a", encoding="utf-8") as f:
            f.write("2026-08-07 12:00, сессии %s, из дашборда: %s\n" % (sid, text))

    def ask(self, tree, tid, when):
        """Признак ожидания .ask у разговора задачи tid: снимок ожидания
        инструмента."""
        d = Path(tree) / ".devkit" / "chat"
        d.mkdir(parents=True, exist_ok=True)
        (d / ("task-%s.ask" % tid)).write_text(when + "\n", encoding="utf-8")

    def wake(self, taskctl=TASKCTL, call=None):
        call = self.call if call is None else call
        return watch.wake(str(self.proj), self.now, call, taskctl)

    def moved(self):
        return self.call.argv_with("move")

    def test_answer_wakes_only_its_row(self):
        # DoD: на доске три задачи, ответ будит припаркованную вопросом и
        # только её, остальные кандидаты не тронуты.
        self.board_with(
            parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]", "DK-901", "DK-901"),
                    PARK_ROW % ("DK-902", "Ждёт среду [блок: окружение: нет железа]", "DK-902", "DK-902")],
            candidates=["DK-903"])
        self.said(self.proj, "DK-901")
        self.said(self.proj, "DK-902")
        lines = self.wake()
        moved = self.moved()
        self.assertEqual(len(moved), 1, "будить обязана только припаркованная вопросом: %s" % self.call.calls)
        self.assertEqual(moved[0][moved[0].index("move"):moved[0].index("-m")],
                         ["move", "DK-901", "in-progress"])
        self.assertIn("-C", moved[0])
        self.assertTrue(lines[-1].endswith("припаркованных вопросом 1, разбужено 1"), lines[-1])
        self.assertIn("DK-901", lines[0])
        self.assertNotIn("DK-903", " ".join(lines))

    def test_silent_without_answer(self):
        # Нет лежащего ответа, значит будить нечего: ни вызова, ни крика.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        lines = self.wake()
        self.assertEqual(self.call.calls, [])
        self.assertTrue(lines[-1].endswith("припаркованных вопросом 1, разбужено 0"), lines[-1])

    def test_fresh_ask_holds_the_chat(self):
        # Свежий признак ожидания отдаёт разговор инструменту ожидания: ответ
        # заберёт сам ждущий заход, будить рано. Протухший признак будить
        # не мешает.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901")
        self.ask(self.proj, "DK-901", stamp(self.now + timedelta(minutes=2)))
        self.assertEqual(self.wake()[-1].endswith("разбужено 0"), True)
        self.ask(self.proj, "DK-901", stamp(self.now - timedelta(minutes=2)))
        self.assertTrue(self.wake()[-1].endswith("разбужено 1"))

    def test_answer_in_task_tree_wakes(self):
        # Разговор задачи лежит в её дереве ../<проект>-<id>, а не только в
        # корне: исполнитель спрашивает из своего дерева, и ответ кладётся туда
        # же.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.dir / "proj-dk-901", "DK-901")
        self.assertTrue(self.wake()[-1].endswith("разбужено 1"))

    def test_answer_by_old_names_wakes(self):
        # Ответ, легший прежними именами DK-440 (.devkit/mail, task-<ID>.inbox)
        # до выката, будит задачу наравне с новым: иначе переезд имён оставил бы
        # припаркованную строку спать до руки человека.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901", old=True)
        self.assertTrue(self.wake()[-1].endswith("разбужено 1"))

    def test_addressed_line_does_not_wake(self):
        # Ответом задаче считается только безадресная строка: реплика, написанная
        # живому окну человека в дереве задачи, будила бы припаркованную строку
        # впустую, и правка доски уезжала бы в origin ни за чем.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said_to(self.proj, "DK-901", "aaaa-1111")
        lines = self.wake()
        self.assertEqual(self.call.calls, [], "адресная реплика разбудила задачу")
        self.assertTrue(lines[-1].endswith("разбужено 0"), lines[-1])
        self.assertIn("с адресатом сессии", " ".join(lines))

    def test_unaddressed_line_wakes_past_addressed(self):
        # Во входе лежат обе строки: адресная реплика окну и безадресный ответ
        # задаче. Будит вторая, и адресная её не заслоняет.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said_to(self.proj, "DK-901", "aaaa-1111")
        with (self.proj / ".devkit" / "chat" / "task-DK-901.in").open("a", encoding="utf-8") as f:
            f.write("2026-08-07 12:01, из дашборда: вот схема, продолжай\n")
        self.assertTrue(self.wake()[-1].endswith("разбужено 1"))

    def test_addressed_line_in_task_tree_does_not_wake(self):
        # Дерево задачи то же правило: адресная реплика лежит там, где идёт
        # сессия-адресат, и ответом задаче она не становится от места.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said_to(self.dir / "proj-dk-901", "DK-901", "bbbb-2222")
        self.assertTrue(self.wake()[-1].endswith("разбужено 0"))

    def test_broken_chat_hook_stops_waking(self):
        # Подхват не загрузился, разбирать адресата нечем: тик не будит ни одной
        # строки корня и называет причину словами. Молчаливый пропуск был бы
        # неотличим от «ответа никто не писал», а пробуждение вслепую погнало бы
        # в origin правку доски по реплике, написанной чужой сессии.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901")
        lines = watch.wake(str(self.proj), self.now, self.call, TASKCTL, hook=None)
        self.assertEqual(self.call.calls, [], "будили без разбора адресата")
        self.assertEqual(len(lines), 1, lines)
        self.assertIn("подхват реплики", lines[0])
        self.assertIn("не загрузился", lines[0])

    def test_missing_taskctl_is_reported(self):
        # Бинаря нет: строка остаётся в Blocked, тик говорит об этом, а не
        # молчит, и следующий тик повторит.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901")
        lines = self.wake(taskctl="")
        self.assertEqual(self.call.calls, [])
        self.assertIn("taskctl", lines[0])

    def test_taskctl_failure_is_reported(self):
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901")
        lines = self.wake(call=Fake(code=1, out="зависимость не закрыта"))
        self.assertIn("с кодом 1", lines[0])
        self.assertIn("зависимость не закрыта", lines[0])

    def test_run_wakes_through_the_tick(self):
        # Будит сам тик сторожка, без отдельного процесса на задачу: цель
        # жива, крика о простое нет, а припаркованная всё равно встаёт в
        # кандидаты.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901")
        out = io.StringIO()
        rc = watch.run(now=self.now, idle=45 * 60, home=self.home, out=out,
                       call=self.call, taskctl=TASKCTL, shipctl=SHIPCTL)
        text = out.getvalue()
        self.assertEqual(rc, 0, text)
        self.assertIn("разбужена", text)
        beat = (self.home / ".devkit" / "goal-watch.log").read_text(encoding="utf-8")
        self.assertIn("разбужена", beat)

    def test_wake_commits_and_pushes_the_board(self):
        # Будящий move не оставляет правку доски грязной: за будящим никого
        # нет, и коммит с пушем доски обязаны случиться тем же вызовом
        # taskctl, иначе предполёт следующего merge отбился бы о чекаут.
        self.board_with(parked=[PARK_ROW % ("DK-901", "Спрашивает [блок: вопрос: нужна схема]",
                                            "DK-901", "DK-901")])
        self.said(self.proj, "DK-901")
        self.wake()
        moved = self.moved()
        self.assertEqual(len(moved), 1, self.call.calls)
        self.assertIn("docs(tasks): DK-901 разбуждена ответом", moved[0])
        self.assertIn("--push", moved[0])

    def test_taskctl_bin_prefers_path(self):
        self.assertEqual(watch.taskctl_bin(which=lambda name: "/x/%s" % name), "/x/taskctl")

    def test_taskctl_bin_falls_back_to_release_dirs(self):
        # launchd даёт агенту системный PATH без каталога бинарей: без
        # запасного перебора каталогов релиза припаркованная стояла бы до
        # ручного прогона.
        import update
        d = self.dir / "bin"
        d.mkdir()
        exe = d / "taskctl"
        exe.write_text("#!/bin/sh\n", encoding="utf-8")
        exe.chmod(0o755)
        old = update.BIN_DIRS
        update.BIN_DIRS = (str(d),)
        self.addCleanup(setattr, update, "BIN_DIRS", old)
        self.assertEqual(watch.taskctl_bin(which=lambda name: None), str(exe))
        # Пустой каталог установки не выдаёт пути: тик обязан сказать о ней,
        # а не помолчать за отсутствием бинаря.
        update.BIN_DIRS = (str(self.dir / "nowhere"),)
        self.assertEqual(watch.taskctl_bin(which=lambda name: None), "")



class RunRootsTest(Stand):
    """Тик обходит и корни задач из записей этапов: разговор с задачей идёт и
    вне цикла цели."""

    def setUp(self):
        super().setUp()
        self.call = Fake()

    def stage_record(self, tid, root):
        d = self.home / ".devkit" / "runs"
        d.mkdir(parents=True, exist_ok=True)
        (d / ("%s-стенд.run" % tid)).write_text(
            "# этапы задачи %s\nid = %s\nroot = %s\nэтап = уточнение | %s | вопрос: как быть\n"
            % (tid, tid, root, stamp(self.now)), encoding="utf-8")

    def test_root_without_a_goal_is_swept(self):
        # DoD: убитый ход не оставляет строку в In progress. Записи цели у
        # обычной задачи нет вовсе, и без второго источника корней страховка
        # молчала бы ровно там, где вопрос задан вне цикла.
        proj = self.dir / "solo"
        (proj / "docs" / "tasks").mkdir(parents=True)
        (proj / ".devkit" / "chat").mkdir(parents=True)
        (proj / "docs" / "TASKS.md").write_text(
            BOARD % (PARK_ROW % ("DK-902", "Спрашивает", "DK-902", "DK-902"), ""), encoding="utf-8")
        (proj / ".devkit" / "chat" / "task-DK-902.ask").write_text(
            stamp(self.now - timedelta(minutes=5)) + "\nзадача DK-902\n"
            '{"questions": [{"text": "как быть"}]}\n', encoding="utf-8")
        self.stage_record("DK-902", str(proj))
        self.assertIn(str(proj), watch.run_roots(self.home))
        out = io.StringIO()
        watch.run(self.now, 45 * 60, self.home, out, self.call, TASKCTL, SHIPCTL)
        moved = self.call.argv_with("move")
        self.assertEqual(len(moved), 1, "корень без цели не обошли: %s" % self.call.calls)
        self.assertIn("DK-902", moved[0])
        self.assertIn("припаркована страховкой", out.getvalue())

    def test_record_of_a_gone_project_is_skipped(self):
        # Запись этапа переживает сам проект: корня нет, и обходить нечего.
        self.stage_record("DK-903", str(self.dir / "снесённый"))
        self.assertNotIn(str(self.dir / "снесённый"), watch.run_roots(self.home))


class DrainTest(Stand):
    """Тик льёт поезд по каждому корню обхода: у события «очередь
    освободилась» нет получателя, и без сторожка поезд стоит до ручного
    ship (LLD DK-306, решение 4)."""

    def setUp(self):
        super().setUp()
        self.call = Fake()
        self.entry(seen_minutes=1)

    def second_root(self, tid="DK-902"):
        proj = self.dir / "solo"
        (proj / "docs" / "tasks").mkdir(parents=True)
        (proj / ".devkit" / "chat").mkdir(parents=True)
        (proj / "docs" / "TASKS.md").write_text(BOARD % ("", ""), encoding="utf-8")
        d = self.home / ".devkit" / "runs"
        d.mkdir(parents=True, exist_ok=True)
        (d / ("%s-стенд.run" % tid)).write_text(
            "id = %s\nroot = %s\n" % (tid, proj), encoding="utf-8")
        return proj

    def sweep(self, call=None, shipctl=SHIPCTL):
        if call is not None:
            self.call = call
        out = io.StringIO()
        rc = watch.run(now=self.now, idle=45 * 60, home=self.home, out=out,
                       call=self.call, taskctl=TASKCTL, shipctl=shipctl)
        return rc, out.getvalue()

    def drained(self):
        return [c for c in self.call.calls if "--drain" in c]

    def journal(self):
        return (self.home / ".devkit" / "goal-watch.log").read_text(encoding="utf-8")

    def test_each_root_drained_once_with_arguments(self):
        # Разлив идёт по множеству корней обоих обходов, ровно один вызов на
        # корень: цель и запись этапа одного проекта не удваивают разлив.
        solo = self.second_root()
        rc, out = self.sweep()
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.drained(), [
            [SHIPCTL, "-C", str(self.proj), "ship", "--drain"],
            [SHIPCTL, "-C", str(solo), "ship", "--drain"]], self.call.calls)

    def test_silent_zero_exit_stays_out_of_the_journal(self):
        # Пустой поезд и занятая очередь это норма тика, а не событие: строка
        # идёт в отчёт, но не в журнал, иначе журнал тонул бы в «поезд пуст»
        # каждые пять минут.
        rc, out = self.sweep(call=Fake(code=0, out="разлив не нужен: поезд пуст: "
                                                  "после точки последнего выката нет слитых задач"))
        self.assertEqual(rc, 0, out)
        self.assertIn("поезд пуст", out)
        self.assertNotIn("поезд пуст", self.journal())
        self.assertIn("целей под надзором", self.journal())

    def test_deploy_reaches_the_journal(self):
        rc, out = self.sweep(call=Fake(code=0, out="поезд выкачен (DK-901)\nдоска: DK-901 в Check, коммит abc"))
        self.assertEqual(rc, 0, out)
        self.assertIn("поезд выкачен", out)
        self.assertIn("поезд выкачен", self.journal())

    def test_failure_reported_and_sweep_continues(self):
        # Провал разлива не поднимает код тика и не глушит остальные корни:
        # уведомление на провал деплоя шлёт сам shipctl через taskctl fail.
        self.second_root()
        rc, out = self.sweep(call=Fake(code=1, out="выкат поезда упал: деплой не прошёл"))
        self.assertEqual(rc, 0, out)
        self.assertEqual(len(self.drained()), 2, self.call.calls)
        self.assertIn("с кодом 1", out)
        self.assertIn("деплой не прошёл", out)
        self.assertIn("разлив упал", self.journal())

    def test_missing_shipctl_is_reported(self):
        rc, out = self.sweep(shipctl="")
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.drained(), [])
        self.assertIn("бинаря shipctl нет", out)
        self.assertIn("бинаря shipctl нет", self.journal())

    def test_root_without_board_is_skipped(self):
        # Корня без доски разлив не касается: разливать там нечего, и строка
        # об отказе только плодила бы шум.
        proj = self.dir / "голый"
        (proj / "docs").mkdir(parents=True)
        self.entry(goal="DK-904", root=str(proj))
        rc, out = self.sweep()
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.drained(), [
            [SHIPCTL, "-C", str(self.proj), "ship", "--drain"]], self.call.calls)


class ParkStaleTest(Stand):
    """Страховка ожидания: брошенный ход паркует сторожок, живую сессию не
    трогает."""

    def setUp(self):
        super().setUp()
        self.call = Fake()
        self.entry(seen_minutes=1)
        self.runlog(1)

    def board_progress(self, rows):
        (self.proj / "docs" / "TASKS.md").write_text(
            BOARD % ("\n".join([ROW % (GOAL, GOAL, GOAL)] + rows), ""), encoding="utf-8")

    def ask(self, tid, shift, session="", question="нужна схема"):
        """Признак ожидания задачи: срок со сдвигом от «сейчас» стенда, ждущая
        сессия и вопрос текстом, ровно как его пишет taskctl ask."""
        d = self.proj / ".devkit" / "chat"
        d.mkdir(parents=True, exist_ok=True)
        body = [stamp(self.now + timedelta(seconds=shift))]
        if session:
            body.append("сессия " + session)
        body.append("задача " + tid)
        body.append('{"questions": [{"text": "%s"}]}' % question)
        (d / ("task-%s.ask" % tid)).write_text("\n".join(body) + "\n", encoding="utf-8")
        return d / ("task-%s.ask" % tid)

    def registry(self, sid, transcript_age_minutes):
        """Запись реестра чатов со свежим или протухшим транскриптом: по ней
        страховка меряет живость сессии."""
        tr = self.dir / ("%s.jsonl" % sid)
        tr.write_text("{}\n", encoding="utf-8")
        when = (self.now - timedelta(minutes=transcript_age_minutes)).timestamp()
        os.utime(str(tr), (when, when))
        log = self.home / ".devkit" / "sessions.log"
        log.write_text("%s сессия %s задача DK-901 проект стенд дерево %s транскрипт %s "
                       "источник заказ повод startup tmux task-DK-901 родитель -\n"
                       % (stamp(self.now), sid, self.proj, tr), encoding="utf-8")

    def park(self, taskctl=TASKCTL):
        return watch.park_stale(str(self.proj), self.now, self.call, taskctl, home=self.home)

    def test_dead_turn_gets_parked_with_the_question(self):
        # Ход ожидания убит SIGKILL: признак лежит протухший, строка стоит в
        # In progress, а живой сессии за ней нет. Паркует страховка, причиной из
        # того же признака.
        self.board_progress([PARK_ROW % ("DK-901", "Спрашивает", "DK-901", "DK-901")])
        path = self.ask("DK-901", -60, session="aaa-1")
        self.registry("aaa-1", 30)
        lines = self.park()
        moved = self.call.argv_with("move")
        self.assertEqual(len(moved), 1, "брошенный вопрос не припаркован: %s" % self.call.calls)
        self.assertEqual(moved[0][moved[0].index("move"):moved[0].index("--reason")],
                         ["move", "DK-901", "blocked"])
        self.assertIn("вопрос: нужна схема", moved[0])
        self.assertIn("--push", moved[0], "правка доски обязана уехать в origin")
        self.assertFalse(path.exists(), "признак остался лежать: следующий тик паркует повторно")
        self.assertIn("страховкой", lines[0])

    def test_live_session_keeps_the_row(self):
        # Убитый ход ожидания не значит убитой сессии: окно человека работает
        # дальше, и парковка встала бы под руками исполнителя.
        self.board_progress([PARK_ROW % ("DK-901", "Спрашивает", "DK-901", "DK-901")])
        self.ask("DK-901", -60, session="aaa-1")
        self.registry("aaa-1", 2)
        lines = self.park()
        self.assertEqual(self.call.calls, [])
        self.assertIn("сессия aaa-1 жива", lines[0])

    def test_fresh_ask_is_not_touched(self):
        # Срок не вышел: ответа ждёт сам заход, и страховке тут делать нечего.
        self.board_progress([PARK_ROW % ("DK-901", "Спрашивает", "DK-901", "DK-901")])
        self.ask("DK-901", 300, session="aaa-1")
        self.assertEqual(self.park(), [])
        self.assertEqual(self.call.calls, [])

    def test_row_without_ask_is_silent(self):
        # Обычная работа в In progress страховку не касается вовсе.
        self.board_progress([PARK_ROW % ("DK-901", "Работает", "DK-901", "DK-901")])
        self.assertEqual(self.park(), [])
        self.assertEqual(self.call.calls, [])

    def test_unpushed_board_still_drops_the_stamp(self):
        # taskctl отбился на хвосте парковки (доска не уехала в origin), а
        # строка из In progress ушла: признак снимается всё равно, иначе он
        # лежал бы вечно, а неуехавшая доска называется словами.
        self.board_progress([PARK_ROW % ("DK-901", "Спрашивает", "DK-901", "DK-901")])
        path = self.ask("DK-901", -60, session="aaa-1")
        call = Fake(code=1, out="git push: exit status 128")

        def moved(argv, **kw):
            out = call(argv, **kw)
            self.board_progress([])
            return out

        lines = watch.park_stale(str(self.proj), self.now, moved, TASKCTL, home=self.home)
        self.assertFalse(path.exists(), "признак остался лежать после парковки")
        self.assertIn("доска не уехала в origin", lines[0])

    def test_refused_parking_keeps_the_stamp(self):
        # Отказ до правки доски это другое дело: строка стоит в In progress,
        # и признак нужен следующему тику.
        self.board_progress([PARK_ROW % ("DK-901", "Спрашивает", "DK-901", "DK-901")])
        path = self.ask("DK-901", -60, session="aaa-1")
        lines = watch.park_stale(str(self.proj), self.now, Fake(code=1, out="вопросов висит 2 из 2"),
                                 TASKCTL, home=self.home)
        self.assertTrue(path.exists(), "признак снят там, где парковки не было")
        self.assertIn("taskctl отказал", lines[0])

    def test_ask_in_the_task_tree_counts_too(self):
        # Признак бывает и в дереве задачи: брошенный ход шёл оттуда.
        self.board_progress([PARK_ROW % ("DK-901", "Спрашивает", "DK-901", "DK-901")])
        tree = Path(watch.task_tree(str(self.proj), "DK-901")) / ".devkit" / "chat"
        tree.mkdir(parents=True, exist_ok=True)
        (tree / "task-DK-901.ask").write_text(
            stamp(self.now - timedelta(minutes=1)) + "\nзадача DK-901\n", encoding="utf-8")
        self.park()
        self.assertEqual(len(self.call.argv_with("move")), 1,
                         "признак в дереве задачи пропущен: %s" % self.call.calls)


class CloseAgentTest(Stand):
    """Тик доводит до Done агентскую строку Check: кого закрывать, отвечает
    `taskctl closable`, закрытие идёт `taskctl close` с коммитом и пушем."""

    class Answers:
        """Подставной запускатель с ответом на каждую команду taskctl. Стенд
        отвечает за обе стороны разговора: вердикт отбора приходит первым
        вызовом, исход закрытия вторым."""

        def __init__(self, verdict="", verdict_code=0, close_code=0, close_out="", board=None):
            self.calls = []
            self.verdict = verdict
            self.verdict_code = verdict_code
            self.close_code = close_code
            self.close_out = close_out
            self.board = board  # доска стенда: закрытие уносит с неё строку

        def __call__(self, argv, **kw):
            self.calls.append(list(argv))
            if "closable" in argv:
                return subprocess.CompletedProcess(argv, self.verdict_code, self.verdict, None)
            if self.board is not None:
                tid = argv[argv.index("close") + 1]
                text = self.board.read_text(encoding="utf-8")
                self.board.write_text(
                    "\n".join(l for l in text.splitlines() if not l.startswith("| " + tid)) + "\n",
                    encoding="utf-8")
            return subprocess.CompletedProcess(argv, self.close_code, self.close_out, None)

        def argv_with(self, needle):
            return [a for a in self.calls if needle in a]

    def setUp(self):
        super().setUp()
        self.check_rows(["DK-902", "DK-903"])

    def check_rows(self, ids):
        """Доска стенда, где перечисленные строки стоят в Check."""
        rows = "".join(PARK_ROW % (i, "Готова к сдаче", i, i) + "\n" for i in ids)
        text = PARK_HEAD % (ROW % (GOAL, GOAL, GOAL), "")
        head, sep, tail = text.partition("## Check")
        body, sep2, rest = tail.partition("\n\n## Backlog")
        (self.proj / "docs" / "TASKS.md").write_text(
            head + sep + body + "\n" + rows + sep2 + rest, encoding="utf-8")

    def close(self, answers):
        return watch.close_agent(str(self.proj), answers, TASKCTL)

    def test_ready_row_is_closed_with_push(self):
        # Вердикт назвал строку готовой: тик зовёт close тем же порядком, что
        # страховка парковки зовёт move, с сообщением коммита и пушем доски.
        a = self.Answers(verdict="DK-902\n")
        lines = self.close(a)
        closed = a.argv_with("close")
        self.assertEqual(len(closed), 1, "готовая строка не закрыта: %s" % a.calls)
        self.assertEqual(closed[0][closed[0].index("close"):closed[0].index("-m")],
                         ["close", "DK-902"])
        self.assertIn("--push", closed[0])
        self.assertIn("закрыта тиком", lines[0])

    def test_refused_rows_stay(self):
        # Вердикт отказал всем: закрывать тик не пробует и молчит. Так стоят в
        # Check виды user и mixed, строка без отметки smoke и строка с пустым
        # разделом «Проверка».
        a = self.Answers(verdict="закрывать автоматике нечего\nотказано:\n"
                                 "  DK-902: вид приёмки user: приёмка за человеком\n"
                                 "  DK-903: отметки smoke на последний выкат нет\n")
        self.assertEqual(self.close(a), [])
        self.assertEqual(a.argv_with("close"), [], "тик полез закрывать отказ: %s" % a.calls)

    def test_ready_list_stops_at_refusals(self):
        # Перечень отказов идёт следом за готовыми, и разбор ответа обязан
        # кончиться на первой строке прозы: иначе тик закрыл бы чужую строку.
        a = self.Answers(verdict="DK-902\nотказано:\n  DK-903: вид приёмки mixed: приёмка за человеком\n")
        self.close(a)
        closed = a.argv_with("close")
        self.assertEqual([c[c.index("close") + 1] for c in closed], ["DK-902"],
                         "закрыто не то, что назвал вердикт: %s" % a.calls)

    def test_without_binary_tick_is_silent(self):
        # Спросить вердикт нечем, а закрывать вслепую нельзя: строка дождётся
        # живой сессии.
        a = self.Answers(verdict="DK-902\n")
        self.assertEqual(watch.close_agent(str(self.proj), a, ""), [])
        self.assertEqual(a.calls, [])

    def test_empty_check_asks_nobody(self):
        # Пустой Check это самый частый случай тика, и лишний запуск бинаря
        # каждые пять минут ему не нужен.
        self.check_rows([])
        a = self.Answers(verdict="")
        self.assertEqual(self.close(a), [])
        self.assertEqual(a.calls, [])

    def test_refusal_of_close_is_reported(self):
        # Строка осталась в Check, а close отказал: тик говорит об этом строкой
        # отчёта и вернётся к ней следующим тиком.
        a = self.Answers(verdict="DK-902\n", close_code=1, close_out="XR-1: в архиве уже есть")
        lines = self.close(a)
        self.assertIn("taskctl отказал", lines[0])
        self.assertIn("в архиве уже есть", lines[0])

    def test_unpushed_board_is_named(self):
        # Закрытие прошло, а пуш нет: строка с доски ушла, и отчёт называет
        # неуехавшую доску, чтобы её починил человек.
        a = self.Answers(verdict="DK-902\n", close_code=1, close_out="пуш доски не прошёл",
                         board=self.proj / "docs" / "TASKS.md")
        lines = self.close(a)
        self.assertIn("доска не уехала в origin", lines[0])

    def test_tick_writes_the_closing_to_journal(self):
        # Закрытие видно снаружи журналом сторожка: громкого зова у него нет
        # намеренно, и журнал остаётся единственным следом.
        self.entry(seen_minutes=1)
        self.runlog(1)
        a = self.Answers(verdict="DK-902\n")
        out = io.StringIO()
        watch.run(now=self.now, idle=None, home=self.home, out=out, call=a, taskctl=TASKCTL)
        journal = (self.home / ".devkit" / "goal-watch.log").read_text(encoding="utf-8")
        self.assertIn("DK-902", journal)
        self.assertIn("закрыта тиком", journal)


class ConfigTest(Stand):

    def test_default_and_overrides(self):
        self.assertEqual(watch.conf_idle(self.home), watch.IDLE)
        conf = self.home / ".devkit" / "watch.local"
        conf.write_text("# порог\nidle = 20\n", encoding="utf-8")
        self.assertEqual(watch.conf_idle(self.home), 20 * 60)
        conf.write_text("idle = скоро\n", encoding="utf-8")
        self.assertEqual(watch.conf_idle(self.home), watch.IDLE,
                         "непонятная строка порога обязана падать в умолчание")

    def test_entry_keeps_unknown_keys(self):
        # Перезапуск витка допишет сюда свои ключи, и терять их сторожку нельзя.
        path = self.entry(seen_minutes=200, tries="2")
        self.look(path)
        self.assertEqual(watch.read_entry(path)["tries"], "2")

    def test_run_log_tail_survives_cut_line(self):
        # Журнал читается хвостом, и первая строка хвоста бывает обрезанной.
        with open(str(self.proj / ".devkit" / "log"), "w", encoding="utf-8") as f:
            f.write("x" * (watch.TAIL + 10) + "\n")
        self.runlog(7)
        self.assertEqual(watch.last_run_stamp(str(self.proj)),
                         self.now - timedelta(minutes=7))

    def test_board_section_read(self):
        self.assertEqual(watch.board_section(str(self.proj), GOAL), "In progress")
        self.board(in_progress=False)
        self.assertTrue(watch.board_section(str(self.proj), GOAL).startswith("Check"))


class AgentTest(Stand):
    """Носитель расписания: launchd-агент и находки доктора про него."""

    def setUp(self):
        super().setUp()
        self.main = self.dir / "devkit"
        (self.main / "tools" / "devkitctl").mkdir(parents=True)
        self.plist = self.home / "Library" / "LaunchAgents" / ("%s.plist" % watch.LABEL)

    def check(self, fix=False, call=None, from_main=True, platform="darwin"):
        call = Fake() if call is None else call
        f, d = watch.check(fix=fix, main=self.main, from_main=from_main,
                           home=self.home, platform=platform, call=call)
        return f, d, call

    def beat(self, ago_minutes=1):
        (self.home / ".devkit" / "goal-watch.log").write_text(
            "%s целей под надзором 0, вставших 0\n"
            % stamp(datetime.now() - timedelta(minutes=ago_minutes)), encoding="utf-8")

    def test_missing_agent_is_a_finding(self):
        f, d, _ = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn("doctor --fix", f[0])
        self.assertEqual(d, [])

    def test_fix_installs_and_loads(self):
        f, d, call = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertTrue(self.plist.exists(), "агент не положен")
        text = self.plist.read_text(encoding="utf-8")
        self.assertIn(str(self.main / "tools" / "devkitctl" / "devkitctl.py"), text)
        self.assertIn("<string>watch</string>", text)
        self.assertIn("<integer>%d</integer>" % watch.EVERY, text)
        self.assertTrue(call.argv_with("bootstrap"), "launchd не позван: %s" % call.calls)

    def test_fix_from_worktree_refuses(self):
        # Агент показывает на чекаут, и класть на машину ветку задачи нельзя.
        f, d, call = self.check(fix=True, from_main=False)
        self.assertEqual(d, [])
        self.assertIn("основного чекаута", f[0])
        self.assertFalse(self.plist.exists())
        self.assertEqual(call.calls, [])

    def test_agent_pointing_elsewhere(self):
        self.check(fix=True)
        old = self.plist.read_text(encoding="utf-8").replace(str(self.main), "/старый/чекаут")
        self.plist.write_text(old, encoding="utf-8")
        f, _, _ = self.check()
        self.assertIn("/старый/чекаут", f[0])
        f, d, _ = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertIn(str(self.main), self.plist.read_text(encoding="utf-8"))

    def test_agent_not_loaded(self):
        self.check(fix=True)
        self.beat()
        f, _, _ = self.check(call=Fake(code=1))
        self.assertIn("не поднят", f[0])

    def test_silent_agent_is_a_finding(self):
        self.check(fix=True)
        self.beat(ago_minutes=(watch.HEARTBEAT_MISS * watch.EVERY) // 60 + 5)
        f, _, _ = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn("не отрабатывал", f[0])

    def test_working_agent_is_quiet(self):
        self.check(fix=True)
        self.beat()
        f, d, _ = self.check()
        self.assertEqual((f, d), ([], []))

    def test_other_platform_says_it_has_no_carrier(self):
        f, _, _ = self.check(platform="linux")
        self.assertEqual(f, [], "без целей под надзором на чужой платформе находки нет")
        self.entry(seen_minutes=1)
        f, _, _ = self.check(platform="linux")
        self.assertIn("носителя сторожка", f[0])


class CommandTest(testenv.SandboxCase):
    """Сторожок изнутри devkitctl: команда и её след в журнале запусков."""

    def test_watch_does_not_touch_the_run_log(self):
        # Движение цикла сторожок меряет журналом запусков, и своя строка раз в
        # пять минут выглядела бы там движением на вставшем цикле.
        devdir = self.box.dk / ".devkit"
        devdir.mkdir()
        try:
            rc, out = self.box.dkctl_run("watch")
            self.assertEqual(rc, 0, out)
            self.assertIn("целей под надзором нет", out)
            self.assertFalse((devdir / "log").exists(),
                             "сторожок написал в журнал запусков: %s" % out)
        finally:
            shutil.rmtree(str(devdir), ignore_errors=True)

    def test_watch_finds_a_stopped_goal(self):
        proj = self.box.project("вставший", board=False)
        (proj / ".devkit").mkdir()
        (proj / "docs").mkdir(parents=True, exist_ok=True)
        (proj / "docs" / "TASKS.md").write_text(
            BOARD % (ROW % (GOAL, GOAL, GOAL), ""), encoding="utf-8")
        old = stamp(datetime.now() - timedelta(hours=3))
        (proj / ".devkit" / "log").write_text("%s\tagentctl\tspend\t0\n" % old, encoding="utf-8")
        goals = self.box.home / ".devkit" / "goals"
        goals.mkdir(parents=True, exist_ok=True)
        watch.write_entry(goals / ("%s-стенд.watch" % GOAL),
                          {"goal": GOAL, "root": str(proj), "seen": old})
        self.addCleanup(shutil.rmtree,
                        str(self.box.home / ".devkit" / "goals"), True)
        rc, out = self.box.dkctl_run("watch")
        self.assertEqual(rc, 1, out)
        self.assertIn(GOAL, out)
        self.assertIn("зову", out)
        # Уведомитель позван настоящий, и он молчит про корень во временной
        # директории: стенд ему песочница, а не рабочий проект.
        self.assertIn("уведомление пропущено", out)


class QuotaTick(unittest.TestCase):
    """Съём квоты тем же тиком (DK-633): снимок обеих подписок свежеет и без
    живых сессий, а журнал сторожка видит съём и отказ, но не «всё свежо»."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="watch-quota-"))
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)
        self.home = self.dir / "дом"
        (self.home / ".devkit").mkdir(parents=True)

    def tick(self, fake):
        out = io.StringIO()
        watch.run(now=datetime.now(), idle=45 * 60, home=self.home, out=out,
                  call=fake, taskctl=TASKCTL, shipctl=SHIPCTL)
        return out.getvalue()

    def journal(self):
        path = self.home / ".devkit" / "goal-watch.log"
        return path.read_text(encoding="utf-8") if path.exists() else ""

    def test_tick_calls_refresh_all_if_stale(self):
        # Красный на старом коде: тик вовсе не звал agentctl, и снимок между
        # заходами стоял часами.
        fake = Fake(code=0, out="подписок 2: снято 0, свежих 2, оставлено 0, отказов 0")
        out = self.tick(fake)
        quota = fake.argv_with("--all")
        self.assertEqual(len(quota), 1, fake.calls)
        for part in ("quota", "refresh", "--if-stale"):
            self.assertIn(part, quota[0])
        self.assertIn("снимок квоты", out)

    def test_fresh_snapshots_stay_out_of_the_journal(self):
        # «Всё свежо» капало бы в журнал каждые пять минут, ничего не добавляя.
        self.tick(Fake(code=0, out="подписок 2: снято 0, свежих 2, оставлено 0, отказов 0"))
        self.assertNotIn("снимок квоты", self.journal())

    def test_actual_snap_reaches_the_journal(self):
        out = self.tick(Fake(code=0, out="подписок 2: снято 1, свежих 1, оставлено 0, отказов 0\n"
                                         "харнес glm-code:\nснимок ..."))
        self.assertIn("снято 1", self.journal())
        # Разбор по харнесам остаётся отчёту тика, журналу хватает счёта.
        self.assertNotIn("glm-code", self.journal())
        self.assertIn("харнес glm-code", out)

    def test_kept_snapshot_reaches_the_journal(self):
        # Живой случай 15:59 (DK-633): панель не далась, сторож отката оставил
        # цифры, и такой исход обязан быть виден в журнале, а не сойти за снятый.
        out = self.tick(Fake(code=0, out="подписок 2: снято 0, свежих 1, оставлено 1, отказов 0\n"
                                         "харнес claude-code: снимок не посвежел, съём будет повторён следующим тиком\n"
                                         "панель не далась (вопрос про доверие)"))
        self.assertIn("оставлено 1", self.journal())
        self.assertIn("панель не далась", out)

    def test_failure_reaches_the_journal(self):
        out = self.tick(Fake(code=1, out="ошибка: харнес glm-code: запрос остатка не прошёл"))
        self.assertIn("кодом 1", self.journal())
        self.assertIn("glm-code", self.journal())
        self.assertIn("кодом 1", out)

    def test_missing_agentctl_is_reported(self):
        report, note = watch.quota_snap(call=Fake(), agentctl="")
        self.assertTrue(note)
        self.assertIn("agentctl нет", report)


if __name__ == "__main__":
    unittest.main()
