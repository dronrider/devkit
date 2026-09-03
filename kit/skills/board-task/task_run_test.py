#!/usr/bin/env python3
"""Самопроверка оболочки конвейера task-run.py. Головы играет стаб вместо
клиента, доску стаб вместо taskctl: настоящих сессий тут не поднимается ни
одной, как и в самопроверке цикла цели. Каждый стенд это свой временный корень
со своей обвязкой .devkit и своим HOME, чтобы журнал утилит и уведомитель
писали в него. Оба стаба написаны на python и живут только во временной
директории теста: файлами репозитория они не становятся, как и любая другая
фикстура, изображающая чужую программу.
"""
import importlib
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(HERE, "task-run.py")

sys.path.insert(0, HERE)
task_run = importlib.import_module("task-run")

# Стаб доски: печатает строку задачи тем же форматом, каким её печатает taskctl
# («DK-1 в in-progress»), а секцию читает из файла стенда. Пометку про
# пользовательскую приёмку он печатает второй строкой, когда её просят.
TASKCTL_STUB = r'''#!/usr/bin/env python3
import os
import sys

state = os.environ["DEVKIT_TEST_STATE"]
with open(state, encoding="utf-8") as f:
    sect = f.read().strip()
if sys.argv[1:2] != ["show"]:
    sys.exit(0)
tail = ""
if sect.startswith("check-user"):
    sect, tail = "check", "  сценарий пользовательский"
sys.stdout.write("%s в %s\n" % (sys.argv[2], sect))
sys.stdout.write("| %s | строка | task | P2 | 10 | S | file |\n" % sys.argv[2])
if tail:
    sys.stdout.write(tail + "\n")
'''

# Стаб головы: играет очередную строку сценария и записывает заказ, с которым
# его подняли. Строки сценария: «работа» ничего не делает, «закрой» уводит
# строку в архив, «паркуй» в blocked, «падение» выходит ненулевым кодом.
# Сценарий кончился, значит повторяется последняя строка: голова, которая не
# двигает строку, так и не двигает её до конца.
CLAUDE_STUB = r'''#!/usr/bin/env python3
import os
import sys

state = os.environ["DEVKIT_TEST_STATE"]
calls = os.environ["DEVKIT_TEST_CALLS"]
plan = os.environ["DEVKIT_TEST_PLAN"].split("|")
order = sys.argv[sys.argv.index("-p") + 1] if "-p" in sys.argv else ""
with open(os.path.join(os.environ["HOME"], "headless"), "w", encoding="utf-8") as f:
    f.write(os.environ.get("DEVKIT_HEADLESS", ""))
with open(calls, "a", encoding="utf-8") as f:
    f.write(order + "\n")
n = 0
with open(calls, encoding="utf-8") as f:
    n = len([l for l in f if l.strip() != ""])
step = plan[min(n, len(plan)) - 1]
if step == "закрой":
    open(state, "w", encoding="utf-8").write("архиве\n")
elif step == "паркуй":
    open(state, "w", encoding="utf-8").write("blocked\n")
elif step == "падение":
    sys.exit(1)
'''


# Стаб tmux: живое окно отвечает на has-session нулём, мёртвое единицей, а
# send-keys складывает нажатия в файл входа, как настоящий tmux складывает их в
# терминал панели. Литерал копится в буфере, Enter отправляет накопленное
# строкой: тем же порядком подаёт реплику дашборд.
TMUX_STUB = r'''#!/usr/bin/env python3
import os
import sys

args = sys.argv[1:]
inbox = os.environ["DEVKIT_TEST_INBOX"]
if args[:1] == ["has-session"]:
    sys.exit(0 if os.environ.get("DEVKIT_TEST_TMUX") == "живая" else 1)
if args[:1] == ["send-keys"]:
    if "-l" in args:
        with open(inbox + ".buf", "a", encoding="utf-8") as f:
            f.write(args[args.index("-l") + 1])
    else:
        body = ""
        if os.path.exists(inbox + ".buf"):
            with open(inbox + ".buf", encoding="utf-8") as f:
                body = f.read()
            os.remove(inbox + ".buf")
        with open(inbox, "a", encoding="utf-8") as f:
            f.write(body + "\n")
sys.exit(0)
'''

# Стаб живой головы: интерактивный клиент, который между заказами не выходит.
# Первый заказ приходит последним аргументом, следующие строками файла входа,
# куда их складывает стаб tmux. Отметки хода клиент пишет за хук turn-mark.py,
# а запись реестра чатов за SessionStart-хук: на стенде хуков харнеса нет, а
# оболочка читает именно их следы.
LIVE_STUB = r'''#!/usr/bin/env python3
import os
import subprocess
import sys
import time

home = os.environ["HOME"]
state = os.environ["DEVKIT_TEST_STATE"]
calls = os.environ["DEVKIT_TEST_CALLS"]
plan = os.environ["DEVKIT_TEST_PLAN"].split("|")
inbox = os.environ["DEVKIT_TEST_INBOX"]
sid = os.environ["DEVKIT_TEST_SID"]
devkit = os.path.join(home, ".devkit")
os.makedirs(devkit, exist_ok=True)
turns = os.environ.get("DEVKIT_TURN_MARK_LOG") or os.path.join(devkit, "turns.log")
stamp = "%Y-%m-%dT%H:%M:%S"


def mark(word, why="-"):
    with open(turns, "a", encoding="utf-8") as f:
        f.write("%s сессия %s ход %s повод %s дерево %s\n"
                % (time.strftime(stamp), sid[:8], word, why, home))


def crowd(count):
    """Отметки чужих сессий машины. Журнал общий на всех, и своих строк в нём
    меньшинство."""
    with open(turns, "a", encoding="utf-8") as f:
        for i in range(count):
            f.write("%s сессия ffff%04d ход кончен повод - дерево /tmp/чужое\n"
                    % (time.strftime(stamp), i))


with open(os.path.join(devkit, "sessions.log"), "a", encoding="utf-8") as f:
    f.write("%s сессия %s задача DK-1 проект p дерево %s транскрипт - "
            "источник заказ повод startup tmux %s родитель -\n"
            % (time.strftime(stamp), sid, home, os.environ.get("DEVKIT_TMUX", "-")))
with open(os.path.join(home, "headless"), "w", encoding="utf-8") as f:
    f.write(os.environ.get("DEVKIT_HEADLESS", ""))
if "ротация" in plan:
    # Журнал был полон чужими сессиями ещё до нашей: рез его подрезает до
    # последних пятисот строк, и без этого запаса своя отметка уехала бы вместе
    # с чужими.
    crowd(800)

order, at = sys.argv[-1], 0
while True:
    with open(calls, "a", encoding="utf-8") as f:
        f.write(order + "\n")
    with open(os.path.join(home, "pids"), "a", encoding="utf-8") as f:
        f.write("%d\n" % os.getpid())
    # Шаг сценария считается по числу отработанных заказов, а не по счётчику
    # процесса: печатная череда поднимает на каждый заказ свой процесс, и
    # сценарий обязан идти одинаково у обеих голов.
    with open(calls, encoding="utf-8") as f:
        n = len([l for l in f if l.strip()])
    step = plan[min(n, len(plan)) - 1]
    if step == "закрой":
        open(state, "w", encoding="utf-8").write("архиве\n")
    elif step == "паркуй":
        open(state, "w", encoding="utf-8").write("blocked\n")
    elif step == "падение":
        sys.exit(1)
    elif step == "вопрос":
        mark("ждёт", "permission_prompt")
        time.sleep(0.3)
    elif step == "молчит":
        time.sleep(3)
    elif step == "ротация":
        # Чужие сессии дописали в общий журнал ещё сотню строк, и хук подрезал
        # его до последних пятисот, как это делает hookio.append_capped. Своя
        # прочитанная отметка осталась в оставленном хвосте, а файл усох вдвое.
        crowd(100)
        with open(turns, encoding="utf-8") as f:
            tail = [l for l in f.readlines() if l.strip()][-500:]
        with open(turns, "w", encoding="utf-8") as f:
            f.writelines(tail)
        time.sleep(1)
    elif step == "потеря":
        # Журнал отметок пропал целиком: снят руками, стёрт уборкой дома.
        if os.path.exists(turns):
            os.remove(turns)
        time.sleep(2)
    elif step == "фон":
        subprocess.Popen([sys.executable, "-c",
                          "import sys,time;time.sleep(1);"
                          "open(sys.argv[1],'w',encoding='utf-8').write('готово')",
                          os.path.join(home, "фон")])
    elif step == "ждать фон":
        # Долгий шаг доживает до конца внутри живого хода. Ждём его признаком,
        # готовым файлом, а не паузой между заказами: пауза под нагрузкой
        # кончается раньше работы.
        until = time.time() + 20
        while time.time() < until and not os.path.exists(os.path.join(home, "фон")):
            time.sleep(0.05)
    # Заказ, пришедший до конца хода, это заказ поверх идущей работы. Живая
    # голова такого не видит, а оболочка, поверившая старой отметке, шлёт его
    # именно так.
    if os.path.exists(inbox) and os.path.getsize(inbox) > at:
        with open(os.path.join(home, "поверх"), "a", encoding="utf-8") as f:
            f.write(order + "\n")
    mark("кончен")
    if step == "чужой":
        # Реплика человека, поданная панелью в это же окно: ход начался не по
        # заказу оболочки и кончился сам. Отметки пишутся подряд, без сна: сон
        # тут гонка, и под нагрузкой оболочка успевала послать свой заказ
        # раньше, чем появлялась отметка о чужом ходе.
        mark("начат")
        mark("кончен")
    # Заказа нет и нет: живой голове его подают клавиатурой окна, а печатная
    # череда ждёт выхода процесса, и висеть в ней вечно клиент не должен.
    until = time.time() + 10
    while time.time() < until:
        if os.path.exists(inbox):
            with open(inbox, encoding="utf-8") as f:
                f.seek(at)
                got = [l for l in f.read().split("\n") if l.strip()]
                at = f.tell()
            if got:
                order = got[-1]
                mark("начат")
                break
        time.sleep(0.05)
    else:
        # Заказа так и не пришло. Живой голове его подают клавиатурой окна, и
        # молчание тут значит, что оболочка её потеряла: след виден тесту, а не
        # только кодом возврата.
        with open(os.path.join(home, "простой"), "a", encoding="utf-8") as f:
            f.write(order + "\n")
        sys.exit(0)
'''


SID = "5a750327-a8b5-4d2f-9aab-46cf862d2c47"


def write_stub(path, body):
    with open(path, "w", encoding="utf-8") as f:
        f.write(body)
    os.chmod(path, 0o755)


class Stand:
    """Временный корень со стабами, доской в файле и своим HOME."""

    def __init__(self, sect="in-progress", plan="работа", live=False, pause="0",
                 mute="8"):
        self.root = tempfile.mkdtemp(prefix="task-run-")
        self.bin = os.path.join(self.root, "bin")
        os.makedirs(self.bin)
        os.makedirs(os.path.join(self.root, ".devkit"))
        self.state = os.path.join(self.root, "sect")
        self.calls = os.path.join(self.root, "calls")
        self.inbox = os.path.join(self.root, "inbox")
        with open(self.state, "w", encoding="utf-8") as f:
            f.write(sect + "\n")
        open(self.calls, "w", encoding="utf-8").close()
        write_stub(os.path.join(self.bin, "taskctl"), TASKCTL_STUB)
        write_stub(os.path.join(self.bin, "claude-stub"), CLAUDE_STUB)
        write_stub(os.path.join(self.bin, "claude-live"), LIVE_STUB)
        write_stub(os.path.join(self.bin, "tmux"), TMUX_STUB)
        self.plan = plan
        self.live = live
        self.pause = pause
        self.mute = mute

    def env(self):
        return {
            "PATH": self.bin + os.pathsep + os.environ.get("PATH", ""),
            "HOME": self.root,
            "DEVKIT_TEST_STATE": self.state,
            "DEVKIT_TEST_CALLS": self.calls,
            "DEVKIT_TEST_PLAN": self.plan,
            "DEVKIT_TEST_INBOX": self.inbox,
            "DEVKIT_TEST_SID": SID,
            # Окно живой головы: стаб tmux отвечает про него has-session, и без
            # этого слова оболочка идёт печатной чередой, как шла.
            "DEVKIT_TEST_TMUX": "живая" if self.live else "нет",
            "DEVKIT_TASK_PASS_PAUSE": self.pause,
            "DEVKIT_TASK_WATCH_STEP": "0",
            # Срок ожидания записи реестра стенд не подрезает. Ждёт оболочка по
            # признаку, появлению записи, и короткий срок под нагрузкой истекал
            # раньше, чем стаб успевал завестись: заказы дальше первого тогда
            # подавать некому, и тест падал на соседях по прогону.
            "DEVKIT_TASK_MUTE": self.mute,
            "DEVKIT_TMUX": "task-DK-1",
            # Метку печатного режима ставит дашборд, и живая голова обязана
            # снять её с клиента: с нею рубеж синхронности отбивает фоновый ход.
            "DEVKIT_HEADLESS": "дашборд",
            # Уведомитель на стенде молчит: звать человека к синтетической
            # задаче незачем, а канал его проверяется своей самопроверкой.
            "DEVKIT_NOTIFY_OFF": "1",
        }

    def run(self, *args):
        argv = [sys.executable, RUN, "DK-1", "-C", self.root] + list(args)
        # Печатную череду гоняет печатный стаб: он выходит концом прохода, а
        # живой между заказами не выходит вовсе.
        head = "claude-live" if self.live and "--headless" not in args else "claude-stub"
        argv += ["--", os.path.join(self.bin, head)]
        return subprocess.run(argv, capture_output=True, text=True, env=self.env())

    def why(self, got):
        """Слова к провалу прогона. Голый код возврата не говорит ничего:
        живая голова пишет свой ход в журнал утилит, а стаб оставляет след
        простоя, когда заказа так и не дождался."""
        return "код %d, простой %s, журнал:\n%s" % (
            got.returncode, self.lines("простой"), "\n".join(self.journal()))

    def lines(self, name):
        path = os.path.join(self.root, name)
        if not os.path.isfile(path):
            return []
        with open(path, encoding="utf-8") as f:
            return [l.rstrip("\n") for l in f if l.strip() != ""]

    def orders(self):
        with open(self.calls, encoding="utf-8") as f:
            return [l.rstrip("\n") for l in f if l.strip() != ""]

    def journal(self):
        path = os.path.join(self.root, ".devkit", "log")
        if not os.path.isfile(path):
            return []
        with open(path, encoding="utf-8") as f:
            return [l.rstrip("\n") for l in f if l.strip() != ""]

    def drop(self):
        shutil.rmtree(self.root, ignore_errors=True)


class TestPasses(unittest.TestCase):
    """Голова выходит, а конвейер живёт: следующий проход поднимается тем же
    порядком, пока строка не закрыта."""

    def setUp(self):
        self.stands = []

    def tearDown(self):
        for s in self.stands:
            s.drop()

    def stand(self, **kw):
        s = Stand(**kw)
        self.stands.append(s)
        return s

    def test_second_pass_closes_the_task(self):
        # Первый проход это выход головы с незакрытой задачей: до DK-691 тут
        # конвейер и кончался, окно tmux закрывалось, а задача стояла.
        s = self.stand(plan="работа|закрой")
        r = s.run()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(len(s.orders()), 2)
        self.assertIn("задача закрыта", r.stdout)

    def test_first_order_and_next_order_differ(self):
        s = self.stand(plan="работа|закрой")
        s.run("--order", "Выполни DK-1", "--again", "Продолжай выполнение DK-1")
        self.assertEqual(s.orders(), ["Выполни DK-1", "Продолжай выполнение DK-1"])

    def test_head_exit_is_written_to_journal(self):
        # DoD задачи: выход головы с незакрытой задачей виден в журнале, и
        # строка называет сессию, задачу и её статус на момент выхода.
        s = self.stand(plan="работа|закрой")
        s.run()
        first = [l for l in s.journal() if "выход головы" in l]
        self.assertTrue(first, s.journal())
        self.assertIn("task-DK-1", first[0])
        self.assertIn("DK-1 в in-progress", first[0])
        self.assertIn("\ttask-run\t", first[0])

    def test_closed_task_raises_nobody(self):
        s = self.stand(sect="архиве")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(s.orders(), [])

    def test_parked_task_stops_the_pipeline(self):
        # Парковка это законный конец: строку ждёт человек, и долбить в неё
        # проходами значит жечь бюджет.
        s = self.stand(plan="паркуй")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(len(s.orders()), 1)
        self.assertIn("ждёт человека", r.stdout)

    def test_user_acceptance_stops_the_pipeline(self):
        s = self.stand(sect="check-user")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(s.orders(), [])
        self.assertIn("приёмки человеком", r.stdout)

    def test_funnel_stops_after_three_idle_passes(self):
        # Голова выходит быстро и строку не двигает: обычно это сломанное
        # окружение, и шесть попыток тут ничем не лучше трёх.
        s = self.stand(plan="работа")
        r = s.run("--passes", "6")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(len(s.orders()), 3)
        self.assertIn("вхолостую", r.stdout)

    def test_passes_run_out(self):
        s = self.stand(plan="падение")
        r = s.run("--passes", "2")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(len(s.orders()), 2)
        self.assertIn("проходы исчерпаны", r.stdout)

    def test_head_crash_does_not_stop_the_pipeline(self):
        # Код возврата головы тут не вердикт: вердикт это статус строки, и
        # упавший проход поднимается следующим.
        s = self.stand(plan="падение|закрой")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(len(s.orders()), 2)

    def test_board_silence_is_not_a_blind_launch(self):
        s = self.stand()
        env = s.env()
        env["PATH"] = os.environ.get("PATH", "")
        argv = [sys.executable, RUN, "DK-1", "-C", s.root, "--",
                os.path.join(s.bin, "claude-stub")]
        r = subprocess.run(argv, capture_output=True, text=True, env=env)
        self.assertEqual(r.returncode, 2)
        self.assertEqual(s.orders(), [])


class TestLiveHead(unittest.TestCase):
    """Голова не выходит: проходы приходят репликами в одну живую сессию, а
    конец прохода оболочка узнаёт отметкой хука."""

    def setUp(self):
        self.stands = []

    def tearDown(self):
        for s in self.stands:
            s.drop()

    def stand(self, **kw):
        s = Stand(live=True, **kw)
        self.stands.append(s)
        return s

    def test_one_process_serves_every_pass(self):
        # Прежде каждый проход поднимал своего клиента, и всё, чего голова не
        # дождалась, гибло вместе с процессом (DK-720, шесть проходов подряд).
        s = self.stand(plan="работа|закрой")
        got = s.run("--order", "Выполни DK-1", "--again", "Продолжай DK-1")
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertEqual(s.orders(), ["Выполни DK-1", "Продолжай DK-1"], s.orders())
        self.assertEqual(len(set(s.lines("pids"))), 1, s.lines("pids"))

    def test_long_step_lives_through_the_pass(self):
        # Живучесть долгого шага: работа, отданная первым проходом наружу,
        # доживает до конца, потому что процесс головы не выходит между
        # проходами и не уносит её с собой.
        s = self.stand(plan="фон|ждать фон|закрой")
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertEqual(len(set(s.lines("pids"))), 1, s.lines("pids"))
        self.assertEqual(s.lines("фон"), ["готово"], "долгий шаг не доработал")

    def test_live_head_drops_the_headless_mark(self):
        # Метку печатного режима ставит дашборд, а рубеж синхронности по ней
        # отбивает фоновый ход (DK-678). Живому окну она не по адресу: фоновый
        # ребёнок тут переживает конец хода.
        s = self.stand(plan="закрой")
        s.run()
        self.assertEqual(s.lines("headless"), [], s.lines("headless"))

    def test_headless_pass_keeps_the_mark(self):
        s = self.stand(plan="закрой")
        s.run("--headless")
        self.assertEqual(s.lines("headless"), ["дашборд"], s.lines("headless"))

    def test_registry_holds_one_record_for_the_task(self):
        # Предмет DK-723: череда проходов давала по записи на проход, и список
        # чатов дашборда рисовал задачу столькими строками, сколько было
        # проходов.
        s = self.stand(plan="работа|работа|закрой")
        s.run()
        rows = [l for l in s.lines(os.path.join(".devkit", "sessions.log"))
                if " tmux task-DK-1 " in l]
        self.assertEqual(len(rows), 1, rows)

    def test_headless_flag_returns_the_passes(self):
        # Запасной вход остаётся рабочим: по флагу конвейер идёт проходами, и
        # каждый проход это свой процесс со своим заказом через -p.
        s = self.stand(plan="работа|закрой")
        got = s.run("--headless", "--order", "Выполни DK-1", "--again", "Продолжай DK-1")
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertEqual(s.orders(), ["Выполни DK-1", "Продолжай DK-1"], s.orders())

    def test_no_live_window_returns_the_passes(self):
        # Машина без tmux и окно, которого нет: живую голову вести негде.
        s = Stand(plan="работа|закрой")
        self.stands.append(s)
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertEqual(len(s.orders()), 2, s.orders())

    def test_standing_question_calls_the_human(self):
        # Права машинного контура покрывают не всё, и сессия, вставшая на
        # незнакомом запросе разрешения, снаружи неотличима от долгой работы.
        s = self.stand(plan="вопрос|закрой")
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        said = [l for l in s.journal() if "ждёт человека" in l]
        self.assertTrue(said, s.journal())
        self.assertIn("permission_prompt", said[0])

    def test_mute_session_is_named_aloud(self):
        # Ни одной отметки за срок: хук отметки хода не подключён либо окно
        # замёрзло. Молчание тут неотличимо от работы, и назвать его надо вслух.
        s = self.stand(plan="молчит|закрой", mute="1")
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertTrue([l for l in s.journal() if "молчит" in l], s.journal())

    def test_human_reply_holds_the_order(self):
        # Реплику человека панель подаёт в это же окно, и свой заказ поверх неё
        # встал бы второй строкой ввода.
        s = self.stand(plan="чужой|закрой", pause="1")
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertTrue([l for l in s.journal() if "ход уже начат репликой человека" in l],
                        s.journal())
        self.assertEqual(len(s.orders()), 2, s.orders())

    def test_trimmed_journal_does_not_repeat_a_turn(self):
        # Журнал отметок общий на машину и режется по размеру. Оболочка, что
        # читала его по смещению, после реза перечитывала файл с начала и
        # принимала свою прошлую отметку за конец текущего прохода. Заказ тогда
        # уходил поверх идущего хода.
        s = self.stand(plan="работа|ротация|закрой")
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertEqual(s.lines("поверх"), [], "заказ ушёл поверх идущего хода")
        self.assertEqual(len(s.orders()), 3, s.orders())
        # Проходов было три, и строк о конце прохода в журнале ровно три.
        # Лишняя это призрак: оболочка сочла проход кончившимся по своей же
        # старой отметке, вернувшейся после реза.
        ends = [l for l in s.journal() if "конец прохода живой головы" in l]
        self.assertEqual(len(ends), 3, ends)

    def test_lost_journal_calls_the_human(self):
        # Журнала отметок нет вовсе: снят руками, стёрт уборкой дома. Ждать
        # конца прохода тут можно до скончания века, и молчать об этом нельзя.
        s = self.stand(plan="работа|потеря|закрой", mute="1")
        got = s.run()
        self.assertEqual(got.returncode, 0, s.why(got))
        self.assertTrue([l for l in s.journal() if "молчит" in l], s.journal())
        self.assertEqual(len(s.orders()), 3, s.orders())

    def test_dead_head_stops_the_pipeline(self):
        # Клиент вышел раньше задачи: окно закрывается, и с экрана дашборда это
        # неотличимо от штатного конца.
        s = self.stand(plan="падение")
        got = s.run()
        self.assertEqual(got.returncode, 1, s.why(got))
        self.assertTrue([l for l in s.journal() if "живая голова вышла" in l], s.journal())


class TestJournal(unittest.TestCase):
    """Чтение общего журнала: своя строка узнаётся по себе самой, и второй раз
    оболочка её не читает."""

    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="task-run-journal-")
        self.path = os.path.join(self.root, "turns.log")
        self.pipe = task_run.Pipeline(task_run.parse_args(
            ["DK-1", "-C", self.root, "--", "claude"]))
        self.pipe.sid = SID

    def tearDown(self):
        shutil.rmtree(self.root, ignore_errors=True)

    def write(self, *lines):
        with open(self.path, "a", encoding="utf-8") as f:
            for l in lines:
                f.write(l + "\n")

    def turn(self, word, when="2026-09-03T12:00:00"):
        return "%s сессия %s ход %s повод - дерево /tmp" % (when, SID[:8], word)

    def read(self, seen):
        return self.pipe.fresh(self.path, seen, self.pipe.own_mark)

    def test_own_marks_are_read_once(self):
        self.write(self.turn("кончен"), "2026-09-03T12:00:00 сессия ffff0001 ход кончен повод - дерево /tmp")
        got, seen = self.read({})
        self.assertEqual(len(got), 1, got)
        self.assertEqual(self.read(seen)[0], [], "своя отметка прочиталась второй раз")

    def test_trimmed_journal_gives_nothing_new(self):
        # Рез журнала это перезапись файла последними строками. Читатель со
        # смещением брал такой файл сначала и возвращал уже прочитанное.
        self.write(self.turn("кончен"))
        _, seen = self.read({})
        with open(self.path, encoding="utf-8") as f:
            tail = f.readlines()
        with open(self.path, "w", encoding="utf-8") as f:
            f.writelines(tail)
        self.assertEqual(self.read(seen)[0], [], "усушка вернула прочитанную отметку")

    def test_two_marks_in_one_second_are_two_marks(self):
        # Время харнес пишет до секунды, и мгновенно отбитый ход даёт две
        # дословно одинаковых строки. Память множеством считала бы их одной.
        self.write(self.turn("кончен"), self.turn("кончен"))
        got, seen = self.read({})
        self.assertEqual(len(got), 2, got)
        self.assertEqual(self.read(seen)[0], [], got)

    def test_unfinished_line_waits_for_its_newline(self):
        # Рез переписывает файл целиком, и чтение попадает на середину записи.
        with open(self.path, "w", encoding="utf-8") as f:
            f.write(self.turn("кончен"))
        self.assertEqual(self.read({})[0], [], "обрывок прочитан как отметка")
        self.write("")
        self.assertEqual(len(self.read({})[0]), 1)

    def test_missing_journal_keeps_the_memory(self):
        self.write(self.turn("кончен"))
        _, seen = self.read({})
        os.remove(self.path)
        got, after = self.read(seen)
        self.assertEqual(got, [])
        self.assertEqual(after, seen, "память забылась на пропавшем журнале")


if __name__ == "__main__":
    unittest.main(verbosity=0)
