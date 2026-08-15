#!/usr/bin/env python3
"""Самопроверка почтальона inbox.py. Хук гоняется процессом с подсунутым stdin,
как гоняются остальные хуки: код возврата и stdout это ровно то, что видит
харнес, а разбор внутри проверяется через них. Стенд у каждого теста свой:
временный HOME с реестром целей и временный корень цели с файлом цели, доской и
каталогом .devkit, так что ни чужой цели, ни настоящего ~/.devkit прогон не
касается.

Корень стенда лежит под tempfile.mkdtemp, то есть на macOS под /var/folders, и
это не мелочь фикстуры: своего правила про песочницу у почтальона нет нарочно,
и цель с корнем под временным каталогом получает почту наравне с любой другой.
Правило песочницы, заведённое заново, красит тут каждый тест доставки.
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

GOAL_MD = """# DK-100: Цель: синтетическая цель обкатки

## Цель

DoD: стенд отработал.

## Бюджет

бюджет: week_all <= 10

## Задачи цели

Заводит нарезка первым витком.
%s
## Журнал

## Итог

Пишет последний виток.
"""


def stamp(shift=0.0):
    return time.strftime(STAMP, time.localtime(time.time() + shift))


class Stand:
    """Один стенд: HOME с реестром целей и корень цели со своим .devkit."""

    def __init__(self, box, name):
        self.home = os.path.join(box, name, "home")
        self.root = os.path.join(box, name, "proj")
        self.goal = os.path.join(self.root, "docs", "tasks", "DK-100.md")
        self.dev = os.path.join(self.root, ".devkit")
        self.mail = os.path.join(self.dev, "goal-DK-100.mail")
        self.lock = os.path.join(self.dev, "goal-DK-100.lock")
        os.makedirs(os.path.dirname(self.goal))
        os.makedirs(self.dev)
        os.makedirs(os.path.join(self.home, ".devkit", "goals"))
        self.inbox()
        self.register()

    def register(self, goal="DK-100", file=None, **fields):
        """Запись реестра целей, какую пишет гейт бюджета."""
        data = {"goal": goal, "root": self.root,
                "file": self.goal if file is None else file, "seen": stamp()}
        data.update(fields)
        body = "".join("%s = %s\n" % (k, v) for k, v in data.items() if v is not None)
        with open(os.path.join(self.home, ".devkit", "goals",
                               "%s-stand.watch" % goal), "w", encoding="utf-8") as f:
            f.write(body)

    def inbox(self, *lines, section=True):
        """Файл цели с разделом «Входящие» из этих строк."""
        block = ""
        if section:
            block = "\n## Входящие\n\n" + "".join("- %s\n" % l for l in lines)
        with open(self.goal, "w", encoding="utf-8") as f:
            f.write(GOAL_MD % block)

    def shell_lock(self, pid, session=None):
        os.makedirs(self.lock, exist_ok=True)
        with open(os.path.join(self.lock, "pid"), "w", encoding="utf-8") as f:
            f.write("%d\n" % pid)
        if session is not None:
            with open(os.path.join(self.lock, "session"), "w", encoding="utf-8") as f:
                f.write(session + "\n")

    def cycle_log(self, *lines):
        with open(os.path.join(self.dev, "goal-DK-100.log"), "w", encoding="utf-8") as f:
            f.write("".join(l + "\n" for l in lines))

    def marks(self, *rows):
        """Отметки доставки: пары «время сессия» и строка «Входящих»."""
        with open(self.mail, "w", encoding="utf-8") as f:
            for session, line in rows:
                f.write("%s %s\n%s\n" % (stamp(), session, line))

    def run(self, cwd=None, session="c0ffee-1111-2222-3333-444455556666",
            tool="Bash", agent=None, raw=None, trace=False, protocol=None):
        event = {"session_id": session, "cwd": self.root if cwd is None else cwd,
                 "transcript_path": os.path.join(self.root, "transcript.jsonl"),
                 "hook_event_name": "PostToolUse", "tool_name": tool,
                 "tool_input": {"command": "echo ping"},
                 "tool_response": {"stdout": "ping"}}
        if agent:
            event["agent_type"] = agent
            event["agent_id"] = "a1b2c3"
        env = dict(os.environ, HOME=self.home)
        env.pop("DEVKIT_INBOX_TRACE", None)
        if trace:
            env["DEVKIT_INBOX_TRACE"] = "1"
        argv = [sys.executable, INBOX, "--hook"] + ([protocol] if protocol else [])
        return subprocess.run(argv, input=json.dumps(event) if raw is None else raw,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              text=True, env=env)

    def read(self, path):
        try:
            with open(path, encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def journal(self):
        return self.read(os.path.join(self.home, ".devkit", "inbox.log"))

    def marks_text(self):
        return self.read(self.mail)

    def cycle_text(self):
        return self.read(os.path.join(self.dev, "goal-DK-100.log"))


class InboxCase(unittest.TestCase):
    def setUp(self):
        self.box = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.box, ignore_errors=True)
        self.n = 0

    def stand(self):
        self.n += 1
        return Stand(self.box, "s%d" % self.n)

    def dead_pid(self):
        p = subprocess.Popen([sys.executable, "-c", "pass"])
        p.wait()
        return p.pid

    def live_pid(self):
        p = subprocess.Popen(["sleep", "30"])
        self.addCleanup(p.wait)
        self.addCleanup(p.kill)
        return p.pid

    def added(self, p):
        """Текст добавки контекста из ответа хука. Ответ обязан быть ровно
        одной записью JSON: строку журнала цикла печатает --say в свой stdout, и
        подмешайся она сюда, харнес прочитал бы вместо ответа мусор."""
        self.assertEqual(p.returncode, 0, p.stderr)
        rows = [l for l in p.stdout.split("\n") if l.strip()]
        self.assertEqual(len(rows), 1, "на stdout не одна запись: %r" % p.stdout)
        data = json.loads(rows[0])
        return data["hookSpecificOutput"]["additionalContext"]

    def silent(self, p):
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stdout.strip(), "", "хук ответил там, где доставлять нечего")


class DeliveryTest(InboxCase):
    def test_lying_line_goes_into_the_turn(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: посмотри ещё на DK-1")
        text = self.added(s.run())
        self.assertIn("DK-100", text)
        self.assertIn("посмотри ещё на DK-1", text)
        # Отметка встала строкой целиком, вместе с датой и подписью дашборда.
        self.assertIn("2026-08-15 14:03, из дашборда: посмотри ещё на DK-1", s.marks_text())
        self.assertIn("c0ffee-1111-2222-3333-444455556666", s.marks_text())
        # Доставку видно снаружи: строка в журнале цикла и строка в своём журнале.
        self.assertIn("почта: витку доставлена реплика", s.cycle_text())
        self.assertIn("доставлено строк 1", s.journal())

    def test_empty_inbox_says_nothing(self):
        s = self.stand()
        self.silent(s.run())
        self.assertEqual(s.journal(), "")
        s.inbox()
        self.silent(s.run())
        self.assertEqual(s.journal(), "")

    def test_second_tool_turn_does_not_repeat_the_line(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.added(s.run())
        self.silent(s.run())
        self.assertEqual(s.cycle_text().count("почта:"), 1)

    def test_same_text_sent_again_after_the_turn_took_it(self):
        # Повтор той же реплики через час: строка «Входящих» другая (в ней
        # время), а старая отметка ей не мешает, потому что своей строки во
        # «Входящих» у неё уже нет.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.added(s.run())
        s.inbox("2026-08-15 15:10, из дашборда: да")
        text = self.added(s.run())
        self.assertIn("15:10", text)
        self.assertNotIn("14:03", s.marks_text())

    def test_mark_without_its_line_is_not_carried_over(self):
        s = self.stand()
        s.marks(("s-old", "2026-08-15 12:00, из дашборда: подхваченная витком"))
        s.inbox("2026-08-15 14:03, из дашборда: новая")
        self.added(s.run())
        self.assertNotIn("подхваченная витком", s.marks_text())
        self.assertIn("новая", s.marks_text())

    def test_line_marked_by_ask_is_left_alone(self):
        # Ящик общий: строку, съеденную ключом --ask, отметил он сам, и второй
        # раз витку она не едет. Сессии у такой отметки нет, и это не битая
        # запись, а виток живого чата, у которого замка нет.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: ответ на вопрос витка")
        s.marks(("-", "2026-08-15 14:03, из дашборда: ответ на вопрос витка"))
        self.silent(s.run())
        self.assertEqual(s.cycle_text(), "")

    def test_two_goals_in_one_root_both_deliver_one_record(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: одна почта на два ящика")
        s.register(goal="DK-200")
        text = self.added(s.run())
        self.assertEqual(text.count("одна почта на два ящика"), 2, text)

    def test_broken_input_is_a_quiet_zero(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.silent(s.run(raw="это не json"))
        self.silent(s.run(raw='"json не объектом"'))
        self.assertEqual(s.marks_text(), "")

    def test_unknown_protocol_refuses_by_name(self):
        s = self.stand()
        p = s.run(protocol="нет-такого")
        self.assertEqual(p.returncode, 2)
        self.assertIn("нет-такого", p.stderr)


class MarksTest(InboxCase):
    def test_failed_mark_cancels_the_delivery(self):
        # Файл отметок подменён директорией: запись падает, и доставки не
        # происходит вовсе. Порядок тут и проверяется, отметка до доставки.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        os.makedirs(s.mail)
        self.silent(s.run())
        self.assertEqual(s.cycle_text(), "")
        self.assertIn("отметку доставки не записать", s.journal())

    def test_foreign_flock_stops_the_delivery(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        fd = os.open(s.mail + ".lock", os.O_CREAT | os.O_RDWR, 0o644)
        self.addCleanup(os.close, fd)
        fcntl.flock(fd, fcntl.LOCK_EX)
        self.silent(s.run())
        self.assertEqual(s.marks_text(), "")
        self.assertEqual(s.cycle_text(), "")

    def test_lock_is_taken_on_its_own_name(self):
        # Замок стоит на соседнем файле, а не на самом файле отметок: тот
        # пересоздаётся на каждой записи, и замок на нём разъезжался бы с путём.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.added(s.run())
        self.assertTrue(os.path.isfile(s.mail + ".lock"), "замка отметок нет")


class AddressTest(InboxCase):
    def test_task_tree_gets_no_mail(self):
        # Дерево задачи стоит рядом с корнем цели и начинается с его имени:
        # сверка подстрокой пустила бы ход исполнителя под адрес цели.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.silent(s.run(cwd=s.root + "-dk-101"))
        self.assertEqual(s.marks_text(), "")
        self.assertEqual(s.journal(), "")

    def test_transcript_does_not_replace_the_tree(self):
        # Дерево берётся из cwd события, а не из транскрипта: у хода в дереве
        # задачи транскрипт называет корень цели, и почта уехала бы исполнителю.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        p = s.run(cwd=os.path.join(s.root + "-dk-101"))
        self.silent(p)
        self.assertEqual(s.cycle_text(), "")

    def test_sandbox_path_leaves_quietly_and_names_itself_under_trace(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        sand = "/private/tmp/claude-501/-Users-rider-projects-devkit/abc/scratchpad/cap/work"
        self.silent(s.run(cwd=sand))
        self.assertEqual(s.journal(), "", "чужой путь написал строку без ключа трассировки")
        self.silent(s.run(cwd=sand, trace=True))
        self.assertIn(sand, s.journal())

    def test_goal_under_a_temporary_root_gets_mail_as_any_other(self):
        # Стенд целиком лежит под временным каталогом, и почта до него доезжает:
        # правило песочницы, заведённое заново, красит этот тест.
        s = self.stand()
        self.assertTrue(s.root.startswith(tempfile.gettempdir()) or "/var/folders" in s.root
                        or "/private/var" in s.root, "стенд не под временным корнем: %s" % s.root)
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertIn("да", self.added(s.run()))

    def test_symlinked_temporary_root_still_matches(self):
        # Харнес приносит cwd развёрнутым: на macOS корень стенда /var/folders
        # приезжает как /private/var/folders, а в записи реестра он лежит таким,
        # каким его назвал вызов. Первым же ходом живого прогона это стоило
        # доставки, поэтому за текстовой сверкой идёт сверка по realpath.
        s = self.stand()
        real = os.path.realpath(s.root)
        if real == s.root:
            self.skipTest("на этой машине корень стенда без симлинка")
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertIn("да", self.added(s.run(cwd=real)))
        # Граница пути при этом остаётся границей, а не подстрокой.
        s.inbox("2026-08-15 15:03, из дашборда: второе")
        self.silent(s.run(cwd=real + "-dk-101"))

    def test_subagent_turn_gets_no_mail(self):
        # Роль в событии хода субагента есть, и отсев ею жёстче географии:
        # Explore и proofread виток зовёт по своему же дереву.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.silent(s.run(agent="general-purpose"))
        self.assertEqual(s.marks_text(), "")

    def test_registry_entry_names_the_root_by_its_goal_file(self):
        # Корень считается от поля file: у записи с относительным root сверка
        # всё равно сходится.
        s = self.stand()
        s.register(root="..")
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertIn("да", self.added(s.run()))


class ShellSessionTest(InboxCase):
    def test_live_lock_delivers_only_to_the_named_turn(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        s.shell_lock(self.live_pid(), "turn-1111")
        self.silent(s.run(session="соседнее-окно"))
        self.assertEqual(s.marks_text(), "")
        self.assertIn("почта адресована витку turn-1111", s.journal())
        self.assertIn("соседнее-окно", s.journal())
        self.assertIn("да", self.added(s.run(session="turn-1111")))

    def test_name_miss_is_silent_without_lying_mail(self):
        s = self.stand()
        s.shell_lock(self.live_pid(), "turn-1111")
        self.silent(s.run(session="соседнее-окно"))
        self.assertEqual(s.journal(), "", "промах имени написал строку при пустых «Входящих»")
        self.silent(s.run(session="соседнее-окно", trace=True))
        self.assertIn("почта адресована витку turn-1111", s.journal())

    def test_dead_lock_reads_as_the_chat_mode(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        s.shell_lock(self.dead_pid(), "turn-1111")
        self.assertIn("да", self.added(s.run(session="окно-человека")))

    def test_live_lock_without_a_name_delivers_to_nobody(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        s.shell_lock(self.live_pid())
        self.silent(s.run())
        self.assertEqual(s.marks_text(), "")


class LivenessTest(InboxCase):
    def test_live_lock_ignores_every_movement_mark(self):
        # Под живым замком метки не смотрятся вовсе: точный признак не должна
        # перебивать мерка грубее его, и отметка stopped ему тоже не указ.
        s = self.stand()
        s.register(seen=stamp(-10 * 3600), stopped=stamp(-9 * 3600))
        s.cycle_log("%s виток 1 поднят" % stamp(-10 * 3600))
        s.inbox("2026-08-15 14:03, из дашборда: да")
        s.shell_lock(self.live_pid(), "turn-1111")
        self.assertIn("да", self.added(s.run(session="turn-1111")))

    def test_fresh_mark_lets_the_chat_turn_through(self):
        s = self.stand()
        s.register(seen=stamp(-60), stopped=stamp(-30))
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertIn("да", self.added(s.run()))

    def test_both_marks_older_than_the_threshold_stop_the_mail(self):
        s = self.stand()
        s.register(seen=stamp(-4 * 3600))
        s.cycle_log("%s виток 7 маркер continue код 0" % stamp(-4 * 3600))
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.silent(s.run())
        self.assertEqual(s.marks_text(), "")

    def test_cold_start_holds_on_seen_alone(self):
        # Цель ведут в чате впервые: журнала цикла нет вовсе, живость держит seen.
        s = self.stand()
        s.register(seen=stamp(-60))
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertFalse(os.path.exists(os.path.join(s.dev, "goal-DK-100.log")))
        self.assertIn("да", self.added(s.run()))

    def test_long_turn_holds_on_the_cycle_journal(self):
        # seen протух на длинном витке, а журнал цикла двигался: доставка идёт.
        s = self.stand()
        s.register(seen=stamp(-4 * 3600))
        s.cycle_log("%s виток 7 поднят, ход витка ниже" % stamp(-10 * 60))
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertIn("да", self.added(s.run()))

    def test_own_delivery_line_is_not_a_movement_of_the_goal(self):
        # Строку доставки кладёт сам почтальон, и держать ею цель живой он не
        # вправе: иначе одна доставка продлевала бы канал ещё на три часа.
        s = self.stand()
        s.register(seen=stamp(-4 * 3600))
        s.cycle_log("%s виток 7 маркер continue код 0" % stamp(-4 * 3600),
                    "%s почта: витку доставлена реплика «раньше»" % stamp(-60))
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.silent(s.run())
        self.assertEqual(s.marks_text(), "")

    def test_entry_without_any_mark_is_a_refusal_with_a_line(self):
        s = self.stand()
        s.register(seen=None)
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.silent(s.run())
        self.assertIn("нет ни одной метки движения", s.journal())

    def test_missing_journal_is_not_a_zero_time(self):
        # Отсутствующий журнал это отсутствующая метка, а не нулевое время:
        # прочтись он нулём, свежий seen ничего бы не спас.
        s = self.stand()
        s.register(seen=stamp(-60))
        s.cycle_log()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.assertIn("да", self.added(s.run()))


class AskTest(InboxCase):
    def wait_flag(self, s, until):
        with open(os.path.join(s.dev, "goal-DK-100.ask"), "w", encoding="utf-8") as f:
            f.write(until + "\n")

    def test_live_wait_holds_the_postman(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.wait_flag(s, stamp(600))
        self.silent(s.run())
        self.assertEqual(s.marks_text(), "")
        self.assertIn("держит вопрос витка", s.journal())

    def test_stale_wait_does_not_hold(self):
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.wait_flag(s, stamp(-600))
        self.assertIn("да", self.added(s.run()))


class JournalTest(InboxCase):
    def test_journal_is_trimmed_over_the_limit(self):
        s = self.stand()
        path = os.path.join(s.home, ".devkit", "inbox.log")
        with open(path, "w", encoding="utf-8") as f:
            f.write(("%s сессия старьё цель DK-100 доставлено строк 1\n" % stamp()) * 4000)
        self.assertGreater(os.path.getsize(path), 100 * 1024)
        s.inbox("2026-08-15 14:03, из дашборда: да")
        self.added(s.run())
        with open(path, encoding="utf-8") as f:
            rows = f.readlines()
        self.assertLessEqual(len(rows), 501, "журнал не обрезался")
        self.assertIn("доставлено строк 1", rows[-1])

    def test_shell_failure_leaves_a_line(self):
        # Ненулевой код --say это беда почтальона: тихий ноль и строка в журнал.
        # Оболочка отказывает, когда файла цели нет, а до неё дело доходит
        # только после снятой отметки.
        s = self.stand()
        s.inbox("2026-08-15 14:03, из дашборда: да")
        # Оболочка зовётся с ID цели из реестра, а файл ей нужен свой: запись
        # называет цель DK-777, чьего docs/tasks/DK-777.md в стенде нет, и
        # --say отказывает ненулевым кодом.
        os.remove(os.path.join(s.home, ".devkit", "goals", "DK-100-stand.watch"))
        s.register(goal="DK-777", file=s.goal)
        self.added(s.run())
        self.assertIn("не записала строку доставки", s.journal())


class CostTest(InboxCase):
    """Цена холостого хода. Хук стоит на каждом ходе инструмента в каждой
    сессии машины, и его цена это цена самого запуска интерпретатора: всё, что
    сверх, платится на чужой работе. Мерка тут относительная, к запуску голого
    python3, потому что абсолютные миллисекунды на загруженной машине скачут."""

    def clock(self, argv, env, feed):
        best = None
        for _ in range(5):
            at = time.time()
            subprocess.run(argv, input=feed, stdout=subprocess.DEVNULL,
                           stderr=subprocess.DEVNULL, text=True, env=env)
            best = time.time() - at if best is None else min(best, time.time() - at)
        return best

    def test_idle_turn_costs_about_the_interpreter_start(self):
        empty = os.path.join(self.box, "no-goals")
        os.makedirs(os.path.join(empty, ".devkit"))
        env = dict(os.environ, HOME=empty)
        env.pop("DEVKIT_INBOX_TRACE", None)
        bare = self.clock([sys.executable, "-c", "pass"], env, "")
        idle = self.clock([sys.executable, INBOX, "--hook"], env, "{}")
        self.assertLess(idle, bare * 3 + 0.05,
                        "холостой ход хука дороже трёх запусков интерпретатора: %.3f против %.3f"
                        % (idle, bare))
        # Целей под надзором нет, значит и следов от хука не остаётся: ранний
        # выход стоит на реестре и до разбора события не доходит.
        self.assertFalse(os.path.exists(os.path.join(empty, ".devkit", "inbox.log")),
                         "холостой ход написал строку в журнал")


if __name__ == "__main__":
    unittest.main(verbosity=0)
