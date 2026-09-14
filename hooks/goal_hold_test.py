#!/usr/bin/env python3
"""Самопроверка держателя хода цикла цели (DK-971): ход держится, пока цель не
кончилась стоп-маркером, вопросом человеку или воронкой холостых кругов. Стенд
это временный дом с реестром целей и временный проект с доской и файлом цели.
Хук вдобавок гоняется подпроцессом с подсунутым stdin: важно не только что
функция считает, но и что команда из settings.json печатает решение и уходит
нулём.
"""
import importlib
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "goal-hold.py")

sys.path.insert(0, HERE)
import hookio  # noqa: E402
hold = importlib.import_module("goal-hold")

SID = "0ebb6e3b-7d4e-4b8b-8a82-a340dd843209"
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


class Sink:
    """Приёмник решения хука: текст block либо None, когда хук промолчал."""

    def __init__(self):
        self.text = ""

    def write(self, chunk):
        self.text += chunk

    def value(self):
        if not self.text.strip():
            return None
        return json.loads(self.text.strip().split("\n")[0])


class Fake:
    """Подставной запускатель уведомителя: помнит вызовы."""

    def __init__(self):
        self.calls = []

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        return subprocess.CompletedProcess(argv, 0, "", None)


class Stand(unittest.TestCase):

    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="dk971-")
        self.home = os.path.join(self.dir, "home", ".devkit")
        self.goals = os.path.join(self.home, "goals")
        os.makedirs(self.goals)
        self.proj = os.path.join(self.dir, "proj")
        os.makedirs(os.path.join(self.proj, ".devkit"))
        os.makedirs(os.path.join(self.proj, "docs", "tasks"))
        self.env = {hold.DIR_ENV: self.goals}
        self.board(in_progress=True)
        self.goalfile()

    def board(self, in_progress=True):
        row = ROW % (GOAL, GOAL, GOAL)
        text = BOARD % (row if in_progress else "", "" if in_progress else row)
        with open(os.path.join(self.proj, "docs", "TASKS.md"), "w", encoding="utf-8") as f:
            f.write(text)

    def goalfile(self, note="цель стенда"):
        path = os.path.join(self.proj, "docs", "tasks", "%s.md" % GOAL)
        with open(path, "w", encoding="utf-8") as f:
            f.write("# %s\n\n## Журнал\n\n- 2026-09-13 12:00, %s; continue\n" % (GOAL, note))
        return path

    def entry(self, session=SID, carrier="chat", **extra):
        data = {"goal": GOAL, "root": self.proj, "session": session, "carrier": carrier,
                "file": os.path.join(self.proj, "docs", "tasks", "%s.md" % GOAL)}
        data.update(extra)
        path = os.path.join(self.goals, "%s-стенд.watch" % GOAL)
        hold.write_entry(path, data)
        return path

    def event(self, session=SID, active=False):
        return hookio.Agent(kind=hookio.TURN_DONE, session=session, cwd=self.proj,
                            transcript="", agent_id="", owner="", job="subagent",
                            agent_type="", description="", command="", output="",
                            message="отчёт витка", jobs=(), active=active)

    def stop(self, session=SID, call=None, active=False):
        """Конец хода через держателя: (решение либо None, запускатель)."""
        sink, call = Sink(), Fake() if call is None else call
        hold.handle(self.event(session=session, active=active), self.env, sink, call)
        return sink.value(), call

    def journal(self):
        with open(os.path.join(self.home, "goal-hold.log"), encoding="utf-8") as f:
            return f.read()

    def saylog(self, text="DK-901 отдан исполнителю"):
        with open(os.path.join(self.proj, ".devkit", "goal-%s.log" % GOAL), "a",
                  encoding="utf-8") as f:
            f.write("2026-09-13T12:30:00 %s\n" % text)


class HoldTest(Stand):

    def test_turn_is_held_while_the_goal_runs(self):
        # Тот самый случай DK-971: сессия отчиталась о сделанном и отдала ход,
        # хотя цель стоит в работе. Ход обязан вернуться в работу.
        self.entry()
        decision, _ = self.stop()
        self.assertIsNotNone(decision, "ход отдан посреди цели")
        self.assertEqual(decision["decision"], "block")
        self.assertIn(GOAL, decision["reason"])
        self.assertIn("стоп-маркер", decision["reason"])

    def test_foreign_session_is_not_held(self):
        # Соседняя сессия того же проекта цели не ведёт, и держать её нельзя.
        self.entry(session="другая-сессия")
        decision, _ = self.stop()
        self.assertIsNone(decision, "держатель взял чужую сессию: %s" % decision)

    def test_no_goal_at_all_is_not_held(self):
        decision, _ = self.stop()
        self.assertIsNone(decision, "держатель взял сессию без цели: %s" % decision)

    def test_shell_turn_is_not_held(self):
        # Виток оболочки goal-run кончается маркером и ход отдаёт: решение block
        # закрутило бы клиента в цикле.
        self.entry(carrier="shell")
        decision, _ = self.stop()
        self.assertIsNone(decision, "держатель взял виток оболочки: %s" % decision)
        self.assertIn("оболочка", self.journal())

    def test_stop_marker_releases_the_turn(self):
        # Стоп-маркер это законный конец работы над целью.
        self.entry(marker="wait-human")
        decision, _ = self.stop()
        self.assertIsNone(decision, "ход держится после стоп-маркера: %s" % decision)
        self.assertIn("wait-human", self.journal())

    def test_continue_marker_keeps_holding(self):
        # Маркер продолжения работу не кончает: цикл едет дальше.
        self.entry(marker="continue")
        decision, _ = self.stop()
        self.assertIsNotNone(decision, "ход отдан по маркеру continue")

    def test_goal_out_of_progress_releases_the_turn(self):
        # Цель ушла в Check: работы за ней нет.
        self.entry()
        self.board(in_progress=False)
        decision, _ = self.stop()
        self.assertIsNone(decision, "ход держится у цели не в работе: %s" % decision)
        self.assertIn("Check", self.journal())

    def test_goal_off_the_board_releases_the_turn(self):
        # Замечание ревью DK-971: строку закрытой цели `taskctl close` уносит в
        # архив, и на доске её нет вовсе. Держать сессию по закрытой цели
        # нельзя, а проверка истинности раздела держала.
        self.entry()
        with open(os.path.join(self.proj, "docs", "TASKS.md"), "w", encoding="utf-8") as f:
            f.write(BOARD % ("", ""))
        decision, _ = self.stop()
        self.assertIsNone(decision, "ход держится по закрытой цели: %s" % decision)
        self.assertIn("строки цели на доске нет", self.journal())

    def test_project_without_a_board_keeps_holding(self):
        # Доски нет вовсе: судить по ней нечего, и цель остаётся под держателем,
        # как остаётся под надзором сторожка.
        self.entry()
        os.remove(os.path.join(self.proj, "docs", "TASKS.md"))
        decision, _ = self.stop()
        self.assertIsNotNone(decision, "пропавшая доска отпустила ход")

    def test_question_to_the_human_releases_the_turn(self):
        # Вопрос, без которого работа не едет, это законный конец хода: рядом с
        # разговором лежит признак ожидания этой сессии.
        self.entry()
        d = os.path.join(self.proj, ".devkit", "chat")
        os.makedirs(d)
        with open(os.path.join(d, "task-DK-901.ask"), "w", encoding="utf-8") as f:
            f.write("без срока\nсессия: %s\nзадача: DK-901\nкакой из двух путей берём\n" % SID)
        decision, _ = self.stop()
        self.assertIsNone(decision, "ход держится на вопросе человеку: %s" % decision)
        self.assertIn("ждёт ответа", self.journal())

    def test_question_of_another_session_does_not_release(self):
        # Признак ожидания соседней сессии этой сессии не касается.
        self.entry()
        d = os.path.join(self.proj, ".devkit", "chat")
        os.makedirs(d)
        with open(os.path.join(d, "task-DK-901.ask"), "w", encoding="utf-8") as f:
            f.write("без срока\nсессия: другая-сессия\nзадача: DK-901\nвопрос\n")
        decision, _ = self.stop()
        self.assertIsNotNone(decision, "чужое ожидание отпустило ход")


class FunnelTest(Stand):

    def test_three_idle_turns_release_the_turn_and_shout(self):
        # Защита от воронки: три конца хода подряд с одним и тем же следом
        # цикла значат, что сессия крутится вхолостую. Число то же, что у
        # оболочки.
        path = self.entry()
        for n in range(hold.IDLE_LIMIT - 1):
            decision, call = self.stop()
            self.assertIsNotNone(decision, "холостой круг %d отдал ход" % (n + 1))
            self.assertEqual(call.calls, [], "уведомитель позван раньше времени")
        decision, call = self.stop()
        self.assertIsNone(decision, "третий холостой круг не отдал ход")
        self.assertEqual(len(call.calls), 1, "человека не позвали: %s" % call.calls)
        self.assertIn("--reason", call.calls[0])
        self.assertIn(GOAL, " ".join(call.calls[0]))
        self.assertIn("воронка", self.journal())
        # Счёт после зова обнуляется: следующий круг работы начинается заново.
        self.assertEqual(hold.read_entry(path)[hold.IDLE_KEY], "1")

    def test_work_resets_the_idle_count(self):
        # Круг, на котором цикл оставил строку журнала, холостым не считается.
        self.entry()
        for _ in range(hold.IDLE_LIMIT + 2):
            self.saylog()
            decision, call = self.stop()
            self.assertIsNotNone(decision, "рабочий круг отдал ход")
            self.assertEqual(call.calls, [], "по рабочему кругу позвали человека")

    def test_goal_journal_record_is_work_too(self):
        # Запись в «Журнале» файла цели это тот же след цикла, что строка
        # журнала: ею кончается работа, о которой отчитываются человеку.
        self.entry()
        for n in range(hold.IDLE_LIMIT + 2):
            self.goalfile(note="взята задача номер %d" % n)
            decision, call = self.stop()
            self.assertIsNotNone(decision, "круг с записью «Журнала» отдал ход")
            self.assertEqual(call.calls, [])

    def test_board_alone_is_not_work(self):
        # Доску в основном чекауте правят и соседние сессии, и по ней холостой
        # круг неотличим от работы (DK-971): в след цикла она не входит.
        self.entry()
        for _ in range(hold.IDLE_LIMIT - 1):
            self.board(in_progress=True)
            self.stop()
        self.board(in_progress=True)
        decision, call = self.stop()
        self.assertIsNone(decision, "правка доски сошла за работу цикла")
        self.assertEqual(len(call.calls), 1)


class CommandTest(Stand):
    """Хук командой: событие приходит на stdin, решение печатается в stdout."""

    def run_hook(self, event, extra=None):
        env = dict(os.environ)
        env["DEVKIT_GOAL_HOLD_DIR"] = self.goals
        env.update(extra or {})
        p = subprocess.run([sys.executable, HOOK, "--hook"], input=json.dumps(event),
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env)
        return p

    def stop_event(self, session=SID):
        return {"hook_event_name": "Stop", "session_id": session, "cwd": self.proj,
                "transcript_path": "", "stop_hook_active": False,
                "last_assistant_message": "виток закончен"}

    def test_hook_prints_the_block(self):
        self.entry()
        p = self.run_hook(self.stop_event())
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(json.loads(p.stdout)["decision"], "block")

    def test_hook_is_silent_without_a_goal(self):
        p = self.run_hook(self.stop_event())
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stdout.strip(), "")

    def test_switch_turns_the_holder_off(self):
        # Выключатель нужен человеку, который ведёт цель руками и хочет спать.
        self.entry()
        p = self.run_hook(self.stop_event(), {"DEVKIT_GOAL_HOLD_OFF": "1"})
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stdout.strip(), "")

    def test_other_events_are_ignored(self):
        # Держатель стоит на конце хода, и чужое событие ему не годится.
        self.entry()
        p = self.run_hook({"hook_event_name": "SubagentStop", "session_id": SID,
                           "cwd": self.proj})
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stdout.strip(), "")


if __name__ == "__main__":
    unittest.main()
