#!/usr/bin/env python3
"""Оболочка режима цели: поднимает витки скилла goal-loop одноразовыми
headless-сессиями, пока виток отвечает маркером continue. Живучесть цикла от
живучести сессии тут не зависит: состояние цели целиком лежит на диске (файл
цели, доска, git, деревья задач), а оболочка только зовёт следующий виток и
решает, когда перестать звать.

  goal-run.py <ID> [-C <корень проекта>] [--foreground]

Без --foreground цикл уходит в свою tmux-сессию goal-<ID>, как съёмщик панели
/usage поднимает клиента в одноразовой сессии, и оболочка возвращается сразу.
С --foreground витки идут в этом же процессе: так цикл смотрят вживую и так
его гоняют тесты.

Коды возврата: 0 штатный стоп по маркеру витка, 1 цикл остановила сама
оболочка (виток без маркера, авария, воронка), 2 ошибка вызова или
окружения, 3 цикл этой цели уже идёт.
"""
import os
import re
import shlex
import shutil
import signal
import subprocess
import sys
import time

AUTONOMOUS_RE = re.compile(r"^\s*autonomous\s*=\s*true")

HERE = os.path.dirname(os.path.abspath(__file__))
NOTIFIER = os.path.normpath(os.path.join(HERE, "..", "..", "..", "hooks", "notify.py"))
PERMS = os.path.normpath(os.path.join(HERE, "..", "..", "..", "tools", "devkitctl", "perms.py"))
FUNNEL_LIMIT = 3  # витков подряд без записи в «Журнале», после которых стоп
MARKERS = ("continue", "done", "over", "wait-human", "stuck")

USAGE = """\
goal-run.py <ID> [-C <корень проекта>] [--foreground]

Витки цели <ID> одноразовыми сессиями, пока виток отвечает marker: continue.
Любой другой маркер завершает цикл и зовёт уведомитель громким поводом.

  -C <корень>    проект с доской, по умолчанию текущая директория
  --foreground   держать цикл в этом процессе, а не в tmux-сессии goal-<ID>

Ход цикла пишется в <корень>/.devkit/goal-<ID>.log: время, номер витка,
маркер, код выхода.
"""


def die(text, code=2):
    sys.stderr.write(text + "\n")
    sys.exit(code)


def parse_args(argv):
    goal_id = None
    proj = os.getcwd()
    fg = False
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "-C":
            if i + 1 >= len(argv):
                die("у -C нет значения")
            proj = argv[i + 1]
            i += 2
        elif arg == "--foreground":
            fg = True
            i += 1
        elif arg in ("-h", "--help"):
            sys.stdout.write(USAGE)
            sys.exit(0)
        elif arg.startswith("-"):
            sys.stderr.write(USAGE)
            die("неизвестный флаг %s" % arg)
        else:
            if goal_id is not None:
                die("лишний аргумент %s: цель у оболочки одна" % arg)
            goal_id = arg
            i += 1
    if goal_id is None:
        sys.stderr.write(USAGE)
        die("не назван ID цели")
    return goal_id, proj, fg


def resolve_proj(proj):
    abspath = proj if os.path.isabs(proj) else os.path.join(os.getcwd(), proj)
    abspath = os.path.normpath(abspath)
    if not os.path.isdir(abspath):
        die("корня проекта %s нет" % proj)
    return abspath


class Loop:
    """Состояние одного цикла: пути цели, лог, замок и подсчёт воронки. Класс
    только группирует то, что в sh было переменными верхнего уровня, своей
    логики сверх методов ниже у него нет."""

    def __init__(self, goal_id, proj):
        self.id = goal_id
        self.proj = proj
        self.devdir = os.path.join(proj, ".devkit")
        self.deploy = os.path.join(self.devdir, "deploy.local")
        self.goal = os.path.join(proj, "docs", "tasks", "%s.md" % goal_id)
        self.log = os.path.join(self.devdir, "goal-%s.log" % goal_id)
        self.lock = os.path.join(self.devdir, "goal-%s.lock" % goal_id)
        self.pidfile = os.path.join(self.lock, "pid")
        self.sess = "goal-%s" % goal_id
        self.prompt = "продолжай цель %s по скиллу goal-loop" % goal_id
        self.turn = 0

    # -- предполётные проверки --------------------------------------------

    def preflight(self):
        if not os.path.isfile(os.path.join(self.proj, "docs", "TASKS.md")):
            die("доски %s/docs/TASKS.md нет, режим цели живёт только в проекте с доской" % self.proj)
        if not os.path.isfile(self.goal):
            die("файла цели %s нет: цель ставит скилл goal-start, оболочка её не заводит" % self.goal)
        # Флаг автономии оболочка читает, но не поднимает: при autonomous = false
        # слияние не пушит и не катит, и цикл проверял бы агентские сценарии
        # против старого прода, считая задачи выкаченными.
        if not self.autonomous_flag_set():
            die("в %s нет autonomous = true, цикл без выката проверял бы сценарии против старого прода"
                % self.deploy)
        if shutil.which("claude") is None:
            die("claude в PATH нет, поднимать витки нечем")
        # Предполётная проверка прав: одобрять запросы харнеса в headless-сессии
        # некому, и виток без прав отвечает continue, не сделав ничего. Без
        # проверки такой цикл жёг бы бюджет до самой воронки, а стоп был бы
        # неотличим от молчания.
        p = subprocess.run(["python3", PERMS], stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, text=True)
        perms_out = p.stdout.rstrip("\n")
        if p.returncode == 0:
            return
        if p.returncode == 1:
            die(perms_out)
        die("прав машинного контура не проверить: %s" % perms_out)

    def autonomous_flag_set(self):
        try:
            with open(self.deploy, encoding="utf-8") as f:
                lines = f.read().splitlines()
        except OSError:
            return False
        return any(AUTONOMOUS_RE.match(line) for line in lines)

    # -- замок --------------------------------------------------------------

    def lock_busy(self):
        try:
            with open(self.pidfile, encoding="utf-8") as f:
                pid = f.read().strip()
        except OSError:
            return False
        if not pid:
            return False
        try:
            os.kill(int(pid), 0)
        except (OSError, ValueError):
            return False
        return True

    def lock_owner(self):
        try:
            with open(self.pidfile, encoding="utf-8") as f:
                return f.read().strip()
        except OSError:
            return ""

    def take_lock(self):
        try:
            os.makedirs(self.devdir, exist_ok=True)
        except OSError:
            pass
        try:
            os.mkdir(self.lock)
        except OSError:
            if self.lock_busy():
                return False
            # Замок без живого владельца остался от убитой оболочки:
            # перезагрузка, kill -9, закрытое окно tmux. Снимаем и берём себе,
            # иначе цикл не поднять до ручной уборки, а обрыв оболочки чинится
            # повторным запуском.
            try:
                os.remove(self.pidfile)
            except OSError:
                pass
            try:
                os.rmdir(self.lock)
            except OSError:
                pass
            try:
                os.mkdir(self.lock)
            except OSError:
                return False
        with open(self.pidfile, "w", encoding="utf-8") as f:
            f.write("%d\n" % os.getpid())
        return True

    def release(self):
        try:
            os.remove(self.pidfile)
        except OSError:
            pass
        try:
            os.rmdir(self.lock)
        except OSError:
            pass

    # -- журнал оболочки и уведомитель --------------------------------------

    def say(self, msg):  # строка в журнал оболочки и в вывод: панель tmux
        # показывает то же
        line = "%s %s" % (time.strftime("%Y-%m-%dT%H:%M:%S"), msg)
        try:
            os.makedirs(self.devdir, exist_ok=True)
        except OSError:
            pass
        try:
            with open(self.log, "a", encoding="utf-8") as f:
                f.write(line + "\n")
        except OSError:
            pass
        print(line)

    def shout(self, title, text):  # заголовок, текст: стоп цикла молчать не должен
        if not os.path.isfile(NOTIFIER):
            self.say("уведомителя %s нет, стоп остался без голоса" % NOTIFIER)
            return
        try:
            p = subprocess.run(["python3", NOTIFIER, title, text], cwd=self.proj,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            out = p.stdout.rstrip("\n")
        except OSError as e:
            out = str(e)
        self.say("уведомление: %s; %s" % (title, out or "отправлено"))

    def stop(self, reason, text, code):  # повод, текст, код
        self.say("цикл цели %s остановлен: %s, витков %d" % (self.id, reason, self.turn))
        self.shout("цель %s: %s" % (self.id, reason), text)
        sys.exit(code)

    # -- «Журнал» файла цели -------------------------------------------------

    def journal_entries(self):
        # Содержательных записей в «Журнале» цели столько. Строки снимков
        # квоты их дописывает сам гейт бюджета, и продвижением витка они не
        # считаются: снимок появляется и у витка, который не сделал ничего.
        try:
            with open(self.goal, encoding="utf-8") as f:
                lines = f.read().splitlines()
        except OSError:
            return 0
        n = 0
        inj = False
        for line in lines:
            if line.startswith("## "):
                if inj:
                    break
                inj = line.startswith("## Журнал")
                continue
            if not inj:
                continue
            s = line.lstrip()
            if s.startswith("- "):
                s = s[2:]
            if s == "" or s.startswith("снимок "):
                continue
            n += 1
        return n

    # -- tmux ------------------------------------------------------------

    def launch_tmux(self):
        if shutil.which("tmux") is None:
            die("tmux в PATH нет, поднять цикл нечем; свой цикл в этом же процессе даёт --foreground")
        if self.lock_busy():
            die("цикл цели %s уже идёт, замок %s" % (self.id, self.lock), 3)
        has = subprocess.run(["tmux", "has-session", "-t", "=%s" % self.sess],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if has.returncode == 0:
            die("tmux-сессия %s уже поднята, смотреть её: tmux attach -t %s" % (self.sess, self.sess), 3)
        cmd = " ".join(shlex.quote(a) for a in
                        (os.path.join(HERE, "goal-run.py"), self.id, "-C", self.proj, "--foreground"))
        new = subprocess.run(["tmux", "new-session", "-d", "-s", self.sess, cmd])
        if new.returncode != 0:
            die("tmux не поднял сессию %s" % self.sess)
        print("цикл цели %s поднят в tmux-сессии %s, журнал %s" % (self.id, self.sess, self.log))
        sys.exit(0)

    # -- виток -------------------------------------------------------------

    def run_turn(self):
        before = self.journal_entries()
        p = subprocess.run(["claude", "-p", self.prompt], cwd=self.proj,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        after = self.journal_entries()
        code = p.returncode
        # Маркер это последняя непустая строка ответа, и сравнивается она
        # целиком: ограда примера или «маркер: continue» прозой за маркер не
        # считаются, так обещает раздел «Маркеры выхода» скилла.
        last = ""
        for l in (p.stdout or "").split("\n"):
            l = l.rstrip()
            if l != "":
                last = l
        marker = ""
        for m in MARKERS:
            if last == "marker: %s" % m:
                marker = m
                break
        self.say("виток %d маркер %s код %d записей в журнале %d -> %d"
                  % (self.turn, marker or "нет", code, before, after))
        if code != 0:
            self.stop("авария витка", "сессия витка вышла с кодом %d, последняя строка: %s"
                      % (code, last or "пусто"), 1)
        if not marker:
            self.stop("виток без маркера",
                      "последняя строка ответа: %s, а маркер идёт голой строкой marker: <имя>"
                      % (last or "пусто"), 1)
        if marker != "continue":
            self.stop(marker, "виток кончился маркером %s, разбор в файле цели docs/tasks/%s.md"
                      % (marker, self.id), 0)
        return after > before

    def run_foreground(self):
        if not self.take_lock():
            die("цикл цели %s уже идёт, замок %s, владелец %s" % (self.id, self.lock, self.lock_owner()), 3)
        try:
            signal.signal(signal.SIGINT, lambda *_: sys.exit(1))
            signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))
            idle = 0
            self.say("цикл цели %s начат в %s, pid %d" % (self.id, self.proj, os.getpid()))
            while True:
                self.turn += 1
                progressed = self.run_turn()
                if progressed:
                    idle = 0
                else:
                    idle += 1
                    self.say("виток %d не записал в «Журнал» ничего, подряд таких %d из %d"
                              % (self.turn, idle, FUNNEL_LIMIT))
                # Воронка это витки, которые говорят «работа осталась» и не
                # делают ничего. Маркером stuck виток останавливает цикл сам,
                # тут ловится молчащий continue: без счётчика он жёг бы
                # бюджет цели до потолка.
                if idle >= FUNNEL_LIMIT:
                    self.stop("воронка", "%d витка подряд без записи в «Журнале», цикл жжёт бюджет вхолостую"
                              % idle, 1)
        finally:
            self.release()


def main(argv):
    goal_id, proj, fg = parse_args(argv)
    proj = resolve_proj(proj)
    loop = Loop(goal_id, proj)
    loop.preflight()
    if not fg:
        loop.launch_tmux()
        return 0
    loop.run_foreground()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
