#!/usr/bin/env python3
"""Самопроверка оболочки цели goal-run.py. Витки играет стаб вместо claude, как
парсер панели /usage гоняется по образцам: настоящих сессий тут не
поднимается ни одной. Каждый стенд это свой синтетический проект с доской,
файлом цели и временным HOME, чтобы уведомитель писал свой журнал в него.
Стаб клиента и стаб tmux сами написаны на python и живут только во временной
директории теста: файлами репозитория они не становятся, как и любая другая
фикстура, изображающая чужую программу.
"""
import importlib.util
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(HERE, "goal-run.py")
HOOKS = os.path.normpath(os.path.join(HERE, "..", "..", "..", "hooks"))
sys.path.insert(0, HOOKS)
# Носитель цели у ключа --ask общий с подхватом реплики, и форматы сверяются с
# ним самим. Дефис в имени файла хука не годится для import, поэтому модуль
# грузится по пути, как его грузит и сама оболочка.
_spec = importlib.util.spec_from_file_location(
    "devkit_chat_in", os.path.join(HOOKS, "chat-in.py"))
chat_in = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(chat_in)

GOAL_MD = """# DK-100: Цель: синтетическая цель обкатки

## Цель

DoD: стенд отработал.

## Бюджет

бюджет: week_all <= 10

## Задачи цели

Заводит нарезка первым витком.

## Журнал

## Итог

Пишет последний виток.
"""

# Стаб клиента стенда: играет очередную строку сценария. Строка сценария это
# «маркер что-делает-с-журналом»: «запись» дописывает содержательную строку в
# «Журнал» цели в том формате, что пишет agentctl lap (часы витка, что сделано,
# маркер), «снимок» строку снимка квоты, «парковка» уводит строку
# доски в Blocked с причиной-вопросом и пишет ход, «выбор» пишет ход о взятой
# новой работе, всё остальное молчит. Особые
# маркеры: none (ответ без маркера), fenced (маркер в ограде), crash (ненулевой
# код возврата), trailing (текст после маркера на той же строке), leading
# (текст перед маркером на той же строке), dotted (точка после маркера),
# blankreal (маркер, пустая строка, а за ней ещё строка ответа) - все пять
# последних это формы, которые постановка называет прямо: маркер идёт голой
# строкой и сравнивается целиком, а не префиксом. Слово «замок» в строке
# поднимает вторую оболочку той же цели. Сценарий кончился, значит
# повторяется последняя строка: цикл, который не остановился, так и крутится.
# Корень стенда и путь до goal-run.py читаются из окружения, чтобы один и тот
# же файл стаба годился для любого стенда.
CLAUDE_STUB = r'''#!/usr/bin/env python3
import os
import subprocess
import sys

root = os.environ["STAND_ROOT"]
goal = os.path.join(root, "proj", "docs", "tasks", "DK-100.md")

with open(os.path.join(root, "calls"), "a", encoding="utf-8") as f:
    f.write(" ".join(sys.argv[1:]) + "\n")
# Имя витка снимается прямо во время витка: после цикла замка уже нет, а
# вопрос теста именно в том, чьё имя лежало в замке, пока виток шёл.
try:
    with open(os.path.join(root, "proj", ".devkit", "goal-DK-100.lock", "session"),
              encoding="utf-8") as f:
        named = f.read().strip()
except OSError:
    named = "-"
with open(os.path.join(root, "sessions"), "a", encoding="utf-8") as f:
    f.write(named + "\n")
with open(os.path.join(root, "calls"), encoding="utf-8") as f:
    turn = sum(1 for _ in f)
with open(os.path.join(root, "turns"), encoding="utf-8") as f:
    turns = [l.rstrip("\n") for l in f]

# Потолок витков: сломанная оболочка, которая не умеет останавливаться, иначе
# крутила бы стенд вечно, и провал выглядел бы зависшим прогоном.
if turn > 10:
    print("стаб: витков больше десяти, оболочка не остановилась")
    print("marker: done")
    sys.exit(0)

spec = turns[turn - 1] if turn - 1 < len(turns) else (turns[-1] if turns else "")
parts = spec.split()
marker = parts[0] if parts else ""

entry = ""
if "запись" in parts:
    entry = "- 2026-08-0%d 09:00-11:30, виток %d, задача цели закрыта; %s" % (turn, turn, marker)
elif "снимок" in parts:
    entry = "- снимок 2026-08-0%d: week_all %d%%" % (turn, turn)
if entry:
    with open(goal, encoding="utf-8") as f:
        lines = f.read().split("\n")
    out = []
    for line in lines:
        out.append(line)
        if line.startswith("## Журнал"):
            out.append("")
            out.append(entry)
    with open(goal, "w", encoding="utf-8") as f:
        f.write("\n".join(out))

if "замок" in parts:
    lock = os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")
    if os.path.isdir(lock):
        with open(os.path.join(root, "lock-probe"), "a", encoding="utf-8") as f:
            f.write("замок на месте\n")
    p = subprocess.run([os.environ["GOAL_RUN"], "DK-100", "-C",
                        os.path.join(root, "proj"), "--foreground"],
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    with open(os.path.join(root, "second.out"), "w", encoding="utf-8") as f:
        f.write(p.stdout)
        f.write("код %d\n" % p.returncode)

# Слово «ход» в строке сценария: виток пишет строку хода тем же ключом, каким
# её пишет живой виток по скиллу, и заодно снимает журнал в момент своей
# работы. Снимок этот и есть ответ на вопрос задачи DK-117: видно ли ход
# витка, пока виток идёт, а не после него.
if "ход" in parts:
    log = os.path.join(root, "proj", ".devkit", "goal-DK-100.log")
    during = ""
    if os.path.isfile(log):
        with open(log, encoding="utf-8") as f:
            during = f.read()
    with open(os.path.join(root, "log-during"), "w", encoding="utf-8") as f:
        f.write(during)
    subprocess.run([os.environ["GOAL_RUN"], "DK-100", "-C", os.path.join(root, "proj"),
                    "--say", "стаб: ход витка %d" % turn],
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

# Слово «парковка» в строке сценария: виток паркует задачу вопросом по
# правилу DK-402 (строка уходит в Blocked с машинным префиксом причины) и
# пишет ход о парковке, слово «выбор» следующим витком берёт новую работу и
# тоже пишет ход. Так стенд играет отвязку стопа от задачного повода (DK-403):
# оболочка на парковке не останавливается, и видно это по строкам хода.
if "парковка" in parts:
    board = os.path.join(root, "proj", "docs", "TASKS.md")
    with open(board, encoding="utf-8") as f:
        b = f.read()
    if "## Blocked" not in b:
        b += "\n## Blocked\n"
    with open(board, "w", encoding="utf-8") as f:
        f.write(b + "- DK-101 [блок: вопрос: ждём схемы]\n")
if "парковка" in parts or "выбор" in parts:
    note = "задача припаркована вопросом" if "парковка" in parts \
        else "взята новая задача DK-102"
    subprocess.run([os.environ["GOAL_RUN"], "DK-100", "-C", os.path.join(root, "proj"),
                    "--say", "стаб витка %d: %s" % (turn, note)],
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

print("виток стаба %d" % turn)
if marker == "none":
    print("работа осталась, но маркер сказать забыли")
    sys.exit(0)
if marker == "fenced":
    print("marker: `continue`")
    sys.exit(0)
if marker == "crash":
    print("клиент упал")
    sys.exit(7)
if marker == "trailing":
    print("marker: continue выполнено")
    sys.exit(0)
if marker == "leading":
    print("готово, marker: continue")
    sys.exit(0)
if marker == "dotted":
    print("marker: continue.")
    sys.exit(0)
if marker == "blankreal":
    print("marker: continue")
    print("")
    print("ещё текст после маркера")
    sys.exit(0)
print("marker: %s" % marker)
'''

# Стаб tmux: только логирует вызов и отвечает на has-session по файлу-флагу.
TMUX_STUB = r'''#!/usr/bin/env python3
import os
import sys

root = os.environ["STAND_ROOT"]
with open(os.path.join(root, "tmux-calls"), "a", encoding="utf-8") as f:
    f.write(" ".join(sys.argv[1:]) + "\n")
if len(sys.argv) > 1 and sys.argv[1] == "has-session":
    sys.exit(0 if os.path.isfile(os.path.join(root, "tmux-has")) else 1)
sys.exit(0)
'''


def write_exec(path, content):
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)
    os.chmod(path, 0o755)


class Stand:
    """Синтетический проект с доской, целью и своим HOME. Живёт отдельным
    классом, потому что стенд нужен двум наборам, циклу и ключу --ask."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.n = 0

    def stand(self, *specs):  # новый стенд, аргументы это строки сценария стаба
        self.n += 1
        root = os.path.join(self.tmp, "s%d" % self.n)
        os.makedirs(os.path.join(root, "proj", ".devkit"))
        os.makedirs(os.path.join(root, "proj", "docs", "tasks"))
        os.makedirs(os.path.join(root, "bin"))
        os.makedirs(os.path.join(root, "home"))
        with open(os.path.join(root, "proj", "docs", "TASKS.md"), "w", encoding="utf-8") as f:
            f.write("# Доска\n\nПрефикс: DK\n\n## In progress\n")
        with open(os.path.join(root, "proj", "docs", "tasks", "DK-100.md"), "w", encoding="utf-8") as f:
            f.write(GOAL_MD)
        with open(os.path.join(root, "proj", ".devkit", "deploy.local"), "w", encoding="utf-8") as f:
            f.write("deploy = true\nautonomous = true\n")
        # Права машинного контура стенду раскладывает тот же код, что и на
        # машине: без них оболочка отказывает предполётной проверкой, и до
        # витков дело не доходит вовсе.
        perms = os.path.normpath(os.path.join(HERE, "..", "..", "..", "tools", "devkitctl", "perms.py"))
        env = dict(os.environ, HOME=os.path.join(root, "home"))
        p = subprocess.run(["python3", perms, "--fix"], env=env,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        self.assertEqual(p.returncode, 0, "стенду не разложились права машинного контура: %s" % p.stdout)
        with open(os.path.join(root, "turns"), "w", encoding="utf-8") as f:
            for spec in specs:
                f.write(spec + "\n")
        write_exec(os.path.join(root, "bin", "claude"), CLAUDE_STUB)
        return root

    def env(self, root, pause="0"):
        env = dict(os.environ)
        env["PATH"] = os.path.join(root, "bin") + os.pathsep + env.get("PATH", "")
        env["HOME"] = os.path.join(root, "home")
        env["STAND_ROOT"] = root
        env["GOAL_RUN"] = RUN
        # Пауза между попытками поднять вставший виток: живому циклу она нужна
        # (обрыв на стороне API немедленный повтор встретит тем же обрывом),
        # стенду она только добавляет секунд, поэтому по умолчанию ноль.
        env["DEVKIT_GOAL_RETRY_PAUSE"] = pause
        return env

    def goal_run(self, root, *args, pause="0"):
        # -C всегда назван явно, как в реальном вызове из SKILL.md: без него
        # оболочка берёт корень из pwd процесса, а голый os.getcwd() в python
        # разворачивает симлинк /var -> /private/var на macOS там, где
        # логический pwd шелла этого не делает, и путь в сообщении разошёлся
        # бы с тем, что ушло в -C у стенда.
        env = self.env(root, pause)
        proj = os.path.join(root, "proj")
        return subprocess.run([RUN, "-C", proj] + list(args), cwd=proj, env=env,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)

    def turns_done(self, root):
        try:
            with open(os.path.join(root, "calls"), encoding="utf-8") as f:
                return sum(1 for _ in f)
        except OSError:
            return 0

    def shell_log(self, root):
        try:
            with open(os.path.join(root, "proj", ".devkit", "goal-DK-100.log"), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def notify_log(self, root):
        try:
            with open(os.path.join(root, "home", ".devkit", "notify.log"), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""


class GoalRunTests(Stand, unittest.TestCase):
    # -- цикл до маркера done -------------------------------------------------

    def test_continue_advances_turns_until_done(self):
        # continue поднимает следующий виток, done завершает. Заодно тут
        # проверяются промпт витка, журнал оболочки, громкое уведомление на
        # стопе и снятый замок.
        root = self.stand("continue запись", "continue запись", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 3)
        with open(os.path.join(root, "calls"), encoding="utf-8") as f:
            first_call = f.readline()
        self.assertIn("продолжай цель DK-100 по скиллу goal-loop", first_call)
        log = self.shell_log(root)
        self.assertIn("виток 1 маркер continue код 0", log)
        self.assertIn("виток 3 маркер done код 0", log)
        self.assertIn("остановлен: done", log)
        self.assertIn("уровень громкий", self.notify_log(root))
        self.assertFalse(os.path.isdir(os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")),
                         "замок остался после цикла")

    def test_each_terminal_marker_stops_on_first_turn(self):
        # Каждый маркер, кроме continue, завершает цикл первым же витком.
        for marker in ("done", "over", "wait-human", "stuck"):
            root = self.stand("%s запись" % marker)
            p = self.goal_run(root, "DK-100", "--foreground")
            self.assertEqual(p.returncode, 0, "маркер %s вернул не 0: %s" % (marker, p.stdout))
            self.assertEqual(self.turns_done(root), 1, "маркер %s не завершил цикл" % marker)
            log = self.shell_log(root)
            self.assertIn("остановлен: %s" % marker, log)
            self.assertIn("уровень громкий", self.notify_log(root))
            # Повод стопа едет в журнал уведомителя словом: лента дашборда
            # отличает по нему зов человека от вставшего цикла, а по тексту
            # хвоста показывает, о чём был стоп.
            key = "wait_human" if marker == "wait-human" else "goal_stop"
            self.assertIn("повод %s" % key, self.notify_log(root))
            self.assertIn("текст «цель DK-100: %s»" % marker, self.notify_log(root))
            # Цель едет полем строки, а не одним заголовком (DK-323): по нему
            # лента дашборда ведёт от стопа к строке цели и к журналу агента.
            self.assertIn("задача DK-100 проект", self.notify_log(root))

    def test_task_question_parks_and_goal_question_stops(self):
        # Отвязка стопа от задачного повода (DK-403, решение 6 LLD DK-400).
        # Вопрос, адресованный задаче, паркует её по механике DK-402, а цикл
        # не останавливается: следующий виток печатает выбор новой работы, и
        # остановлен цикл только своим финальным маркером. Вопрос уровня цели
        # стоп держит: wait-human завершает цикл первым же витком и зовёт
        # человека поводом wait_human.
        root = self.stand("continue парковка запись", "continue выбор запись", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 3, "цикл остановился на парковке задачи")
        log = self.shell_log(root)
        self.assertIn("задача припаркована вопросом", log, "ход о парковке не доехал до журнала")
        self.assertIn("взята новая задача", log, "выбор новой работы не напечатан")
        self.assertIn("остановлен: done", log)
        self.assertNotIn("wait-human", log, "задачный повод остановил цикл, это ошибка витка")
        with open(os.path.join(root, "proj", "docs", "TASKS.md"), encoding="utf-8") as f:
            self.assertIn("[блок: вопрос:", f.read(), "задача не припаркована на доске стенда")
        goal = self.stand("wait-human запись")
        p = self.goal_run(goal, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(goal), 1, "вопрос уровня цели не остановил цикл")
        self.assertIn("остановлен: wait-human", self.shell_log(goal))
        self.assertIn("повод wait_human", self.notify_log(goal))

    # -- воронка --------------------------------------------------------------

    def test_funnel_stops_on_third_silent_turn(self):
        # Воронка: витки говорят continue и в «Журнал» ничего не пишут. Стоп
        # ровно на третьем, а не на втором и не на четвёртом.
        root = self.stand("continue молчок")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 1, p.stdout)
        self.assertEqual(self.turns_done(root), 3)
        log = self.shell_log(root)
        self.assertIn("остановлен: воронка", log)
        self.assertIn("уровень громкий", self.notify_log(root))

    def test_funnel_counter_resets_on_progress(self):
        # Счётчик воронки сбрасывается витком, который в «Журнал» записал.
        root = self.stand("continue молчок", "continue молчок", "continue запись",
                          "continue молчок", "continue молчок", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 6)

    def test_turn_line_with_times_counts_as_progress(self):
        # Формат строки витка: часы витка, что сделано, маркер. Времена в начале
        # строки продвижением витка быть не мешают, а снимок квоты им остаётся
        # снимком: считает воронка не по началу строки, а по префиксу «снимок ».
        root = self.stand("continue запись", "continue молчок", "continue молчок",
                          "continue запись", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 5)
        with open(os.path.join(root, "proj", "docs", "tasks", "DK-100.md"),
                  encoding="utf-8") as f:
            journal = f.read()
        self.assertIn("- 2026-08-01 09:00-11:30, виток 1, задача цели закрыта; continue",
                      journal)

    def test_quota_snapshot_does_not_count_as_progress(self):
        # Строка снимка квоты продвижением не считается: её дописывает гейт
        # бюджета и у витка, который не сделал ничего.
        root = self.stand("continue снимок")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 1)
        self.assertEqual(self.turns_done(root), 3)

    # -- замок ------------------------------------------------------------

    def test_second_shell_refuses_while_first_holds_the_lock(self):
        # Замок на цель: пока цикл идёт, вторая оболочка той же цели отказывает.
        root = self.stand("continue запись замок", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        with open(os.path.join(root, "lock-probe"), encoding="utf-8") as f:
            self.assertIn("замок на месте", f.read())
        with open(os.path.join(root, "second.out"), encoding="utf-8") as f:
            second = f.read()
        self.assertIn("уже идёт", second)
        self.assertIn("код 3", second)
        self.assertEqual(self.turns_done(root), 2, "вторая оболочка успела поднять свои витки")

    def test_stale_lock_is_reclaimed_but_live_owner_is_respected(self):
        # Чужой замок с живым владельцем уважается, а брошенный снимается:
        # оболочка, убитая вместе с окном tmux, не должна запирать цель до
        # ручной уборки.
        root = self.stand("done запись")
        lock = os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")
        os.makedirs(lock)
        alive = subprocess.Popen(["sleep", "30"])
        with open(os.path.join(lock, "pid"), "w", encoding="utf-8") as f:
            f.write("%d\n" % alive.pid)
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 3, "оболочка полезла в цель, которую ведёт живой владелец замка")
        alive.kill()
        alive.wait()
        with open(os.path.join(lock, "pid"), "w", encoding="utf-8") as f:
            f.write("%d\n" % alive.pid)
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, "брошенный замок не снялся: %s" % p.stdout)
        self.assertEqual(self.turns_done(root), 1, "цикл после брошенного замка не пошёл")

    def test_each_turn_is_raised_with_its_own_name_in_the_lock(self):
        # Имя витка это третий шаг адреса подхвата реплики: он сверяет session_id
        # события с файлом session в замке. Имя выдаётся на виток, а не на
        # замок, иначе второй виток поднимался бы с занятым именем, а реплика
        # после первого витка молча не доезжала бы никуда.
        root = self.stand("continue запись", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        with open(os.path.join(root, "calls"), encoding="utf-8") as f:
            calls = [l.split() for l in f.read().splitlines()]
        self.assertEqual(len(calls), 2, calls)
        for c in calls:
            self.assertIn("--session-id", c, "виток поднят без имени сессии: %s" % c)
        ids = [c[c.index("--session-id") + 1] for c in calls]
        self.assertEqual(len(set(ids)), 2, "оба витка подняты одним именем: %s" % ids)
        with open(os.path.join(root, "sessions"), encoding="utf-8") as f:
            named = f.read().split()
        self.assertEqual(ids, named, "в файле session лежит имя не того витка, что идёт")

    def test_release_takes_the_whole_lock_directory(self):
        # Замок снимается целиком, вместе с именем витка: перечень известных
        # имён оставил бы каталог непустым, а rmdir ушёл бы в ENOTEMPTY.
        root = self.stand("done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertFalse(os.path.exists(os.path.join(root, "proj", ".devkit",
                                                     "goal-DK-100.lock")),
                         "каталог замка пережил цикл")

    def test_stale_lock_with_a_session_file_is_reclaimed(self):
        # Оболочка, убитая kill -9 или закрытым окном tmux, оставляет в замке и
        # pid, и имя витка. Следующий запуск снимает такой замок целиком, иначе
        # брошенный цикл чинился бы с клавиатуры ровно в том режиме, ради
        # которого заведён живой канал.
        root = self.stand("done запись")
        lock = os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")
        os.makedirs(lock)
        dead = subprocess.Popen(["sleep", "0"])
        dead.wait()
        with open(os.path.join(lock, "pid"), "w", encoding="utf-8") as f:
            f.write("%d\n" % dead.pid)
        with open(os.path.join(lock, "session"), "w", encoding="utf-8") as f:
            f.write("имя витка прошлой оболочки\n")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, "брошенный замок с именем витка не снялся: %s" % p.stdout)
        self.assertEqual(self.turns_done(root), 1, "цикл после брошенного замка не пошёл")

    # -- стоп без вердикта и попытки ----------------------------------------

    def assert_no_verdict(self, root, p, what):
        # Виток без вердикта поднимается заново, и цикл встаёт исчерпанными
        # попытками, а не первым же таким витком. Сколько было витков, столько
        # и попыток: строка сценария у стенда повторяется. Проверка «не
        # continue» тут в том, что цикл не ушёл по сценарию дальше, а
        # остановился поводом попыток.
        self.assertEqual(p.returncode, 1, "%s сошло за вердикт: %s" % (what, p.stdout))
        log = self.shell_log(root)
        self.assertIn("остановлен: попытки исчерпаны", log, "%s: цикл встал не на попытках" % what)
        self.assertNotIn("маркер continue", log, "%s разобрано как continue" % what)
        self.assertEqual(self.turns_done(root), 3, "%s: витков не по пределу попыток" % what)

    def test_stop_without_verdict_restarts_the_turn(self):
        # Обрыв середины ответа убивает сессию витка, а не цель: состояние на
        # диске, и виток поднимается заново той же командой. Два обрыва подряд
        # цикл переживает, третий виток доводит цель до маркера.
        root = self.stand("crash", "crash", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 3, "оболочка не подняла виток заново")
        log = self.shell_log(root)
        self.assertIn("перезапуск после стопа без вердикта", log)
        self.assertIn("попытка 2 из 3", log)
        self.assertIn("остановлен: done", log)

    def test_retry_limit_stops_the_loop_loudly(self):
        # Сломанный виток не долбится вечно: три попытки подряд, и это громкий
        # стоп с названным поводом, а не тихое зацикливание.
        root = self.stand("crash")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 1, p.stdout)
        self.assertEqual(self.turns_done(root), 3, "предел попыток не удержал цикл")
        log = self.shell_log(root)
        self.assertIn("остановлен: попытки исчерпаны", log)
        self.assertIn("уровень громкий", self.notify_log(root))

    def test_retry_counter_resets_on_a_turn_with_a_verdict(self):
        # Счётчик попыток считает стопы подряд: виток, ответивший маркером,
        # его сбрасывает, иначе редкие обрывы за длинный прогон складывались бы
        # в ложный стоп.
        root = self.stand("crash", "continue запись", "crash", "crash", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 5, "счётчик попыток не сбросился витком с маркером")

    def test_retry_waits_between_attempts(self):
        # Между попытками цикл ждёт: обрыв на стороне API немедленный повтор
        # встретит тем же обрывом. Пауза стенда секунда, живая двадцать.
        root = self.stand("crash")
        began = time.time()
        p = self.goal_run(root, "DK-100", "--foreground", pause="1")
        self.assertEqual(p.returncode, 1, p.stdout)
        self.assertGreaterEqual(time.time() - began, 2, "перезапуск пошёл без паузы")
        self.assertIn("через 1 с", self.shell_log(root))

    def test_terminal_marker_is_not_retried(self):
        # Граница перезапуска: вердикт остаётся стопом. Виток сказал
        # wait-human, и второго витка нет.
        root = self.stand("wait-human запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.turns_done(root), 1, "вердикт витка пошёл на перезапуск")
        self.assertNotIn("перезапуск", self.shell_log(root))

    def test_funnel_is_not_reset_by_retries(self):
        # Воронка и попытки считаются раздельно, но воронку перезапуск не
        # обнуляет: молчащие continue доводят цикл до стопа воронкой, даже
        # если между ними виток обрывался.
        root = self.stand("continue молчок", "crash", "continue молчок", "continue молчок",
                          "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 1, p.stdout)
        self.assertIn("остановлен: воронка", self.shell_log(root))
        self.assertEqual(self.turns_done(root), 4, "обрыв посреди воронки сбросил её счётчик")

    # -- авария витка -----------------------------------------------------

    def test_answer_without_marker_is_a_crash(self):
        root = self.stand("none запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "ответ без маркера")

    def test_fenced_marker_is_not_a_marker(self):
        root = self.stand("fenced запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "маркер в ограде")

    def test_marker_with_trailing_text_is_not_a_marker(self):
        # Постановка требует сравнение целиком: строка «marker: continue
        # выполнено» маркером не считается, хотя и начинается с него. Именно
        # эта форма ловит мутацию «== заменили на startswith» в сравнении
        # маркера (ревью DK-143): под ней такая строка проходит как continue,
        # а тест обязан на этом упасть.
        root = self.stand("trailing запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "текст после маркера на той же строке")

    def test_marker_with_leading_text_is_not_a_marker(self):
        # Симметричная форма: текст перед маркером на той же строке. Startswith
        # её и так не пропустит (строка не начинается с «marker:»), но
        # постановка называет обе формы прямо, и разбор целиком обязан
        # отклонять и эту.
        root = self.stand("leading запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "текст перед маркером на той же строке")

    def test_marker_with_trailing_dot_is_not_a_marker(self):
        # Точка в конце строки: тоже префикс «marker: continue», и тоже
        # ловит ту же мутацию сравнения, что и текст после маркера.
        root = self.stand("dotted запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "точка после маркера")

    def test_marker_followed_by_more_text_after_blank_line_is_not_a_marker(self):
        # «После маркера не остаётся ничего, даже пустой строки»: пустая
        # строка сама по себе маркер не портит (её оставляет обычный print),
        # но настоящая строка ответа после неё означает, что маркер был не
        # последним, а где-то в середине, и разбор обязан взять истинную
        # последнюю строку, а не остановиться на маркере раньше срока.
        root = self.stand("blankreal запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "текст после маркера через пустую строку")

    def test_client_crash_is_a_stop_without_verdict(self):
        root = self.stand("crash запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assert_no_verdict(root, p, "упавшая сессия витка")
        self.assertIn("авария витка", self.shell_log(root))

    # -- ход витка в журнале --------------------------------------------------

    def log_during_turn(self, root):  # журнал, снятый витком посреди его работы
        try:
            with open(os.path.join(root, "log-during"), encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def test_log_grows_while_the_turn_is_still_running(self):
        # Регресс DK-117: журнал цели наполнялся только между витками, потому
        # что вывод `claude -p` буферизуется до конца сессии. Работающий виток
        # выглядел снаружи так же, как вставший, и на прогоне DK-109 чат молчал
        # час. Теперь строку подъёма пишет оболочка до сессии, а ход пишет сам
        # виток ключом --say, и обе строки видны, пока виток идёт.
        root = self.stand("continue ход запись", "done запись")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 0, p.stdout)
        during = self.log_during_turn(root)
        self.assertIn("виток 1 поднят", during, "журнал молчал, пока виток шёл")
        self.assertNotIn("виток 1 маркер", during, "итог витка лёг в журнал раньше конца витка")
        log = self.shell_log(root)
        self.assertIn("стаб: ход витка 1", log)
        self.assertLess(log.index("стаб: ход витка 1"), log.index("виток 1 маркер"),
                        "строка хода легла в журнал позже итога витка")

    def test_say_appends_progress_line_without_raising_the_loop(self):
        # Ключ --say пишет строку хода и возвращается: ни витка, ни замка, ни
        # tmux-сессии. Формат строки тот же, что у строк оболочки, иначе один
        # tail показывал бы два разных журнала в одном файле.
        root = self.stand("done запись")
        p = self.goal_run(root, "DK-100", "--say", "DK-101 отдан исполнителю")
        self.assertEqual(p.returncode, 0, p.stdout)
        p = self.goal_run(root, "DK-100", "--say", "DK-101 ушла в ревью")
        self.assertEqual(p.returncode, 0, p.stdout)
        lines = [l for l in self.shell_log(root).split("\n") if l]
        self.assertEqual(len(lines), 2, "строки хода не дописались, а перезаписали журнал")
        self.assertRegex(lines[0], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d DK-101 отдан исполнителю$")
        self.assertIn("DK-101 ушла в ревью", lines[1])
        self.assertIn("DK-101 ушла в ревью", p.stdout)
        self.assertFalse(os.path.isdir(os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")),
                         "строка хода подняла замок цикла")
        self.assertFalse(os.path.isfile(os.path.join(root, "calls")), "строка хода подняла виток")

    def test_say_works_where_the_loop_would_refuse_to_start(self):
        # Ход пишет и виток живого чата, а там нет ни autonomous, ни claude в
        # PATH: предполётные проверки оболочки строке хода не касаются.
        root = self.stand("done запись")
        with open(os.path.join(root, "proj", ".devkit", "deploy.local"), "w", encoding="utf-8") as f:
            f.write("deploy = true\nautonomous = false\n")
        os.remove(os.path.join(root, "bin", "claude"))
        p = self.goal_run(root, "DK-100", "--say", "виток чата: нарезка начата")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn("виток чата: нарезка начата", self.shell_log(root))

    def test_say_refusals(self):
        # Строка хода без текста, по цели без файла и вместе с циклом это
        # ошибка вызова: журнал чужой цели заводить нечем, а промахнувшийся ID
        # уводил бы ход в файл, который никто не читает.
        root = self.stand("done запись")
        p = self.goal_run(root, "DK-100", "--say")
        self.assertEqual(p.returncode, 2, "оболочка приняла --say без текста")
        p = self.goal_run(root, "DK-100", "--say", "   ")
        self.assertEqual(p.returncode, 2, "оболочка приняла пустую строку хода")
        p = self.goal_run(root, "DK-101", "--say", "ход не той цели")
        self.assertEqual(p.returncode, 2, "оболочка написала ход по цели, файла которой нет")
        self.assertFalse(os.path.isfile(os.path.join(root, "proj", ".devkit", "goal-DK-101.log")),
                         "журнал цели, которой нет, всё же заведён")
        p = self.goal_run(root, "DK-100", "--foreground", "--say", "и то и другое")
        self.assertEqual(p.returncode, 2, "оболочка приняла --say вместе с --foreground")
        self.assertEqual(self.shell_log(root), "", "отказавший вызов всё же тронул журнал")

    # -- отказы на старте ---------------------------------------------------

    def test_startup_refusals(self):
        # Без ID, без доски, без файла цели и при autonomous = false цикл не
        # поднимается вовсе.
        root = self.stand("done запись")
        p = self.goal_run(root, "--foreground")
        self.assertEqual(p.returncode, 2, "оболочка пошла без ID цели")

        p = self.goal_run(root, "DK-101", "--foreground")
        self.assertEqual(p.returncode, 2, "оболочка пошла по цели, файла которой нет")
        # Причина сверяется целиком, а не подстрокой: отказ называет скилл,
        # которым цель ставится, и на разрезе режима цели (DK-118) он уехал в
        # goal-start, а подстрока «файла цели» такую порчу пропускала. Человек
        # по этому тексту идёт заводить цель, так что неверное имя скилла это
        # не косметика.
        want = ("файла цели %s нет: цель ставит скилл goal-start, оболочка её не заводит"
                % os.path.join(root, "proj", "docs", "tasks", "DK-101.md"))
        self.assertEqual(p.stdout.rstrip("\n"), want)

        with open(os.path.join(root, "proj", ".devkit", "deploy.local"), "w", encoding="utf-8") as f:
            f.write("deploy = true\nautonomous = false\n")
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 2, "оболочка пошла при autonomous = false")
        self.assertIn("autonomous = true", p.stdout)

        with open(os.path.join(root, "proj", ".devkit", "deploy.local"), "w", encoding="utf-8") as f:
            f.write("deploy = true\nautonomous = true\n")
        os.rename(os.path.join(root, "proj", "docs", "TASKS.md"),
                  os.path.join(root, "proj", "docs", "BOARD.md"))
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 2, "оболочка пошла в проекте без доски")
        self.assertFalse(os.path.isfile(os.path.join(root, "calls")), "отказавшая оболочка успела поднять виток")

    # -- предполётная проверка прав ------------------------------------------

    def test_missing_machine_permissions_refuse_before_first_turn(self):
        # На машине без разложенного контура оболочка отказывает до первого
        # витка и называет команду починки, а не жжёт бюджет витками, которым
        # харнес отвечает отказом на каждый вызов.
        root = self.stand("done запись")
        os.remove(os.path.join(root, "home", ".claude", "settings.json"))
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 2, "оболочка пошла на машине без прав машинного контура")
        self.assertIn("не хватает прав машинного контура", p.stdout)
        self.assertIn("doctor --fix", p.stdout)
        self.assertFalse(os.path.isfile(os.path.join(root, "calls")), "оболочка без прав успела поднять виток")

    def test_partial_permissions_name_exactly_what_is_missing(self):
        # Права выданы наполовину: отказ тот же, и в нём названо ровно
        # недостающее.
        root = self.stand("done запись")
        settings = os.path.join(root, "home", ".claude", "settings.json")
        import json
        with open(settings, encoding="utf-8") as f:
            d = json.load(f)
        d["permissions"]["allow"] = [r for r in d["permissions"]["allow"] if r != "Bash(agentctl:*)"]
        with open(settings, "w", encoding="utf-8") as f:
            json.dump(d, f, ensure_ascii=False, indent=2)
        p = self.goal_run(root, "DK-100", "--foreground")
        self.assertEqual(p.returncode, 2, "оболочка пошла с наполовину выданными правами")
        self.assertIn("Bash(agentctl:*)", p.stdout)
        self.assertFalse(os.path.isfile(os.path.join(root, "calls")),
                         "оболочка с неполными правами успела поднять виток")

    # -- tmux ------------------------------------------------------------

    def test_without_foreground_goes_into_its_own_tmux_session(self):
        # Без --foreground цикл уходит в свою tmux-сессию, а поднятую сессию
        # той же цели оболочка второй раз не заводит.
        root = self.stand("done запись")
        write_exec(os.path.join(root, "bin", "tmux"), TMUX_STUB)
        p = self.goal_run(root, "DK-100")
        self.assertEqual(p.returncode, 0, "запуск в tmux вернул не 0")
        with open(os.path.join(root, "tmux-calls"), encoding="utf-8") as f:
            calls = f.read()
        self.assertIn("new-session -d -s goal-DK-100", calls)
        self.assertIn("--foreground", calls)
        self.assertIn("goal-DK-100", p.stdout)
        self.assertFalse(os.path.isfile(os.path.join(root, "calls")), "оболочка подняла виток мимо tmux")

        with open(os.path.join(root, "tmux-has"), "w", encoding="utf-8"):
            pass
        p = self.goal_run(root, "DK-100")
        self.assertEqual(p.returncode, 3, "оболочка полезла в цель с уже поднятой tmux-сессией")


class GoalAskTests(Stand, unittest.TestCase):
    """Ключ --ask: вопрос человеку с ожиданием ответа. Стенд тот же, что у
    цикла, но витков тут не поднимается ни одного: ключ зовёт уведомитель и
    читает «Входящие» файла цели, а отвечает за человека сам тест, дописывая
    строку в раздел.

    Форматы носителя проверяются против подхвата реплики, а не против самих
    себя: файл отметок и признак ожидания читает hooks/chat-in.py, и
    разъехавшийся формат оборачивается тем, что ответ приезжает витку дважды или
    что подхват
    съедает ответ на прямой вопрос."""

    MAIL = "goal-DK-100.mail"
    ASKFILE = "goal-DK-100.ask"

    def devfile(self, root, name):
        return os.path.join(root, "proj", ".devkit", name)

    def answer(self, root, *lines):
        """Ответ человека: строка ложится в «Входящие» файла цели ровно так,
        как её кладёт туда дашборд (addInboxLine, tools/dashboard/messages.go)."""
        path = os.path.join(root, "proj", "docs", "tasks", "DK-100.md")
        with open(path, encoding="utf-8") as f:
            doc = f.read()
        block = "## Входящие\n\n" + "".join("- %s\n" % line for line in lines) + "\n"
        with open(path, "w", encoding="utf-8") as f:
            f.write(doc.replace("## Журнал", block + "## Журнал", 1))

    def ask_bg(self, root, question, wait="20"):
        env = self.env(root)
        env["DEVKIT_GOAL_ASK_WAIT"] = wait
        proj = os.path.join(root, "proj")
        p = subprocess.Popen([RUN, "-C", proj, "DK-100", "--ask", question], cwd=proj, env=env,
                             stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        self.addCleanup(self.reap, p)
        return p

    def reap(self, p):
        if p.poll() is None:
            p.kill()
            p.wait(timeout=10)

    def wait_hold(self, root, p):
        """Дождаться признака ожидания: до него ключ ещё зовёт уведомитель, и
        ответ, положенный раньше, ловился бы гонкой самого теста."""
        path = self.devfile(root, self.ASKFILE)
        for _ in range(200):
            if os.path.isfile(path):
                return path
            if p.poll() is not None:
                self.fail("ключ вышел, не начав ждать: %s" % p.communicate()[0])
            time.sleep(0.05)
        self.fail("признак ожидания %s не лёг" % path)

    def marks(self, root):
        return chat_in.read_marks(self.devfile(root, self.MAIL))

    def test_ask_waits_for_the_answer_and_prints_it(self):
        # Ответ приходит посреди ожидания, и ключ печатает строку витку целиком,
        # вместе со временем, которое дашборд поставил в неё сам.
        root = self.stand("done запись")
        line = "2026-08-15 12:30, из дашборда: бери sonnet, opus держи на ревью"
        p = self.ask_bg(root, "какую модель брать на DK-101?")
        self.wait_hold(root, p)
        self.answer(root, line)
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        self.assertIn(line, out)
        self.assertIn("вопрос человеку: какую модель брать на DK-101?", self.shell_log(root))
        self.assertIn("ответ человека получен", self.shell_log(root))
        self.assertFalse(os.path.isfile(self.devfile(root, self.ASKFILE)),
                         "признак ожидания пережил ответ")
        self.assertFalse(os.path.isfile(os.path.join(root, "calls")), "вопрос поднял виток")
        self.assertFalse(os.path.isdir(os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")),
                         "вопрос поднял замок цикла")

    def test_ask_calls_the_notifier_loudly(self):
        # Человека к делу зовёт уведомитель, и повод у зова тот же, каким
        # зовёт маркер wait-human: по нему лента дашборда отбирает зов человека.
        root = self.stand("done запись")
        p = self.ask_bg(root, "чинить DK-102 или отложить?", wait="1")
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        log = self.notify_log(root)
        self.assertIn("уровень громкий", log)
        self.assertIn("повод wait_human", log)
        self.assertIn("текст «цель DK-100: вопрос человеку»", log)
        self.assertIn("чинить DK-102 или отложить?", log)

    def test_ask_gives_up_by_the_deadline(self):
        # Никто не ответил: это не авария, а обычный возврат нолём. Отметок
        # ключ при этом не заводит вовсе, отмечать нечего.
        root = self.stand("done запись")
        p = self.ask_bg(root, "ответа не будет", wait="1")
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        self.assertIn("ответа нет", out)
        self.assertFalse(os.path.isfile(self.devfile(root, self.ASKFILE)),
                         "признак ожидания пережил истёкший срок")
        self.assertFalse(os.path.isfile(self.devfile(root, self.MAIL)),
                         "ключ завёл отметку, никого не дождавшись")

    def test_hold_is_read_by_the_chat_hook_as_a_live_wait(self):
        # Признак ожидания читает подхват, и читает он его своим кодом: пока
        # срок не вышел, вход принадлежит ключу и доставлять он не должен
        # ничего. Формат тут сверяется литералами, а не на глаз.
        root = self.stand("done запись")
        proj = os.path.join(root, "proj")
        p = self.ask_bg(root, "вход занят вопросом", wait="20")
        path = self.wait_hold(root, p)
        until = chat_in.ask_until(proj, "DK-100")
        self.assertIsNotNone(until, "подхват не разобрал срок в признаке ожидания")
        self.assertGreater(until, time.time(), "срок в признаке ожидания уже прошёл")
        self.assertLessEqual(until, time.time() + 25, "срок в признаке ожидания взят с потолка")
        with open(path, encoding="utf-8") as f:
            self.assertRegex(f.read(), r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\n$")
        self.answer(root, "2026-08-15 12:31, из дашборда: ответ")
        p.communicate(timeout=60)
        self.assertIsNone(chat_in.ask_until(proj, "DK-100"),
                          "снятый признак ожидания подхват всё ещё видит")

    def test_eaten_line_is_marked_in_the_chat_hook_format(self):
        # Отметку съеденной строки ставит тот, кто отдал её витку, и формат у
        # неё общий с подхватом: две строки, «время сессия» и строка целиком.
        root = self.stand("done запись")
        line = "2026-08-15 12:32, из дашборда: сливай как есть"
        p = self.ask_bg(root, "сливать?")
        self.wait_hold(root, p)
        self.answer(root, line)
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        marks = self.marks(root)
        self.assertEqual([m.line for m in marks], [line])
        self.assertRegex(marks[0].stamp, r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d$")
        with open(self.devfile(root, self.MAIL), encoding="utf-8") as f:
            self.assertEqual(f.read(), "%s -\n%s\n" % (marks[0].stamp, line),
                             "файл отметок разъехался с разбором подхвата")

    def test_mark_names_the_turn_from_the_lock(self):
        # Имя витка ключ берёт из замка оболочки: под циклом отметка названа
        # тем витком, который вопрос задал, а в живом чате замка нет и поле
        # остаётся прочерком (случай выше).
        root = self.stand("done запись")
        lock = os.path.join(root, "proj", ".devkit", "goal-DK-100.lock")
        os.makedirs(lock)
        with open(os.path.join(lock, "session"), "w", encoding="utf-8") as f:
            f.write("11111111-2222-3333-4444-555555555555\n")
        line = "2026-08-15 12:33, из дашборда: годится"
        p = self.ask_bg(root, "годится?")
        self.wait_hold(root, p)
        self.answer(root, line)
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        self.assertEqual([m.session for m in self.marks(root)],
                         ["11111111-2222-3333-4444-555555555555"])

    def test_already_marked_line_is_not_taken_for_an_answer(self):
        # Лежащая строка с отметкой доставки это не ответ: её уже отдал витку
        # подхват, и вторым экземпляром она приезжать не должна.
        root = self.stand("done запись")
        line = "2026-08-15 11:00, из дашборда: реплика прошлого часа"
        self.answer(root, line)
        chat_in.write_marks(self.devfile(root, self.MAIL),
                          [chat_in.Mark("2026-08-15T11:00:05", "-", line)])
        p = self.ask_bg(root, "ждём нового ответа", wait="1")
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        self.assertIn("ответа нет", out)
        self.assertNotIn(line, out, "ключ принял за ответ уже доставленную строку")

    def test_mark_is_written_before_the_line_is_printed(self):
        # Отметка встаёт до печати: не записав её, ключ строку не отдаёт вовсе.
        # Иначе реплика, чья отметка не легла, ездила бы витку по кругу.
        root = self.stand("done запись")
        os.mkdir(self.devfile(root, self.MAIL))  # каталог на месте файла: запись отметки не пройдёт
        line = "2026-08-15 12:34, из дашборда: ответ, который не отметить"
        self.answer(root, line)
        p = self.ask_bg(root, "отметка не запишется", wait="2")
        out = p.communicate(timeout=60)[0]
        self.assertEqual(p.returncode, 0, out)
        self.assertNotIn(line, out, "строка напечатана витку без отметки доставки")
        self.assertIn("ответа нет", out)

    def test_answered_line_is_not_served_twice(self):
        # Отметка живёт на диске, поэтому следующий вопрос ту же строку за
        # ответ не берёт: она уже съедена.
        root = self.stand("done запись")
        line = "2026-08-15 12:35, из дашборда: первый ответ"
        self.answer(root, line)
        first = self.ask_bg(root, "первый вопрос", wait="20").communicate(timeout=60)[0]
        self.assertIn(line, first)
        second = self.ask_bg(root, "второй вопрос", wait="1").communicate(timeout=60)[0]
        self.assertNotIn(line, second, "съеденная строка приехала витку вторым экземпляром")
        self.assertIn("ответа нет", second)

    def test_ask_refusals(self):
        # Вопрос без текста, вместе с циклом, вместе со строкой хода и по цели
        # без файла это ошибка вызова: ждать ответа некуда и не от кого.
        root = self.stand("done запись")
        p = self.goal_run(root, "DK-100", "--ask")
        self.assertEqual(p.returncode, 2, "оболочка приняла --ask без текста")
        p = self.goal_run(root, "DK-100", "--ask", "   ")
        self.assertEqual(p.returncode, 2, "оболочка приняла пустой вопрос")
        p = self.goal_run(root, "DK-100", "--foreground", "--ask", "и то и другое")
        self.assertEqual(p.returncode, 2, "оболочка приняла --ask вместе с --foreground")
        p = self.goal_run(root, "DK-100", "--say", "ход", "--ask", "вопрос")
        self.assertEqual(p.returncode, 2, "оболочка приняла --ask вместе с --say")
        p = self.goal_run(root, "DK-101", "--ask", "вопрос не той цели")
        self.assertEqual(p.returncode, 2, "оболочка задала вопрос по цели, файла которой нет")
        self.assertEqual(self.shell_log(root), "", "отказавший вызов всё же тронул журнал")
        self.assertFalse(os.path.isfile(self.devfile(root, self.ASKFILE)),
                         "отказавший вызов оставил признак ожидания")


class SkillInboxTests(unittest.TestCase):
    """Формулировки SKILL.md про «Входящие» файла цели (DK-220). Дашборд кладёт
    сообщения человека в этот раздел и рассчитывает, что виток читает их на
    шаге состояния и убирает записью витка: уехавшая формулировка оборвала бы
    канал переписки молча, поэтому её держит тест. С DK-343 у строки есть и
    вторая дорога, посреди витка от подхвата реплики, и шаг состояния обязан на неё
    показывать: правило реакции лежит своим разделом, а его слова держит
    check-skills.py."""

    @classmethod
    def setUpClass(cls):
        with open(os.path.join(HERE, "SKILL.md"), encoding="utf-8") as f:
            cls.skill = f.read()

    def test_inbox_is_read_on_the_state_step(self):
        # Чтение «Входящих» стоит в шаге состояния, до всякой работы: виток
        # читает файл цели целиком, и лежавшую строку он не пропустит.
        state = self.skill[self.skill.index("1. Состояние"):self.skill.index("2. Гейт бюджета")]
        self.assertIn("«Входящие»", state)
        self.assertIn("из дашборда", state, "формат строки сообщения не назван")

    def test_state_step_points_at_the_live_reply(self):
        # Реплика не ждёт шага состояния, и виток обязан знать об этом там же,
        # где читает «Входящие»: иначе он примет доставленную посреди работы
        # строку за чужой текст в контексте.
        state = self.skill[self.skill.index("1. Состояние"):self.skill.index("2. Гейт бюджета")]
        self.assertIn("посреди витка", state)
        self.assertIn("Живая реплика", state, "раздел с правилом реакции не назван")

    def test_inbox_line_is_removed_by_the_turn_record(self):
        # Убирается строка записью витка, а не при чтении: оборванный виток
        # теряет только себя, и сообщение дожидается следующего.
        state = self.skill[self.skill.index("1. Состояние"):self.skill.index("2. Гейт бюджета")]
        self.assertIn("запись витка", state)
        record = self.skill[self.skill.index("5. Запись витка"):self.skill.index("6. Итог")]
        self.assertIn("убирает из «Входящих»", record)
        self.assertIn("ждёт витка", record, "надпись дашборда не привязана к лежащей строке")


class SkillRecordTests(unittest.TestCase):
    """Формат строки витка (DK-644): времена ставит команда. В скилле от этого
    норма и вызов, а разбор формата живёт в README agentctl, куда скилл и
    показывает (DK-211, скиллы не разбухают). Держатся оба конца здесь: без них
    журнал вернулся бы к строке без часов, и на вопрос «куда ушёл день» такой
    журнал не отвечает."""

    @classmethod
    def setUpClass(cls):
        with open(os.path.join(HERE, "SKILL.md"), encoding="utf-8") as f:
            cls.skill = f.read()
        readme = os.path.normpath(os.path.join(HERE, "..", "..", "..", "tools",
                                               "agentctl", "README.md"))
        with open(readme, encoding="utf-8") as f:
            cls.readme = f.read()

    def record(self):
        return self.skill[self.skill.index("5. Запись витка"):self.skill.index("7. Выход маркером")]

    def test_turn_line_is_written_by_the_command(self):
        record = self.record()
        self.assertIn("agentctl lap --goal", record, "строку витка снова пишут руками")
        self.assertIn("--marker", record)
        self.assertIn("--note", record)
        self.assertIn("README agentctl", record, "разбор формата потерял адрес")

    def test_turn_line_example_carries_hours(self):
        # Пример это то, по чему формат читают на самом деле, и строка без часов
        # в нём стоит дороже любого абзаца. Лежит пример в README, скилл на него
        # ссылается.
        head = self.readme.index("## Время витка и сводка итога")
        section = self.readme[head:self.readme.index("\n## ", head + 1)]
        for line in section.split("\n"):
            line = line.strip()
            if line.startswith("- 20") and line.endswith("continue"):
                self.assertRegex(line, r"^- \d{4}-\d{2}-\d{2} \d{2}:\d{2}-\d{2}:\d{2}, .+; continue$")
                break
        else:
            self.fail("в README нет примера строки витка")

    def test_summary_of_the_stop_comes_from_the_command(self):
        # Сводка итога считается по этапам задач цели, и собирает её команда:
        # руками такую разбивку виток не соберёт, живая запись этапов к моменту
        # итога уже уехала в файлы задач.
        summary = self.skill[self.skill.index("6. Итог"):self.skill.index("7. Выход маркером")]
        self.assertIn("agentctl tally --goal", summary)
        self.assertIn("по видам этапов", summary)


class SkillMarkerTests(unittest.TestCase):
    """Поводы wait-human после DK-403 (решение 6 LLD DK-400): стоп держат
    только поводы уровня цели, задачный повод паркует задачу и закрывается
    ответом continue. Формулировки держит тест по образцу SkillInboxTests:
    уехавшая строка вернула бы всегдашний стоп цикла молча, и человек, чей
    ответ просто лежит в разговоре припаркованной задачи, выглядел бы виноватым
    в простое всего конвейера."""

    @classmethod
    def setUpClass(cls):
        with open(os.path.join(HERE, "SKILL.md"), encoding="utf-8") as f:
            cls.skill = f.read()

    def markers(self):
        return self.skill[self.skill.index("## Маркеры выхода"):self.skill.index("## Живая реплика")]

    def test_wait_human_names_goal_level_reasons_only(self):
        # У маркера три повода уровня цели: сверка бюджета, вопрос постановки,
        # недоступное окружение. Задачные поводы старого списка (задача ждёт
        # проверки, окружение задачи) сюда не возвращаются: их виток закрывает
        # парковкой и continue.
        bullet = self.markers()
        bullet = bullet[bullet.index("- `wait-human`"):]
        bullet = bullet[:bullet.index("- `stuck`")]
        self.assertIn("сама цель", bullet, "граница повода не названа")
        self.assertIn("`goal-cut`", bullet, "сверка бюджета не названа")
        for gone in ("задача цели ждёт", "харнес отказал"):
            self.assertNotIn(gone, bullet, "задачный повод вернулся в wait-human: %s" % gone)

    def test_task_reason_parks_the_task_and_answers_continue(self):
        # Задачный повод описан парковкой (DK-402) с ответом continue, а
        # задачный повод, поставивший wait-human, назван ошибкой витка: без
        # запрета виток прикрывал бы стопом любую трудность одной задачи.
        section = self.markers()
        self.assertIn("move <ID> blocked", section, "парковка задачи не названа командой")
        self.assertIn("вопрос:", section, "машинный префикс причины парковки не назван")
        self.assertIn("«окружение:", section, "парковка окружения задачи не названа")
        self.assertIn("ошибка витка", section, "запрет задачного повода у маркера пропал")


# Подписка витков (замечание пользователя: «выполнить задачу с выбором подписки
# можно только для задач, а для цели нет, а должно быть»). Раскладку подписок
# знает agentctl, и клиента чужой подписки поднимает он же своей обвязкой.
AGENTCTL_STUB = r'''#!/usr/bin/env python3
import json
import os
import sys

root = os.environ["STAND_ROOT"]
args = sys.argv[1:]
if args[:1] == ["harness"]:
    print(json.dumps({"default": "перваяtest", "harnesses": [
        {"name": "перваяtest", "enabled": True, "default": True, "bin": "claude",
         "models": [{"tier": "base", "model": "модель-base"},
                    {"tier": "pro", "model": "модель-pro"}]},
        {"name": "втораяtest", "enabled": True, "default": False, "bin": "клиент-2",
         "models": [{"tier": "pro", "model": "вторая-pro"}]},
        {"name": "третьяtest", "enabled": False, "default": False, "bin": "клиент-3"},
    ]}))
    sys.exit(0)
if args[:1] == ["exec"]:
    with open(os.path.join(root, "execs"), "a", encoding="utf-8") as f:
        f.write(" ".join(args) + "\n")
    cut = args.index("--")
    os.execvp(args[cut + 1], args[cut + 1:])
sys.exit("стаб agentctl: неизвестная команда %s" % args)
'''


def load_goal_run():
    """Оболочка грузится модулем: часть вопросов (разбор флага, команда витка)
    не стоит целого прогона цикла, а дефис в имени файла не годится для
    import."""
    spec = importlib.util.spec_from_file_location("devkit_goal_run", RUN)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class HarnessTests(Stand, unittest.TestCase):
    def test_parse_args_reads_harness(self):
        mod = load_goal_run()
        got = mod.parse_args(["DK-100", "-C", "/tmp", "--harness", "втораяtest"])
        self.assertEqual(got[5], "втораяtest", "имя подписки не разобралось: %r" % (got,))
        # Без флага всё как было: подписка по умолчанию, пустое имя.
        self.assertEqual(mod.parse_args(["DK-100"])[5], "")

    def test_parse_args_reads_tier(self):
        """Ярус витка приезжает флагом от дашборда: без него виток шёл дефолтом
        клиента, то есть верхним ярусом, которого цели никто не назначал."""
        mod = load_goal_run()
        got = mod.parse_args(["DK-100", "--tier", "base"])
        self.assertEqual(got[6], "base", "ярус не разобрался: %r" % (got,))
        self.assertEqual(mod.parse_args(["DK-100"])[6], "", "без флага ярус обязан быть пустым")

    def test_turn_cmd_names_tier_model(self):
        """Ярус разворачивается в модель раскладкой машины и называется витку
        флагом. Второй подписке модель не называется вовсе: её называет
        собственный профиль подписки."""
        mod = load_goal_run()
        loop = mod.Loop("DK-100", "/tmp", "", "base")
        loop.model = "модель-base"
        cmd = loop.turn_cmd("сид")
        self.assertEqual(cmd[:3], ["claude", "--model", "модель-base"],
                          "виток поехал не ярусом заказа: %r" % (cmd,))
        loop = mod.Loop("DK-100", "/tmp", "втораяtest", "pro")
        loop.client = "клиент-2"
        loop.model = ""
        self.assertNotIn("--model", loop.turn_cmd("сид"),
                          "второй подписке приклеили явную модель")

    def test_unknown_tier_refused_before_turns(self):
        """Имён моделей у оболочки своих нет: лестницу держит раскладка машины,
        и незнакомый ярус это отказ словами до первого витка, а не молчаливый
        дефолт посреди цикла."""
        root = self.stand("continue", "done")
        write_exec(os.path.join(root, "bin", "agentctl"), AGENTCTL_STUB)
        p = self.goal_run(root, "DK-100", "--tier", "космос", "--foreground")
        self.assertNotEqual(p.returncode, 0, "незнакомый ярус прошёл: %s" % p.stdout)
        self.assertIn("яруса космос", p.stdout, "отказ не назвал ярус: %s" % p.stdout)
        self.assertEqual(self.turns_done(root), 0, "витки пошли на незнакомом ярусе")

    def test_cycle_turn_carries_tier_model(self):
        """Ярус доезжает до самой команды витка, а не только до разбора флагов."""
        root = self.stand("continue", "done")
        write_exec(os.path.join(root, "bin", "agentctl"), AGENTCTL_STUB)
        p = self.goal_run(root, "DK-100", "--tier", "base", "--foreground")
        self.assertEqual(p.returncode, 0, "цикл с ярусом не прошёл: %s" % p.stdout)
        with open(os.path.join(root, "calls"), encoding="utf-8") as f:
            calls = f.read()
        self.assertIn("--model модель-base", calls,
                       "виток поехал не ярусом заказа: %s" % calls)

    def test_turn_cmd_wraps_foreign_harness(self):
        mod = load_goal_run()
        loop = mod.Loop("DK-100", "/tmp")
        self.assertEqual(loop.turn_cmd("сид")[0], "claude",
                          "виток без выбора поехал не своим клиентом")
        loop = mod.Loop("DK-100", "/tmp", "втораяtest")
        loop.client = "клиент-2"
        cmd = loop.turn_cmd("сид")
        self.assertEqual(cmd[:5], [mod.AGENTCTL, "exec", "--harness", "втораяtest", "--"],
                          "виток чужой подписки поехал мимо её обвязки: %r" % (cmd,))
        self.assertEqual(cmd[5], "клиент-2", "поднят не клиент выбранной подписки: %r" % (cmd,))
        self.assertIn("--permission-mode", cmd,
                       "чужой подписке не назван режим разрешений: виток встанет на первом вопросе")
        self.assertIn("--session-id", cmd, "виток потерял своё имя: живая реплика до него не доедет")
        # Подписка по умолчанию обвязки не требует: её клиент зовётся как звался.
        loop = mod.Loop("DK-100", "/tmp", "перваяtest")
        loop.client = ""
        self.assertEqual(loop.turn_cmd("сид")[0], "claude")

    def test_cycle_runs_turn_through_chosen_harness(self):
        root = self.stand("continue", "done")
        write_exec(os.path.join(root, "bin", "agentctl"), AGENTCTL_STUB)
        # Клиент второй подписки это тот же стаб: витки он играет так же, а
        # предмет проверки в том, кем он поднят.
        write_exec(os.path.join(root, "bin", "клиент-2"), CLAUDE_STUB)
        p = self.goal_run(root, "DK-100", "--harness", "втораяtest", "--foreground")
        self.assertEqual(p.returncode, 0, "цикл на второй подписке не прошёл: %s" % p.stdout)
        with open(os.path.join(root, "execs"), encoding="utf-8") as f:
            execs = f.read()
        self.assertIn("exec --harness втораяtest", execs,
                       "витки пошли мимо выбранной подписки: %s" % execs)
        self.assertIn("клиент-2", execs, "поднят не клиент выбранной подписки: %s" % execs)
        self.assertEqual(self.turns_done(root), 2, "витков прошло не два: %s" % p.stdout)

    def test_unknown_harness_refused_before_turns(self):
        root = self.stand("done")
        write_exec(os.path.join(root, "bin", "agentctl"), AGENTCTL_STUB)
        p = self.goal_run(root, "DK-100", "--harness", "какая-то", "--foreground")
        self.assertNotEqual(p.returncode, 0, "неизвестная подписка прошла: %s" % p.stdout)
        self.assertIn("в раскладке машины нет", p.stdout)
        self.assertEqual(self.turns_done(root), 0, "виток всё равно подняли: %s" % p.stdout)

    def test_disabled_harness_refused(self):
        root = self.stand("done")
        write_exec(os.path.join(root, "bin", "agentctl"), AGENTCTL_STUB)
        p = self.goal_run(root, "DK-100", "--harness", "третьяtest", "--foreground")
        self.assertNotEqual(p.returncode, 0, "выключенная подписка прошла: %s" % p.stdout)
        self.assertIn("выключена", p.stdout)

    def test_tmux_launch_carries_harness(self):
        mod = load_goal_run()
        seen = {}

        class FakeRun:
            def __init__(self, code):
                self.returncode = code

        def fake_run(cmd, **kw):
            seen.setdefault("calls", []).append(cmd)
            # Живой сессии у цели нет: иначе оболочка отказывает до подъёма, и
            # проверять было бы нечего.
            return FakeRun(1 if "has-session" in cmd else 0)

        loop = mod.Loop("DK-100", "/tmp", "втораяtest")
        loop.lock_busy = lambda: False
        old_run, old_which = mod.subprocess.run, mod.shutil.which
        mod.subprocess.run = fake_run
        mod.shutil.which = lambda name: "/bin/" + name
        try:
            with self.assertRaises(SystemExit):
                loop.launch_tmux()
        finally:
            mod.subprocess.run, mod.shutil.which = old_run, old_which
        new = [c for c in seen.get("calls", []) if "new-session" in c]
        self.assertTrue(new, "tmux-сессию не поднимали: %r" % seen)
        self.assertIn("--harness", " ".join(new[-1]),
                       "своя tmux-сессия цикла потеряла подписку: %r" % new[-1])


if __name__ == "__main__":
    unittest.main(verbosity=0)
