#!/usr/bin/env python3
"""Самопроверка реестра чатов (DK-431): строка ~/.devkit/sessions.log про
родившуюся сессию. Разбор события идёт с живого образца SessionStart, а не с
сочинённого JSON, стенд это временное дерево с .git, как у бокового дерева
задачи. Прогон хука идёт подпроцессом с подсунутым stdin, как у остальных
хуков: важно не только что функция считает, но и что команда из settings.json
пишет строку и уходит нулём.
"""
import importlib
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "session-task.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "session-start.json")

sys.path.insert(0, HERE)
import hookio  # noqa: E402
session_task = importlib.import_module("session-task")

SID = "5a750327-a8b5-4d2f-9aab-46cf862d2c47"


def sample():
    with open(SAMPLE, encoding="utf-8") as f:
        return json.load(f)


def start(cwd, session=SID, transcript="/home/t/.claude/projects/p/%s.jsonl" % SID,
          source="startup"):
    return hookio.Start(session=session, cwd=cwd, transcript=transcript, source=source)


def fields(line):
    """Строка журнала парами «ключ значение», как её читает дашборд."""
    parts = line.rstrip("\n").split(" ")
    return dict(zip(parts[1::2], parts[2::2])), parts[0]


class Tree:
    """Дерево с .git на диске: по нему хук и узнаёт корень работы."""

    def __init__(self, tmp, name):
        self.root = os.path.join(tmp, name)
        os.makedirs(self.root)
        with open(os.path.join(self.root, ".git"), "w", encoding="utf-8") as f:
            f.write("gitdir: /dev/null\n")


class TestSample(unittest.TestCase):
    """Форма события живёт на стороне харнеса, и разбор проверяется снятым с
    него живьём JSON."""

    def test_start_is_parsed_whole(self):
        st = hookio.parse_start(hookio.DEFAULT, sample())
        self.assertEqual(st.session, SID)
        self.assertEqual(len(st.session), 36)
        self.assertTrue(st.cwd.endswith("/cap/work"))
        self.assertTrue(st.transcript.endswith(SID + ".jsonl"))
        self.assertEqual(st.source, "startup")

    def test_other_events_are_not_a_start(self):
        # Реестр стоит на своём событии, но чужой JSON доходит до него на
        # стенде и в ручной проверке, и запись по нему была бы враньём.
        for name in ("turn-done.json", "tool-done-bash.json", "notify-permission.json"):
            with open(os.path.join(HERE, "testdata", "claude-code", name), encoding="utf-8") as f:
                self.assertIsNone(hookio.parse_start(hookio.DEFAULT, json.load(f)), name)

    def test_start_is_not_a_turn(self):
        self.assertIsNone(hookio.parse_tool(hookio.DEFAULT, sample()))
        self.assertIsNone(hookio.parse_session(hookio.DEFAULT, sample()))


class TestRecord(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", self.tmp]))

    def test_side_tree_names_the_task_and_the_project(self):
        tree = Tree(self.tmp, "devkit-dk-431")
        f, _ = fields(session_task.record(start(tree.root), env={}))
        self.assertEqual(f["задача"], "DK-431")
        self.assertEqual(f["проект"], "devkit")
        self.assertEqual(f["источник"], "дерево")
        self.assertEqual(f["дерево"], tree.root)

    def test_order_beats_the_tree(self):
        # Конвейер дашборда стартует в главном чекауте, и дерево там называет
        # не ту задачу: заказ обязан её перебить, иначе конвейер DK-9 писался
        # бы работой той задачи, чьё дерево оказалось под рукой.
        tree = Tree(self.tmp, "devkit-dk-431")
        f, _ = fields(session_task.record(start(tree.root), env={"DEVKIT_TASK": "DK-9"}))
        self.assertEqual((f["задача"], f["источник"]), ("DK-9", "заказ"))

    def test_order_in_the_main_checkout(self):
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(start(tree.root), env={"DEVKIT_TASK": "dk-431"}))
        self.assertEqual((f["задача"], f["источник"], f["проект"]), ("DK-431", "заказ", "devkit"))

    def test_junk_order_is_not_a_task(self):
        # Чужая переменная в окружении не должна назначать сессию работой:
        # неверная привязка стоит дороже пустой.
        tree = Tree(self.tmp, "devkit")
        for junk in ("", "  ", "задача", "DK431", "TOOLONGPREFIX-1", "DK-1234567"):
            f, _ = fields(session_task.record(start(tree.root), env={"DEVKIT_TASK": junk}))
            self.assertEqual((f["задача"], f["источник"]), ("-", "-"), junk)

    def test_session_without_a_task_still_writes_a_line(self):
        # Разговор доски идёт из главного чекаута, и молчание про него вернуло
        # бы дашборду угадывание по транскрипту.
        tree = Tree(self.tmp, "devkit")
        f, stamp = fields(session_task.record(start(tree.root), env={}))
        self.assertEqual((f["задача"], f["источник"], f["проект"]), ("-", "-", "devkit"))
        self.assertEqual(f["сессия"], SID)
        self.assertEqual(len(stamp), len("2026-08-18T12:03:11"))

    def test_session_outside_a_git_tree(self):
        f, _ = fields(session_task.record(start(self.tmp), env={}))
        self.assertEqual((f["дерево"], f["задача"], f["проект"]), ("-", "-", "-"))

    def test_tmux_name_comes_from_the_one_who_raised_it(self):
        # Имя chat-<ID>-<n> из записи не вывести, а мера «разговор кончился»
        # смотрит именно на живость tmux-сессии с этим именем.
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(
            start(tree.root), env={"DEVKIT_TASK": "DK-431", "DEVKIT_TMUX": "chat-DK-431-2"}))
        self.assertEqual(f["tmux"], "chat-DK-431-2")
        f, _ = fields(session_task.record(start(tree.root), env={}))
        self.assertEqual(f["tmux"], "-")

    def test_fields_go_in_the_written_order(self):
        # Порядок полей это договор с читателем на go, и сползание любого из
        # них тут обязано быть заметно.
        tree = Tree(self.tmp, "devkit-dk-431")
        line = session_task.record(start(tree.root, source="resume"),
                                   env={"DEVKIT_TMUX": "task-DK-431"}, now=0)
        self.assertEqual(line.split(" ")[1::2],
                         ["сессия", "задача", "проект", "дерево", "транскрипт",
                          "источник", "повод", "tmux", "родитель"])
        self.assertTrue(line.endswith(
            " источник дерево повод resume tmux task-DK-431 родитель -\n"), line)

    def test_parent_session_names_the_one_who_handed_out_the_work(self):
        # Подпроцесс делегирования это не разговор человека, а чужая работа, и
        # список чатов отличает одно от другого только по этому полю.
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(
            start(tree.root), env={"DEVKIT_PARENT_SESSION": "aaa-bbb"}))
        self.assertEqual(f["родитель"], "aaa-bbb")
        f, _ = fields(session_task.record(start(tree.root), env={}))
        self.assertEqual(f["родитель"], "-")

    def test_own_session_is_not_its_own_parent(self):
        # Переменная едет подпроцессу наследованием, и сессия, поднятая из
        # сессии подпроцесса, увидела бы в ней себя.
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(
            start(tree.root), env={"DEVKIT_PARENT_SESSION": SID}))
        self.assertEqual(f["родитель"], "-")


class TestHook(unittest.TestCase):
    """Хук целиком: команда из settings.json на живом событии."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", self.tmp]))
        self.home = os.path.join(self.tmp, "home")
        os.makedirs(self.home)

    def run_hook(self, event, extra=None, args=("--hook",)):
        env = dict(os.environ, HOME=self.home)
        env.pop("DEVKIT_TASK", None)
        env.pop("DEVKIT_TMUX", None)
        env.update(extra or {})
        return subprocess.run([sys.executable, HOOK] + list(args),
                              input=json.dumps(event), capture_output=True,
                              text=True, env=env)

    def log(self):
        path = os.path.join(self.home, ".devkit", "sessions.log")
        if not os.path.exists(path):
            return []
        with open(path, encoding="utf-8") as f:
            return [ln for ln in f.read().split("\n") if ln]

    def test_live_start_writes_one_line(self):
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-431"})
        # Контекст задачи уезжает сессии тем же ходом: в нём правило плана,
        # которое дашборд рисует делениями кольца, а строка доски и файл
        # постановки прибавляются, когда они есть. План ведётся файлом:
        # инструмент TodoWrite харнес в обход разрешений не выдаёт.
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        self.assertIn(".devkit/plans/", r.stdout)
        # Тем же абзацем едет правило отзывчивости: разговор идёт ходами, и
        # получасовой ход в нём неотличим от зависшей сессии.
        self.assertIn("отдавай субагенту", r.stdout)
        lines = self.log()
        self.assertEqual(len(lines), 1)
        f, _ = fields(lines[0])
        self.assertEqual((f["сессия"], f["задача"], f["источник"], f["повод"]),
                         (SID, "DK-431", "заказ", "startup"))

    def test_rebinding_adds_a_second_line(self):
        # Перепривязка это обычная запись, и выигрывает последняя: правкой
        # файла реестр не живёт.
        self.run_hook(sample(), {"DEVKIT_TASK": "DK-431"})
        self.run_hook(sample(), {"DEVKIT_TASK": "DK-438"})
        self.assertEqual(len(self.log()), 2)

    def test_foreign_event_writes_nothing(self):
        with open(os.path.join(HERE, "testdata", "claude-code", "turn-done.json"),
                  encoding="utf-8") as f:
            r = self.run_hook(json.load(f))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.log(), [])

    def test_broken_input_is_a_silent_zero(self):
        # Хук стоит в каждой сессии на машине, и ронять её ради журнала нельзя.
        env = dict(os.environ, HOME=self.home)
        r = subprocess.run([sys.executable, HOOK, "--hook"], input="не json",
                           capture_output=True, text=True, env=env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("Traceback", r.stderr)
        self.assertEqual(self.log(), [])

    def test_unknown_protocol_names_the_reason(self):
        r = self.run_hook(sample(), args=("--hook", "кодекс"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("не заведён", r.stderr)

    def test_without_the_key_it_prints_the_help(self):
        r = self.run_hook(sample(), args=())
        self.assertEqual(r.returncode, 2)
        self.assertIn("--hook", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
