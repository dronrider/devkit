#!/usr/bin/env python3
"""Самопроверка реестра чатов (DK-431): строка ~/.devkit/sessions.log про
родившуюся сессию. Разбор события идёт с живого образца SessionStart, а не с
сочинённого JSON, стенд это временное дерево с .git, как у бокового дерева
задачи. Прогон хука идёт подпроцессом с подсунутым stdin, как у остальных
хуков: важно не только что функция считает, но и что команда из settings.json
пишет строку и уходит нулём.
"""
import importlib
import io
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
import stagerun  # noqa: E402
session_task = importlib.import_module("session-task")

SID = "5a750327-a8b5-4d2f-9aab-46cf862d2c47"


def sample():
    with open(SAMPLE, encoding="utf-8") as f:
        return json.load(f)


def start(cwd, session=SID, transcript="/home/t/.claude/projects/p/%s.jsonl" % SID,
          source="startup"):
    return hookio.Start(session=session, cwd=cwd, transcript=transcript, source=source)


REG_KEYS = ("сессия", "задача", "проект", "дерево", "транскрипт", "источник",
            "повод", "tmux", "панель", "родитель", "носитель")


def fields(line):
    """Строка журнала парами «ключ значение», как её читает дашборд: значение
    идёт до следующего ключевого слова, и пробел в пути дерева или в поводе
    строку не рассыпает."""
    parts = line.rstrip("\n").split(" ")
    out, key = {}, None
    for tok in parts[1:]:
        # Ключевое слово переключает поле только на непустом значении, иначе
        # «источник дерево» читалось бы как второй ключ (правило то же, что у
        # ParseLine на go).
        if tok in REG_KEYS and out.get(key):
            key = tok
            continue
        if key is None:
            key = tok
            continue
        out[key] = (out.get(key, "") + " " + tok).strip()
    return out, parts[0]


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

    def test_headless_start_does_not_take_the_window(self):
        # DK-673, живой случай с чатом XR-207. Агент выполнил из своего окна
        # `claude -p --resume` чужой сессии, та унаследовала DEVKIT_TMUX, и
        # хозяином имени стал мёртвый разговор: реплики панели поехали
        # клавишами в окно диспетчера, а уборка того разговора сняла бы его
        # живую работу. Клиент печатного подъёма поднят без терминала, и по
        # этому он и отличается от клиента, которого поднял дашборд.
        tree = Tree(self.tmp, "devkit")
        client = subprocess.Popen(["sleep", "30"], start_new_session=True,
                                  stdin=subprocess.DEVNULL,
                                  stdout=subprocess.DEVNULL,
                                  stderr=subprocess.DEVNULL)
        self.addCleanup(client.wait)
        self.addCleanup(client.kill)
        f, _ = fields(session_task.record(
            start(tree.root), env={"DEVKIT_TMUX": "chat-DK-161-1",
                                   "CLAUDE_PID": str(client.pid)}))
        self.assertEqual(f["tmux"], "-")
        # Отказ назван в самом журнале: без него потеря имени неотличима от
        # сессии, поднятой мимо дашборда.
        self.assertEqual(f["повод"],
                         "startup, имя окна chat-DK-161-1 не принято "
                         "(клиент без терминала)")

    def test_window_of_a_client_with_a_terminal_is_written(self):
        # Обратная сторона той же меры: дашборд поднимает клиента в панели
        # tmux, терминал у него есть, и имя окна ложится в реестр как прежде.
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(
            start(tree.root), env={"DEVKIT_TMUX": "chat-DK-431-2"},
            terminal=lambda env: "ttys004"))
        self.assertEqual((f["tmux"], f["повод"]), ("chat-DK-431-2", "startup"))

    def test_client_without_a_terminal_is_told_apart(self):
        # Сама мера: процесс своей сессии ps показывает вопросительными
        # знаками, и это единственный ответ, по которому имя отбирается.
        client = subprocess.Popen(["sleep", "30"], start_new_session=True,
                                  stdin=subprocess.DEVNULL,
                                  stdout=subprocess.DEVNULL,
                                  stderr=subprocess.DEVNULL)
        self.addCleanup(client.wait)
        self.addCleanup(client.kill)
        self.assertEqual(session_task.client_terminal(
            {"CLAUDE_PID": str(client.pid)}), "")

    def test_unknown_client_is_not_a_verdict(self):
        # Незнание именем не распоряжается: чужой харнес своего pid не
        # называет, а мёртвый процесс ps не покажет вовсе. Имя в обоих случаях
        # остаётся, дыру закрывает знание, а не догадка.
        self.assertIsNone(session_task.client_terminal({}))
        self.assertIsNone(session_task.client_terminal({"CLAUDE_PID": "нет"}))
        dead = subprocess.Popen(["true"])
        dead.wait()
        self.assertIsNone(session_task.client_terminal({"CLAUDE_PID": str(dead.pid)}))
        f, _ = fields(session_task.record(
            start(Tree(self.tmp, "devkit").root),
            env={"DEVKIT_TMUX": "chat-DK-431-2", "CLAUDE_PID": str(dead.pid)}))
        self.assertEqual(f["tmux"], "chat-DK-431-2")

    def test_fields_go_in_the_written_order(self):
        # Порядок полей это договор с читателем на go, и сползание любого из
        # них тут обязано быть заметно.
        tree = Tree(self.tmp, "devkit-dk-431")
        line = session_task.record(start(tree.root, source="resume"),
                                   env={"DEVKIT_TMUX": "task-DK-431"}, now=0)
        self.assertEqual(line.split(" ")[1::2],
                         ["сессия", "задача", "проект", "дерево", "транскрипт",
                          "источник", "повод", "tmux", "панель", "родитель",
                          "носитель"])
        self.assertTrue(line.endswith(
            " источник дерево повод resume tmux task-DK-431 панель - родитель - "
            "носитель -\n"), line)

    def test_carrier_tells_headless_from_a_talk(self):
        # Разговор человека и безголовый заход по реестру были неотличимы, и
        # свод расхода относил их к одной статье. Носитель пишется из
        # окружения поднявшего, а виток цели старше безголовости: цикл ходит и
        # печатным клиентом дашборда, а статья у него своя (DK-913).
        tree = Tree(self.tmp, "devkit-dk-431")
        f, _ = fields(session_task.record(start(tree.root), env={}))
        self.assertEqual(f["носитель"], "-")
        f, _ = fields(session_task.record(
            start(tree.root), env={"DEVKIT_HEADLESS": "дашборд"}))
        self.assertEqual(f["носитель"], "дашборд")
        f, _ = fields(session_task.record(
            start(tree.root),
            env={"DEVKIT_HEADLESS": "дашборд", "DEVKIT_GOAL_SHELL": "1"}))
        self.assertEqual(f["носитель"], "виток")

    def test_pane_of_any_window_is_written(self):
        # Окно, открытое руками внутри tmux: имени от поднявшего нет, а адрес
        # панели есть, и по нему taskctl run подаёт реплику (DK-931).
        tree = Tree(self.tmp, "devkit-dk-431")
        f, _ = fields(session_task.record(
            start(tree.root), env={"TMUX": "/private/tmp/tmux-501/default,123,0",
                                   "TMUX_PANE": "%12"},
            terminal=lambda env: "ttys004"))
        self.assertEqual((f["задача"], f["tmux"], f["панель"], f["повод"]),
                         ("DK-431", "-", "%12", "startup"))

    def test_headless_client_does_not_take_the_pane(self):
        # Печатный подъём из хода наследует $TMUX_PANE окна, где идёт ход, и
        # адрес чужого разговора ему не принадлежит.
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(
            start(tree.root), env={"TMUX": "/tmp/tmux-501/default,1,0", "TMUX_PANE": "%12"},
            terminal=lambda env: ""))
        self.assertEqual((f["панель"], f["повод"]), ("-", "startup"))

    def test_pane_of_a_foreign_server_is_told_apart(self):
        # Номер панели чужого сервера из своего счёта: адреса нет, а отказ
        # виден в поводе, иначе потеря читалась бы как окно вне tmux.
        tree = Tree(self.tmp, "devkit")
        f, _ = fields(session_task.record(
            start(tree.root), env={"TMUX": "/tmp/tmux-501/work,1,0", "TMUX_PANE": "%3"},
            terminal=lambda env: "ttys004"))
        self.assertEqual(f["панель"], "-")
        self.assertIn("адрес окна %3 не принят (чужой сервер /tmp/tmux-501/work)", f["повод"])

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


class TestTouchKeepsBirth(unittest.TestCase):
    """Записи работы кормят реестр до потолка, а запись рождения живой сессии
    остаётся. Хвостовая обрезка вымывала её, дедупликация вымытой строки не
    видела и писала работу заново, и живой разговор оставался без имени tmux
    (DK-826)."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", self.tmp]))
        self.log = os.path.join(self.tmp, "sessions.log")
        self.tree = Tree(self.tmp, "devkit-dk-826")

    def touch(self, sid):
        event = {"session_id": sid, "cwd": self.tree.root, "hook_event_name": "PostToolUse",
                 "tool_name": "Edit", "tool_input": {"file_path": os.path.join(self.tree.root, "a.py")},
                 "tool_response": {}}
        stdin, sys.stdin = sys.stdin, io.StringIO(json.dumps(event))
        try:
            return session_task.run_touch("claude-code", path=self.log, now=0)
        finally:
            sys.stdin = stdin

    def lines(self):
        with open(self.log, encoding="utf-8") as f:
            return [ln for ln in f.read().split("\n") if ln]

    def test_work_to_the_cap_keeps_birth_of_live_session(self):
        birth = session_task.record(start(self.tree.root), env={"DEVKIT_TMUX": "chat-DK-826-1"},
                                    now=0, terminal=lambda env: "/dev/ttys001")
        hookio.append_capped(self.log, birth)
        for i in range(2 * hookio.LOG_KEEP):
            self.touch("w%04d" % i)
        lines = self.lines()
        self.assertLessEqual(len(lines), hookio.LOG_KEEP + 1)
        self.assertEqual(lines[0], birth.rstrip("\n"),
                         "запись рождения вымыта записями работы")
        self.assertTrue(all(" источник работа " in ln or ln == birth.rstrip("\n")
                            for ln in lines))

    def test_touch_once_per_session_and_task(self):
        self.assertEqual(self.touch("s1"), 0)
        self.assertEqual(self.touch("s1"), 0)
        lines = self.lines()
        self.assertEqual(len(lines), 1)
        got, _ = fields(lines[0])
        self.assertEqual((got["источник"], got["задача"], got["повод"]),
                         ("работа", "DK-826", "правка файла"))


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
        env.pop("TMUX", None)
        env.pop("TMUX_PANE", None)
        env.pop("DEVKIT_HIDDEN", None)
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
        # инструмент TodoWrite харнес в обход разрешений не выдаёт. Формат и
        # адрес файла правило не пересказывает, оно зовёт утилиту (DK-613).
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        self.assertIn("agentctl plan", r.stdout)
        self.assertIn("work-plan", r.stdout)
        # Порог повода из скилла work-plan едет дословно: без него правило
        # звучит безусловно, и разговор на один вопрос получает план (DK-881).
        self.assertIn("один вопрос с одним ответом", r.stdout)
        # Тем же абзацем едет правило отзывчивости: разговор идёт ходами, и
        # получасовой ход в нём неотличим от зависшей сессии.
        self.assertIn("отдавай субагенту", r.stdout)
        lines = self.log()
        self.assertEqual(len(lines), 1)
        f, _ = fields(lines[0])
        self.assertEqual((f["сессия"], f["задача"], f["источник"], f["повод"]),
                         (SID, "DK-431", "заказ", "startup"))

    def test_start_names_the_chat_skill(self):
        # Порядок разговора с человеком лежит в скилле chat (DK-614), и
        # старт называет скилл одной строкой: тело приезжает по вызову, а не
        # каждым стартом.
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-431"})
        self.assertEqual(r.returncode, 0)
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("chat", said)
        self.assertIn("DK-431", said)
        self.assertNotIn("Контекст сжат", said)
        # Второй ход держит хук chat-pointer.py по транскрипту, а не эта
        # оговорка (DK-1032): в тексте её быть не должно.
        self.assertNotIn("на каждой реплике", said)

    def test_hidden_task_keeps_plan_but_drops_chat_rule(self):
        # Скрытая сессия с заказом (виток цикла цели, тиковый прогон
        # конвейера, делегат agentctl run) ведёт работу, и план с
        # отзывчивостью ей нужны по-прежнему. Собеседника-человека у неё нет,
        # и правило разговора она не получает (DK-880).
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-431", "DEVKIT_HIDDEN": "1"})
        self.assertEqual(r.returncode, 0)
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertNotIn("chat", said)
        self.assertIn("agentctl plan", said)
        self.assertIn("отдавай субагенту", said)

    def test_compact_asks_to_reread_the_chat_skill(self):
        # SessionStart с поводом compact приходит после сжатия контекста, и
        # прочитанный скилл из контекста выпадает вместе с остальным. Хук
        # просит перечитать его фразой с ID задачи, а строку доски и файл
        # задачи не повторяет. Правила плана и отзывчивости едут той же
        # фразой: без них план после сжатия не ведётся, и кольцо дашборда
        # молчит (замечание ревью DK-614).
        event = dict(sample(), source="compact")
        r = self.run_hook(event, {"DEVKIT_TASK": "DK-431"})
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        data = json.loads(r.stdout)
        self.assertEqual(data["hookSpecificOutput"]["hookEventName"], "SessionStart")
        said = data["hookSpecificOutput"]["additionalContext"]
        self.assertIn("перечитай скилл chat", said)
        self.assertIn("DK-431", said)
        self.assertNotIn("Строка доски", said)
        self.assertNotIn("Файл задачи", said)
        self.assertIn("agentctl plan", said)
        self.assertIn("отдавай субагенту", said)
        # Строка реестра пишется и на этом поводе: повод виден в поле.
        f, _ = fields(self.log()[0])
        self.assertEqual((f["задача"], f["повод"]), ("DK-431", "compact"))

    def test_compact_hidden_keeps_plan_but_drops_reread(self):
        # После сжатия скрытая сессия план с отзывчивостью восстанавливает
        # по-прежнему, а просьбу перечитать chat не получает: человека
        # в собеседниках у неё нет (DK-880).
        event = dict(sample(), source="compact")
        r = self.run_hook(event, {"DEVKIT_TASK": "DK-431", "DEVKIT_HIDDEN": "1"})
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertNotIn("chat", said)
        self.assertIn("agentctl plan", said)
        self.assertIn("отдавай субагенту", said)

    def test_compact_without_a_task_asks_to_reread_and_keeps_plan(self):
        # Заказа задачи нет, контекста задачи в фразе нести нечего, но
        # собеседник у сессии остаётся человек: это чат доски с пустой
        # привязкой или консольная сессия. После сжатия она получает ту же
        # просьбу перечитать скилл и те же строки плана и отзывчивости, что
        # сессия с заказом (DK-914).
        event = dict(sample(), source="compact")
        r = self.run_hook(event)
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("Перечитай скилл chat", said)
        self.assertIn("agentctl plan", said)
        self.assertIn("отдавай субагенту", said)
        self.assertNotIn("по задаче", said)
        self.assertEqual(len(self.log()), 1)

    def test_no_task_without_hidden_gets_plan_and_pointer(self):
        # Общий чат доски (пустая привязка) и консольная сессия задачи не
        # заказывают, но человек в собеседниках у них есть: план, отзывчивость
        # и указатель на скилл едут и без DEVKIT_TASK, той же веткой, что и
        # сессии с заказом (DK-914). Слова «по задаче» тут нет: привязки к
        # задаче тоже нет.
        r = self.run_hook(sample())
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("chat", said)
        self.assertIn("вызови скилл chat инструментом Skill", said)
        self.assertIn("agentctl plan", said)
        self.assertIn("отдавай субагенту", said)
        self.assertNotIn("по задаче", said)
        # Второй ход держит хук chat-pointer.py по транскрипту, а не эта
        # оговорка (DK-1032): в тексте её быть не должно.
        self.assertNotIn("на каждой реплике", said)

    def test_no_task_with_hidden_is_silent(self):
        # Скрытая сессия без заказа задачи (например, делегат без привязки)
        # человека в собеседниках не имеет, и правило разговора ей не едет.
        r = self.run_hook(sample(), {"DEVKIT_HIDDEN": "1"})
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))
        self.assertEqual(len(self.log()), 1)

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

    def chat_store(self, session=SID):
        path = os.path.join(self.home, ".devkit", "chats", "%s.json" % session)
        if not os.path.exists(path):
            return None
        with open(path, encoding="utf-8") as f:
            return json.load(f)

    def test_hidden_env_marks_the_chat_store(self):
        # Признак «поднято без человека» (DK-847) ставит сам поднимающий
        # переменной DEVKIT_HIDDEN, а хук пишет его в память диалога
        # настоящего sid: список панели читает это поле оттуда же
        # (chatEntriesFrom, chats.go).
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-847", "DEVKIT_HIDDEN": "1"})
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        self.assertEqual(self.chat_store(), {"hidden": True})

    def test_without_hidden_env_store_stays_untouched(self):
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-847"})
        self.assertEqual(r.returncode, 0)
        self.assertIsNone(self.chat_store())

    def test_hidden_env_keeps_existing_fields(self):
        # Файл памяти диалога бывает заведён раньше рождения сессии (сервер
        # кладёт туда модель незачатой записи), и признак не должен стереть
        # то, что уже лежит рядом.
        path = os.path.join(self.home, ".devkit", "chats")
        os.makedirs(path)
        with open(os.path.join(path, "%s.json" % SID), "w", encoding="utf-8") as f:
            json.dump({"model": "opus"}, f)
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-847", "DEVKIT_HIDDEN": "1"})
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.chat_store(), {"model": "opus", "hidden": True})

    def test_empty_hidden_env_does_not_mark(self):
        # Унаследованный, но пустой признак не должен читаться как истинный:
        # devkit гасит эту переменную у чужого наследства тем же путём, что
        # и метку печатного режима (foreignVars, chats.go), а хук проверяет
        # это и сам, на пустой строке.
        r = self.run_hook(sample(), {"DEVKIT_TASK": "DK-847", "DEVKIT_HIDDEN": ""})
        self.assertEqual(r.returncode, 0)
        self.assertIsNone(self.chat_store())

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


class SetupStage(unittest.TestCase):
    """Сессия на черновике или на цели ставит записи этап «постановка»
    (DK-911): временный репозиторий с накопителем, временный дом."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", self.tmp]))
        self.home = os.path.join(self.tmp, "home")
        self.root = os.path.join(self.tmp, "proj")
        subprocess.run(["git", "init", "-q", "-b", "main", self.root], check=True)
        os.makedirs(os.path.join(self.root, "docs", "tasks", "drafts"))
        with open(os.path.join(self.root, "docs", "tasks", "drafts", "DK-7.md"), "w", encoding="utf-8") as f:
            f.write("# DK-7: черновик\n")
        with open(os.path.join(self.root, "docs", "tasks", "DK-8.md"), "w", encoding="utf-8") as f:
            f.write("# DK-8: цель\n\n## Задачи цели\n\n- DK-9\n")
        with open(os.path.join(self.root, "docs", "tasks", "DK-9.md"), "w", encoding="utf-8") as f:
            f.write("# DK-9: задача\n\n## Что происходит\n")

    def stages(self, task):
        return stagerun.load(stagerun.path(self.home, os.path.realpath(self.root), task))["stages"]

    def run_stage(self, task, hidden=False, source="startup"):
        env = {"HOME": self.home, "DEVKIT_TASK": task}
        if hidden:
            env["DEVKIT_HIDDEN"] = "1"
        return session_task.setup_stage(start(self.root, source=source), task, env, now=0)

    def test_draft_session_writes_setup(self):
        self.assertEqual(self.run_stage("DK-7"), "черновик")
        stages = self.stages("DK-7")
        self.assertEqual([s["kind"] for s in stages], [stagerun.SETUP])
        self.assertEqual(stages[0]["note"], "разбор черновика, сессия startup")
        self.assertEqual(stages[0]["session"], SID)

    def test_goal_session_with_a_human_writes_setup(self):
        self.assertEqual(self.run_stage("DK-8"), "цель")
        self.assertEqual(self.stages("DK-8")[0]["note"], "нарезка цели, сессия startup")

    def test_hidden_goal_tick_is_not_setup(self):
        self.assertEqual(self.run_stage("DK-8", hidden=True), "")
        self.assertEqual(self.stages("DK-8"), [])

    def test_plain_task_and_compact_write_nothing(self):
        self.assertEqual(self.run_stage("DK-9"), "")
        self.assertEqual(self.run_stage("DK-7", source=session_task.COMPACT), "")
        self.assertEqual(self.stages("DK-9"), [])
        self.assertEqual(self.stages("DK-7"), [])

    def test_hook_from_stdin_writes_setup_for_a_draft(self):
        ev = dict(sample())
        ev["cwd"] = self.root
        env = dict(os.environ, HOME=self.home, DEVKIT_TASK="DK-7")
        for key in ("DEVKIT_TMUX", "TMUX", "TMUX_PANE", "DEVKIT_HIDDEN"):
            env.pop(key, None)
        r = subprocess.run([sys.executable, HOOK, "--hook"], input=json.dumps(ev),
                           capture_output=True, text=True, env=env)
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        self.assertEqual([s["kind"] for s in self.stages("DK-7")], [stagerun.SETUP])
