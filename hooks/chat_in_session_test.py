#!/usr/bin/env python3
"""Самопроверка разговоров любой сессии (DK-345): .devkit/chat/<имя>.in в
дереве работы, адресат в самой строке. Стенд у каждого теста свой: временное
дерево с .git-файлом, как у бокового дерева задачи, и пустым реестром целей в
подсунутом HOME, так что разговор цели подхват реплики тут не отвлекает. Прогон
идёт подпроцессом с подсунутым stdin, как у остальных хуков.

Прежние имена носителя (.devkit/mail и <имя>.inbox) держит свой класс: переезд
DK-440 обязан довезти строку, написанную до выката.
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
HOOK = os.path.join(HERE, "chat-in.py")
STAMP = "%Y-%m-%dT%H:%M:%S"

SID = "c0ffee-1111-2222-3333-444455556666"
OTHER = "7c1e9a02-1111-2222-3333-444455556666"


def stamp(shift=0.0):
    return time.strftime(STAMP, time.localtime(time.time() + shift))


class ChatStand:
    """Одно дерево работы с разговорами и HOME без единой цели."""

    # Каталог входов и расширение файла: подкласс подменяет их прежней парой
    # DK-440 и получает тот же набор проверок на старом носителе.
    DIR = "chat"
    SUFFIX = ".in"

    def __init__(self, tmp, name, tree="tree"):
        self.home = os.path.join(tmp, name, "home")
        self.root = os.path.join(tmp, name, tree)
        self.chat = os.path.join(self.root, ".devkit", self.DIR)
        os.makedirs(self.chat)
        # .git файлом, как у бокового дерева задачи (worktree): границу дерева
        # подхват меряет наличием .git, а не его видом.
        with open(os.path.join(self.root, ".git"), "w", encoding="utf-8") as f:
            f.write("gitdir: %s/main/.git\n" % tmp)
        os.makedirs(os.path.join(self.home, ".devkit"))
        # Сессия стенда ведёт задачу DK-1: разговор task-DK-1 принадлежит ей, и
        # безадресную строку оттуда забирает она. Без записи реестра разговор
        # задачи хозяина не имеет, и подхват не отдаёт его никому.
        self.bind(SID, "DK-1")

    def bind(self, session, task, when="2026-08-17T11:00:00", tmux="-"):
        """Строка реестра чатов ~/.devkit/sessions.log: её пишет хук старта
        hooks/session-task.py, а свёртка берёт последнюю запись сессии."""
        with open(os.path.join(self.home, ".devkit", "sessions.log"), "a",
                  encoding="utf-8") as f:
            f.write("%s сессия %s задача %s проект devkit дерево %s "
                    "транскрипт - источник заказ повод startup tmux %s\n"
                    % (when, session, task or "-", self.root, tmux))

    def said(self, name, *lines):
        with open(os.path.join(self.chat, name + self.SUFFIX), "w", encoding="utf-8") as f:
            f.write("".join(line + "\n" for line in lines))

    def hold(self, name, until, session="", task=""):
        """Признак ожидания: срок первой строкой, ниже ждущая сессия и задача.
        Так его пишет инструмент ожидания taskctl ask (internal/chat), а
        однострочный признак цели остаётся законным."""
        body = [until]
        if session:
            body.append("сессия " + session)
        if task:
            body.append("задача " + task)
            body.append('{"questions": [{"text": "поле или чип"}]}')
        with open(os.path.join(self.chat, name + ".ask"), "w", encoding="utf-8") as f:
            f.write("\n".join(body) + "\n")

    def read(self, name):
        try:
            with open(os.path.join(self.chat, name + self.SUFFIX), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def lock(self, name):
        fd = os.open(os.path.join(self.chat, name + ".lock"),
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
        env.pop("DEVKIT_CHAT_TRACE", None)
        if trace:
            env["DEVKIT_CHAT_TRACE"] = "1"
        return subprocess.run([sys.executable, HOOK, "--hook"], input=json.dumps(event),
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              text=True, env=env)

    def journal(self):
        try:
            with open(os.path.join(self.home, ".devkit", "chat-in.log"), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""


class OldChatStand(ChatStand):
    """Тот же стенд на прежних именах DK-440: каталог .devkit/mail, файл
    <имя>.inbox. Строка, написанная дашбордом до выката, лежит именно так."""

    DIR = "mail"
    SUFFIX = ".inbox"


class ChatCase(unittest.TestCase):
    STAND = ChatStand

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.n = 0

    def stand(self, tree="tree"):
        self.n += 1
        return self.STAND(self.tmp, "s%d" % self.n, tree)

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


class ChatDeliveryTest(ChatCase):
    def test_unaddressed_line_reaches_the_session_that_leads_the_task(self):
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: стой, не туда")
        text = self.added(s.run())
        self.assertIn("разговора task-DK-1", text)
        self.assertIn("стой, не туда", text)
        # Доставка видна снаружи: строка в своём журнале.
        self.assertIn("доставлено строк 1", s.journal())
        # И доставленная строка ушла из разговора: повторный ход молчит.
        self.assertEqual(s.read("task-DK-1"), "")
        self.silent(s.run())

    # Утечка адресации (DK-397 POC). Живой случай: конвейер задачи подняли
    # заново, старую tmux-сессию сняли, а реплика в разговор task-DK-503 легла
    # безадресной строкой во вход основного чекаута. Забрал её первый же ход в
    # том же чекауте, диспетчерская сессия чужой задачи, и человек прочитал её
    # у себя как реплику из чужого разговора. Разговор задачи принадлежит той
    # сессии, что задачу ведёт, и больше никому.

    def test_task_chat_does_not_reach_a_session_of_another_task(self):
        s = self.stand()
        s.bind(OTHER, "DK-9")
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: Продолжай работу")
        self.silent(s.run(session=OTHER))
        self.assertIn("Продолжай работу", s.read("task-DK-1"),
                      "сессия чужой задачи забрала безадресную реплику")
        self.assertIn("разговор принадлежит не этой сессии", s.journal())

    def test_task_chat_does_not_reach_a_session_without_a_task(self):
        # Сессии нет в реестре вовсе: окно человека в том же чекауте. Прежде
        # такой ход забирал безадресную строку задачи наравне с исполнителем.
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: Продолжай работу")
        self.silent(s.run(session=OTHER))
        self.assertIn("Продолжай работу", s.read("task-DK-1"))

    def test_rebound_session_loses_the_task_chat(self):
        # Свёртка реестра по последней записи: сессию перепривязали, и разговор
        # прежней задачи ей больше не принадлежит.
        s = self.stand()
        s.bind(SID, "DK-9", when="2026-08-17T12:30:00")
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: стой, не туда")
        self.silent(s.run())
        self.assertIn("стой, не туда", s.read("task-DK-1"))

    def test_task_worktree_session_takes_the_task_chat(self):
        # Боковое дерево задачи принадлежит ей целиком: разговор task-DK-1 в
        # нём свой по месту, даже когда реестра про сессию не знает.
        s = self.stand(tree="devkit-DK-1")
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: стой, не туда")
        text = self.added(s.run(session=OTHER))
        self.assertIn("стой, не туда", text)

    def test_personal_chat_reaches_only_its_own_session(self):
        # Личный разговор сессии несёт её ID прямо в имени, и чужой ход в том же
        # дереве забирать оттуда нечего.
        s = self.stand()
        s.said("sess-" + OTHER, "2026-08-17 12:00, из дашборда: только тебе")
        self.silent(s.run())
        self.assertIn("только тебе", s.read("sess-" + OTHER),
                      "чужая сессия забрала личный разговор")
        text = self.added(s.run(session=OTHER))
        self.assertIn("только тебе", text)

    def test_addressed_line_goes_only_to_its_session(self):
        s = self.stand()
        line = "2026-08-17 12:00, сессии %s, из дашборда: глянь ревью" % OTHER
        s.said("task-DK-1", line)
        self.silent(s.run())
        self.assertIn("глянь ревью", s.read("task-DK-1"), "чужая сессия забрала реплику")
        text = self.added(s.run(session=OTHER))
        self.assertIn("глянь ревью", text)
        self.assertEqual(s.read("task-DK-1"), "")

    def test_addressed_miss_is_named_in_the_journal_under_trace(self):
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, сессии %s, из дашборда: глянь ревью" % OTHER)
        self.silent(s.run(trace=True))
        self.assertIn("реплика адресована сессии %s" % OTHER, s.journal())

    def test_handwritten_line_reads_by_any_session(self):
        # Имя разговора хозяина не называет: ни task-<ID>, ни sess-<ID>. Такой
        # разговор человек заводит рукой, и читает его любая сессия дерева.
        s = self.stand()
        s.said("note", "рукописная строка без подписи")
        text = self.added(s.run())
        self.assertIn("рукописная строка без подписи", text)
        self.assertEqual(s.read("note"), "")

    def test_mixed_lines_take_only_their_own(self):
        s = self.stand()
        s.said("task-DK-1",
                 "2026-08-17 12:00, из дашборда: всем",
                 "2026-08-17 12:01, сессии %s, из дашборда: лично" % OTHER)
        text = self.added(s.run())
        self.assertIn("всем", text)
        self.assertNotIn("лично", text)
        # Чужая строка осталась лежать ровно одна.
        self.assertIn("лично", s.read("task-DK-1"))
        self.assertNotIn("всем", s.read("task-DK-1"))

    def test_ask_flag_holds_the_chat(self):
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: ответ пришёл")
        s.hold("task-DK-1", stamp(300))
        self.silent(s.run())
        self.assertIn("разговор держит вопрос", s.journal())
        self.assertIn("ответ пришёл", s.read("task-DK-1"))

    def test_ask_flag_holds_only_what_the_waiter_takes(self):
        # Живой признак запирает не весь вход: реплика чужой сессии доезжает
        # ходом как обычно, а безадресную и адресованную ждущему забирает сам
        # ждущий (LLD DK-430, решение 2). Два живых чата по одной задаче лежат
        # в одном входе и не глушат друг друга.
        s = self.stand()
        s.said("task-DK-1",
               "2026-08-17 12:00, из дашборда: ответ задаче",
               "2026-08-17 12:01, сессии %s, из дашборда: это ждущему" % OTHER,
               "2026-08-17 12:02, сессии %s, из дашборда: это окну человека" % SID)
        s.hold("task-DK-1", stamp(300), session=OTHER, task="DK-1")
        text = self.added(s.run())
        self.assertIn("это окну человека", text)
        self.assertNotIn("ответ задаче", text)
        self.assertNotIn("это ждущему", text)
        left = s.read("task-DK-1")
        self.assertIn("ответ задаче", left)
        self.assertIn("это ждущему", left)
        self.assertIn("частичный отказ", s.journal())

    def test_ask_flag_of_the_same_session_holds_its_lines(self):
        # Ждущий и ход это одна внешняя сессия: у субагента своего ID нет.
        # Реплику, которую заберёт ожидание, подхват не крадёт себе.
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, сессии %s, из дашборда: ответ ждущему" % SID)
        s.hold("task-DK-1", stamp(300), session=SID, task="DK-1")
        self.silent(s.run())
        self.assertIn("ответ ждущему", s.read("task-DK-1"))

    def test_stale_ask_does_not_hold(self):
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: ответ пришёл")
        s.hold("task-DK-1", stamp(-300))
        self.added(s.run())

    def test_forever_ask_does_not_hold_its_own_session(self):
        # Признак без срока (DK-715) живёт до ответа, а не до часов, но живого
        # процесса, который бы читал ответ отдельно от подхвата, у него больше
        # нет: --wait и опрос входа ушли из taskctl ask вместе с этой правкой.
        # Держать реплику для несуществующего читателя значит хоронить её:
        # подхват обязан доставить её сам на первом же ходе.
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, сессии %s, из дашборда: ответ ждущему" % SID)
        s.hold("task-DK-1", "-", session=SID, task="DK-1")
        text = self.added(s.run())
        self.assertIn("ответ ждущему", text)
        self.assertEqual(s.read("task-DK-1"), "")

    def test_neighbor_lock_leaves_the_line(self):
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: стой")
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
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: стой")
        self.silent(s.run(agent="Explore"))
        self.assertIn("стой", s.read("task-DK-1"))


class ChatWithGoalTest(ChatCase):
    def test_goal_and_chat_adds_arrive_together(self):
        # Разговор и цель одного дерева не спорят: у цели свои «Входящие» в
        # файле цели, у сессии свой вход, и один ход приносит обе реплики одной
        # добавкой.
        from chat_in_test import Stand
        s = Stand(self.tmp, "goal")
        with open(os.path.join(s.root, ".git"), "w", encoding="utf-8") as f:
            f.write("gitdir: elsewhere\n")
        os.makedirs(os.path.join(s.root, ".devkit", "chat"))
        # Сессия витка ведёт DK-1: разговор задачи принадлежит ей, иначе
        # безадресную строку оттуда не забирает никто.
        with open(os.path.join(s.home, ".devkit", "sessions.log"), "a",
                  encoding="utf-8") as f:
            f.write("2026-08-15T11:00:00 сессия %s задача DK-1 проект devkit "
                    "дерево %s транскрипт - источник заказ повод startup tmux -\n"
                    % (SID, s.root))
        with open(os.path.join(s.root, ".devkit", "chat", "task-DK-1.in"),
                  "w", encoding="utf-8") as f:
            f.write("2026-08-17 12:00, из дашборда: из разговора\n")
        s.incoming("2026-08-15 14:03, из дашборда: из цели")
        p = s.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        rows = [l for l in p.stdout.split("\n") if l.strip()]
        self.assertEqual(len(rows), 1, "на stdout не одна запись: %r" % p.stdout)
        text = json.loads(rows[0])["hookSpecificOutput"]["additionalContext"]
        self.assertIn("из цели", text)
        self.assertIn("из разговора", text)


class ChatTreeTest(ChatCase):
    def test_tree_boundary_keeps_the_neighbor_chat(self):
        # Два соседних дерева: разговор одного не читается сессией другого, хотя
        # путь второго лежит в каталоге, где .devkit первого нашёлся бы при
        # подъёме по предкам без границы git.
        first, second = self.stand(), self.stand()
        first.said("task-DK-1", "2026-08-17 12:00, из дашборда: стоп")
        self.silent(second.run())
        self.assertIn("стоп", first.read("task-DK-1"))

    def test_walk_stops_at_the_nearest_git(self):
        # Ход в подкаталоге дерева находит свой разговор, а не разговор предка.
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: стоп")
        deep = os.path.join(s.root, "docs", "lld")
        os.makedirs(deep)
        self.added(s.run(cwd=deep))

    def test_path_outside_any_git_tree_is_silent(self):
        s = self.stand()
        outside = os.path.join(self.tmp, "plain")
        os.makedirs(outside)
        self.silent(s.run(cwd=outside))

    def test_tree_without_the_chat_dir_is_silent(self):
        s = self.stand()
        shutil.rmtree(s.chat)
        self.silent(s.run())


class OldNamesTest(ChatDeliveryTest):
    """Тот же набор проверок доставки на прежнем носителе DK-440. Реплика,
    написанная дашбордом за минуту до выката, лежит в .devkit/mail/<имя>.inbox,
    и подхват обязан её довезти: иначе переезд имён теряет строку молча."""

    STAND = OldChatStand


class BothDirsTest(ChatCase):
    def test_old_and_new_lines_arrive_in_one_turn(self):
        # Каталоги живут рядом ровно в момент выката: новую строку уже пишет
        # дашборд с новыми именами, старая лежит с прошлого запуска.
        s = self.stand()
        s.said("task-DK-1", "2026-08-17 12:00, из дашборда: новая строка")
        old = os.path.join(s.root, ".devkit", "mail")
        os.makedirs(old)
        with open(os.path.join(old, "task-DK-1.inbox"), "w", encoding="utf-8") as f:
            f.write("2026-08-17 11:59, из дашборда: старая строка\n")
        text = self.added(s.run())
        self.assertIn("новая строка", text)
        self.assertIn("старая строка", text)
        self.assertEqual(s.read("task-DK-1"), "")
        self.assertFalse(os.path.exists(os.path.join(old, "task-DK-1.inbox")))


if __name__ == "__main__":
    unittest.main(verbosity=0)
