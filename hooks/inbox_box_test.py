#!/usr/bin/env python3
"""Самопроверка ящиков любой сессии (DK-345): .devkit/mail/<имя>.inbox в дереве
работы, адресат в самой строке. Стенд у каждого теста свой: временное дерево с
.git-файлом, как у бокового дерева задачи, и пустым реестром целей в подсунутом
HOME, так что цельная почта цели почтальона не отвлекает. Прогон идёт
подпроцессом с подсунутым stdin, как у остальных хуков.
"""
import fcntl
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
INBOX = os.path.join(HERE, "inbox.py")
STAMP = "%Y-%m-%dT%H:%M:%S"

SID = "c0ffee-1111-2222-3333-444455556666"
OTHER = "7c1e9a02-1111-2222-3333-444455556666"


def stamp(shift=0.0):
    return time.strftime(STAMP, time.localtime(time.time() + shift))


class BoxStand:
    """Одно дерево работы с ящиками и HOME без единой цели."""

    def __init__(self, box, name):
        self.home = os.path.join(box, name, "home")
        self.root = os.path.join(box, name, "tree")
        self.mail = os.path.join(self.root, ".devkit", "mail")
        os.makedirs(self.mail)
        # .git файлом, как у бокового дерева задачи (worktree): границу дерева
        # почтальон меряет наличием .git, а не его видом.
        with open(os.path.join(self.root, ".git"), "w", encoding="utf-8") as f:
            f.write("gitdir: %s/main/.git\n" % box)
        os.makedirs(os.path.join(self.home, ".devkit"))

    def letter(self, name, *lines):
        with open(os.path.join(self.mail, name + ".inbox"), "w", encoding="utf-8") as f:
            f.write("".join(line + "\n" for line in lines))

    def hold(self, name, until):
        with open(os.path.join(self.mail, name + ".ask"), "w", encoding="utf-8") as f:
            f.write(until + "\n")

    def read(self, name):
        try:
            with open(os.path.join(self.mail, name + ".inbox"), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def lock(self, name):
        fd = os.open(os.path.join(self.mail, name + ".lock"),
                     os.O_CREAT | os.O_RDWR, 0o644)
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return fd

    def run(self, cwd=None, session=SID, agent=None, trace=False):
        event = {"session_id": session, "cwd": self.root if cwd is None else cwd,
                 "transcript_path": os.path.join(self.root, "transcript.jsonl"),
                 "hook_event_name": "PostToolUse", "tool_name": "Bash",
                 "tool_input": {"command": "echo ping"},
                 "tool_response": {"stdout": "ping"}}
        if agent:
            event["agent_type"] = agent
            event["agent_id"] = "a1b2c3"
        env = dict(os.environ, HOME=self.home)
        env.pop("DEVKIT_INBOX_TRACE", None)
        if trace:
            env["DEVKIT_INBOX_TRACE"] = "1"
        return subprocess.run([sys.executable, INBOX, "--hook"], input=json.dumps(event),
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              text=True, env=env)

    def journal(self):
        try:
            with open(os.path.join(self.home, ".devkit", "inbox.log"), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""


class BoxCase(unittest.TestCase):
    def setUp(self):
        self.box = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.box, ignore_errors=True)
        self.n = 0

    def stand(self):
        self.n += 1
        return BoxStand(self.box, "s%d" % self.n)

    def added(self, p):
        """Текст добавки контекста из ответа хука, ровно одной записью JSON."""
        self.assertEqual(p.returncode, 0, p.stderr)
        rows = [l for l in p.stdout.split("\n") if l.strip()]
        self.assertEqual(len(rows), 1, "на stdout не одна запись: %r" % p.stdout)
        data = json.loads(rows[0])
        return data["hookSpecificOutput"]["additionalContext"]

    def silent(self, p):
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stdout.strip(), "", "хук ответил там, где доставлять нечего")


class BoxDeliveryTest(BoxCase):
    def test_unaddressed_line_reaches_the_session_of_the_tree(self):
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, из дашборда: стой, не туда")
        text = self.added(s.run())
        self.assertIn("ящика task-DK-1", text)
        self.assertIn("стой, не туда", text)
        # Доставка видна снаружи: строка в своём журнале.
        self.assertIn("доставлено строк 1", s.journal())
        # И доставленная строка ушла из ящика: повторный ход молчит.
        self.assertEqual(s.read("task-DK-1"), "")
        self.silent(s.run())

    def test_addressed_line_goes_only_to_its_session(self):
        s = self.stand()
        line = "2026-08-17 12:00, сессии %s, из дашборда: глянь ревью" % OTHER
        s.letter("task-DK-1", line)
        self.silent(s.run())
        self.assertIn("глянь ревью", s.read("task-DK-1"), "чужая сессия забрала письмо")
        text = self.added(s.run(session=OTHER))
        self.assertIn("глянь ревью", text)
        self.assertEqual(s.read("task-DK-1"), "")

    def test_addressed_miss_is_named_in_the_journal_under_trace(self):
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, сессии %s, из дашборда: глянь ревью" % OTHER)
        self.silent(s.run(trace=True))
        self.assertIn("письмо адресовано сессии %s" % OTHER, s.journal())

    def test_handwritten_line_reads_by_any_session(self):
        s = self.stand()
        s.letter("sess-x", "рукописная строка без подписи")
        text = self.added(s.run())
        self.assertIn("рукописная строка без подписи", text)
        self.assertEqual(s.read("sess-x"), "")

    def test_mixed_letters_take_only_their_own(self):
        s = self.stand()
        s.letter("task-DK-1",
                 "2026-08-17 12:00, из дашборда: всем",
                 "2026-08-17 12:01, сессии %s, из дашборда: лично" % OTHER)
        text = self.added(s.run())
        self.assertIn("всем", text)
        self.assertNotIn("лично", text)
        # Чужая строка осталась лежать ровно одна.
        self.assertIn("лично", s.read("task-DK-1"))
        self.assertNotIn("всем", s.read("task-DK-1"))

    def test_ask_flag_holds_the_box(self):
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, из дашборда: ответ пришёл")
        s.hold("task-DK-1", stamp(300))
        self.silent(s.run())
        self.assertIn("ящик держит вопрос", s.journal())
        self.assertIn("ответ пришёл", s.read("task-DK-1"))

    def test_stale_ask_does_not_hold(self):
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, из дашборда: ответ пришёл")
        s.hold("task-DK-1", stamp(-300))
        self.added(s.run())

    def test_neighbor_lock_leaves_the_letter(self):
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, из дашборда: стой")
        fd = s.lock("task-DK-1")
        try:
            self.silent(s.run())
        finally:
            os.close(fd)
        self.assertIn("стой", s.read("task-DK-1"))
        # Замок ушёл, лежащую строку доставил соседний прогон.
        self.added(s.run())

    def test_subagent_gets_nothing_even_addressed_to_the_tree(self):
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, из дашборда: стой")
        self.silent(s.run(agent="Explore"))
        self.assertIn("стой", s.read("task-DK-1"))


class BoxWithGoalTest(BoxCase):
    def test_goal_and_box_letters_arrive_together(self):
        # Ящик и цель одного дерева не спорят: у цели свои «Входящие» в файле
        # цели, у сессии свой ящик, и один ход приносит обе реплики одной
        # добавкой.
        from inbox_test import Stand
        s = Stand(self.box, "goal")
        with open(os.path.join(s.root, ".git"), "w", encoding="utf-8") as f:
            f.write("gitdir: elsewhere\n")
        os.makedirs(os.path.join(s.root, ".devkit", "mail"))
        with open(os.path.join(s.root, ".devkit", "mail", "task-DK-1.inbox"),
                  "w", encoding="utf-8") as f:
            f.write("2026-08-17 12:00, из дашборда: из ящика\n")
        s.inbox("2026-08-15 14:03, из дашборда: из цели")
        p = s.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        rows = [l for l in p.stdout.split("\n") if l.strip()]
        self.assertEqual(len(rows), 1, "на stdout не одна запись: %r" % p.stdout)
        text = json.loads(rows[0])["hookSpecificOutput"]["additionalContext"]
        self.assertIn("из цели", text)
        self.assertIn("из ящика", text)


class BoxTreeTest(BoxCase):
    def test_tree_boundary_keeps_the_neighbor_mail(self):
        # Два соседних дерева: ящик одного не читается сессией другого, хотя
        # путь второго лежит в каталоге, где .devkit первого нашёлся бы при
        # подъёме по предкам без границы git.
        first, second = self.stand(), self.stand()
        first.letter("task-DK-1", "2026-08-17 12:00, из дашборда: стоп")
        self.silent(second.run())
        self.assertIn("стоп", first.read("task-DK-1"))

    def test_walk_stops_at_the_nearest_git(self):
        # Ход в подкаталоге дерева находит его ящик, а не ящик предка.
        s = self.stand()
        s.letter("task-DK-1", "2026-08-17 12:00, из дашборда: стоп")
        deep = os.path.join(s.root, "docs", "lld")
        os.makedirs(deep)
        self.added(s.run(cwd=deep))

    def test_path_outside_any_git_tree_is_silent(self):
        s = self.stand()
        outside = os.path.join(self.box, "plain")
        os.makedirs(outside)
        self.silent(s.run(cwd=outside))

    def test_tree_without_the_mail_dir_is_silent(self):
        s = self.stand()
        shutil.rmtree(s.mail)
        self.silent(s.run())


if __name__ == "__main__":
    unittest.main(verbosity=0)
