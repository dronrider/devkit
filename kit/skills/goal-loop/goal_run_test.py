#!/usr/bin/env python3
"""Самопроверка оболочки цели goal-run.py. Витки играет стаб вместо claude, как
парсер панели /usage гоняется по образцам: настоящих сессий тут не
поднимается ни одной. Каждый стенд это свой синтетический проект с доской,
файлом цели и временным HOME, чтобы уведомитель писал свой журнал в него.
Стаб клиента и стаб tmux сами написаны на python и живут только во временной
директории теста: файлами репозитория они не становятся, как и любая другая
фикстура, изображающая чужую программу.
"""
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(HERE, "goal-run.py")

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
# «Журнал» цели, «снимок» строку снимка квоты, всё остальное молчит. Особые
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
    entry = "- виток %d, задача цели закрыта; %s" % (turn, marker)
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


class GoalRunTests(unittest.TestCase):
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

    def goal_run(self, root, *args, pause="0"):
        # -C всегда назван явно, как в реальном вызове из SKILL.md: без него
        # оболочка берёт корень из pwd процесса, а голый os.getcwd() в python
        # разворачивает симлинк /var -> /private/var на macOS там, где
        # логический pwd шелла этого не делает, и путь в сообщении разошёлся
        # бы с тем, что ушло в -C у стенда.
        env = dict(os.environ)
        env["PATH"] = os.path.join(root, "bin") + os.pathsep + env.get("PATH", "")
        env["HOME"] = os.path.join(root, "home")
        env["STAND_ROOT"] = root
        env["GOAL_RUN"] = RUN
        # Пауза между попытками поднять вставший виток: живому циклу она нужна
        # (обрыв на стороне API немедленный повтор встретит тем же обрывом),
        # стенду она только добавляет секунд, поэтому по умолчанию ноль.
        env["DEVKIT_GOAL_RETRY_PAUSE"] = pause
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
        self.assertIn("-p продолжай цель DK-100 по скиллу goal-loop", first_call)
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


class SkillInboxTests(unittest.TestCase):
    """Формулировки SKILL.md про «Входящие» файла цели (DK-220). Дашборд кладёт
    сообщения человека в этот раздел и рассчитывает, что виток читает их на
    шаге состояния и убирает записью витка: уехавшая формулировка оборвала бы
    канал переписки молча, поэтому её держит тест."""

    @classmethod
    def setUpClass(cls):
        with open(os.path.join(HERE, "SKILL.md"), encoding="utf-8") as f:
            cls.skill = f.read()

    def test_inbox_is_read_on_the_state_step(self):
        # Чтение «Входящих» стоит в шаге состояния, до всякой работы: виток
        # читает файл цели целиком, и отдельного канала доставки нет.
        state = self.skill[self.skill.index("1. Состояние"):self.skill.index("2. Гейт бюджета")]
        self.assertIn("«Входящие»", state)
        self.assertIn("из дашборда", state, "формат строки сообщения не назван")
        self.assertIn("контекст витка", state)

    def test_inbox_line_is_removed_by_the_turn_record(self):
        # Убирается строка записью витка, а не при чтении: оборванный виток
        # теряет только себя, и сообщение дожидается следующего.
        state = self.skill[self.skill.index("1. Состояние"):self.skill.index("2. Гейт бюджета")]
        self.assertIn("записью витка", state)
        record = self.skill[self.skill.index("5. Запись витка"):self.skill.index("6. Выход маркером")]
        self.assertIn("убирает из «Входящих»", record)
        self.assertIn("ждёт витка", record, "надпись дашборда не привязана к лежащей строке")


if __name__ == "__main__":
    unittest.main(verbosity=0)
