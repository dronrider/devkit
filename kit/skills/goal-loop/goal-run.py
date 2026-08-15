#!/usr/bin/env python3
"""Оболочка режима цели: поднимает витки скилла goal-loop одноразовыми
headless-сессиями, пока виток отвечает маркером continue. Живучесть цикла от
живучести сессии тут не зависит: состояние цели целиком лежит на диске (файл
цели, доска, git, деревья задач), а оболочка только зовёт следующий виток и
решает, когда перестать звать.

  goal-run.py <ID> [-C <корень проекта>] [--foreground]
  goal-run.py <ID> [-C <корень проекта>] --say <строка хода>

Без --foreground цикл уходит в свою tmux-сессию goal-<ID>, как съёмщик панели
/usage поднимает клиента в одноразовой сессии, и оболочка возвращается сразу.
С --foreground витки идут в этом же процессе: так цикл смотрят вживую и так
его гоняют тесты.

Ключ --say цикла не поднимает вовсе: он дописывает строку хода в тот же журнал
и возвращается. Пишет её сам виток, пока работает, и по этим строкам видно
живой виток, чей вывод сессии буферизуется до конца.

Коды возврата: 0 штатный стоп по маркеру витка, 1 цикл остановила сама
оболочка (исчерпанные попытки поднять вставший виток, воронка), 2 ошибка вызова
или окружения, 3 цикл этой цели уже идёт.
"""
import os
import re
import shlex
import shutil
import signal
import subprocess
import sys
import time
import uuid

AUTONOMOUS_RE = re.compile(r"^\s*autonomous\s*=\s*true")

HERE = os.path.dirname(os.path.abspath(__file__))
NOTIFIER = os.path.normpath(os.path.join(HERE, "..", "..", "..", "hooks", "notify.py"))
PERMS = os.path.normpath(os.path.join(HERE, "..", "..", "..", "tools", "devkitctl", "perms.py"))
FUNNEL_LIMIT = 3  # витков подряд без записи в «Журнале», после которых стоп
# Стоп без вердикта (упавшая сессия, обрыв соединения, ответ без маркера) это
# не приговор цели: состояние лежит на диске, и продолжение механическое, той
# же командой. Поэтому такой виток поднимается заново, а предел держит цикл от
# бесконечного долбления в сломанное, как FUNNEL_LIMIT держит его от воронки.
RETRY_LIMIT = 3
RETRY_PAUSE = 20  # секунд между попытками: обрыв на стороне API переживает пауза
PAUSE_ENV = "DEVKIT_GOAL_RETRY_PAUSE"
MARKERS = ("continue", "done", "over", "wait-human", "stuck")
# Повод стопа словом для журнала уведомителя: по нему лента дашборда отличает
# вставший цикл от цикла, который зовёт человека к делу. Остальные маркеры и
# стопы без вердикта идут общим поводом, разбор стоит в самом заголовке.
STOP_KEY = "goal_stop"
STOP_REASONS = {"wait-human": "wait_human"}

USAGE = """\
goal-run.py <ID> [-C <корень проекта>] [--foreground | --say <строка>]

Витки цели <ID> одноразовыми сессиями, пока виток отвечает marker: continue.
Любой другой маркер завершает цикл и зовёт уведомитель громким поводом.

  -C <корень>    проект с доской, по умолчанию текущая директория
  --foreground   держать цикл в этом процессе, а не в tmux-сессии goal-<ID>
  --say <строка> дописать строку хода в журнал цели и выйти, цикла не поднимая

Виток, вставший без вердикта (упал, оборвался, ответил без маркера),
поднимается заново, попыток подряд не больше трёх. Паузу между ними (20 секунд)
перебивает DEVKIT_GOAL_RETRY_PAUSE, это для стендов.

Ход цикла пишется в <корень>/.devkit/goal-<ID>.log: время, номер витка,
маркер, код выхода, а строками --say туда же ложится ход самого витка.

Каждый виток поднимается со своим именем сессии (--session-id), и оно лежит
файлом session в каталоге замка рядом с pid: по нему почтальон hooks/inbox.py
находит идущий виток и доносит до него реплику человека из чата.
"""


def die(text, code=2):
    sys.stderr.write(text + "\n")
    sys.exit(code)


def parse_args(argv):
    goal_id = None
    proj = os.getcwd()
    fg = False
    note = None
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
        elif arg == "--say":
            if i + 1 >= len(argv):
                die("у --say нет значения: строка хода это его аргумент")
            note = argv[i + 1]
            if note.strip() == "":
                die("строка хода пустая, в журнале от неё толку нет")
            i += 2
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
    if fg and note is not None:
        die("--say и --foreground вместе не ходят: строка хода цикла не поднимает")
    return goal_id, proj, fg, note


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
        # Имя текущего витка: по нему почтальон (hooks/inbox.py) отличает виток
        # цели от соседнего окна того же корня. Имя заводится на виток, а не на
        # замок: одно имя на весь прогон второй виток поднимал бы уже занятым,
        # и клиент либо отбил бы его кодом, либо поднял виток поверх чужого
        # контекста.
        self.sessfile = os.path.join(self.lock, "session")
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
            # повторным запуском. Каталог снимается целиком, а не перечнем
            # известных имён: содержимое замка это его частное дело, и рядом с
            # pid там живёт имя витка, из-за которого rmdir ушёл бы в
            # ENOTEMPTY, а брошенный цикл чинился бы с клавиатуры.
            shutil.rmtree(self.lock, ignore_errors=True)
            try:
                os.mkdir(self.lock)
            except OSError:
                return False
        with open(self.pidfile, "w", encoding="utf-8") as f:
            f.write("%d\n" % os.getpid())
        return True

    def release(self):
        # Каталог уходит целиком, вместе с именем витка: ни одно имя не должно
        # пережить оболочку, иначе почтальон адресовал бы почту сессии, которой
        # давно нет.
        shutil.rmtree(self.lock, ignore_errors=True)

    # -- журнал оболочки и уведомитель --------------------------------------

    def append(self, msg):  # строка с временем в журнал цели, ошибка записи наверх
        line = "%s %s" % (time.strftime("%Y-%m-%dT%H:%M:%S"), msg)
        os.makedirs(self.devdir, exist_ok=True)
        with open(self.log, "a", encoding="utf-8") as f:
            f.write(line + "\n")
        return line

    def say(self, msg):  # строка в журнал цикла и в вывод: панель tmux
        # показывает то же
        try:
            line = self.append(msg)
        except OSError:
            line = "%s %s" % (time.strftime("%Y-%m-%dT%H:%M:%S"), msg)
        print(line)

    def note(self, msg):  # ключ --say: строка хода витка мимо цикла и замка
        # Виток пишет ход туда же, куда пишет цикл: у вопроса «жив ли цикл»
        # одно место ответа при любом способе запуска, и tail на него
        # нацеливается один раз. Провал записи тут громкий, в отличие от say:
        # молча потерянная строка хода это ровно та беда, ради которой ключ и
        # заведён.
        if not os.path.isfile(self.goal):
            die("файла цели %s нет: ход пишется в журнал своей цели" % self.goal)
        try:
            print(self.append(msg))
        except OSError as e:
            die("строку хода не записать в %s: %s" % (self.log, e))

    def shout(self, key, title, text):  # повод, заголовок, текст: стоп цикла молчать не должен
        if not os.path.isfile(NOTIFIER):
            self.say("уведомителя %s нет, стоп остался без голоса" % NOTIFIER)
            return
        try:
            # Цель едет ключом --task: событие ленты ведёт к строке цели полем,
            # а не разбором заголовка (DK-323). Проект уведомитель соберёт сам
            # по рабочей директории, а зовётся он из корня проекта.
            p = subprocess.run(["python3", NOTIFIER, "--reason", key,
                                "--task", self.id, title, text],
                                cwd=self.proj,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            out = p.stdout.rstrip("\n")
        except OSError as e:
            out = str(e)
        self.say("уведомление: %s; %s" % (title, out or "отправлено"))

    def stop(self, reason, text, code):  # повод, текст, код
        self.say("цикл цели %s остановлен: %s, витков %d" % (self.id, reason, self.turn))
        self.shout(STOP_REASONS.get(reason, STOP_KEY), "цель %s: %s" % (self.id, reason), text)
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
        # Строка о подъёме идёт до сессии, а не после: вывод `claude -p`
        # буферизуется до конца витка, и без неё журнал молчал бы всё время
        # работы, а молчание неотличимо от вставшего цикла. Дальше журнал
        # наполняет сам виток строками --say.
        self.say("виток %d поднят, ход витка ниже" % self.turn)
        # Своё имя витку выдаётся до подъёма клиента и кладётся в замок: почта
        # человека едет в идущий виток по этому имени, и файл переписывается
        # целиком, чтобы имя прошлого витка не осталось хвостом. Между витками
        # в файле лежит имя того, кто уже кончился, и читается это правильно:
        # почта тогда никому не уезжает и её прочитает шаг 1 следующего витка.
        sid = str(uuid.uuid4())
        try:
            with open(self.sessfile, "w", encoding="utf-8") as f:
                f.write(sid + "\n")
        except OSError as e:
            self.say("имя витка %d не записано в замок (%s): живая реплика доедет "
                      "не раньше следующего витка" % (self.turn, e))
        p = subprocess.run(["claude", "-p", "--session-id", sid, self.prompt], cwd=self.proj,
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
        # Стоп без вердикта возвращается наверх поводом, а не останавливает
        # цикл прямо тут: решение, поднять виток заново или встать, принимает
        # run_foreground, у которого есть счётчик попыток.
        if code != 0:
            return ("авария витка", "сессия витка вышла с кодом %d, последняя строка: %s"
                    % (code, last or "пусто"), False)
        if not marker:
            return ("виток без маркера",
                    "последняя строка ответа: %s, а маркер идёт голой строкой marker: <имя>"
                    % (last or "пусто"), False)
        if marker != "continue":
            self.stop(marker, "виток кончился маркером %s, разбор в файле цели docs/tasks/%s.md"
                      % (marker, self.id), 0)
        return ("", "", after > before)

    def retry_pause(self):
        try:
            pause = int(os.environ.get(PAUSE_ENV, ""))
        except ValueError:
            pause = RETRY_PAUSE
        return max(0, pause)

    def run_foreground(self):
        if not self.take_lock():
            die("цикл цели %s уже идёт, замок %s, владелец %s" % (self.id, self.lock, self.lock_owner()), 3)
        try:
            signal.signal(signal.SIGINT, lambda *_: sys.exit(1))
            signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))
            idle = 0
            tries = 0
            self.say("цикл цели %s начат в %s, pid %d" % (self.id, self.proj, os.getpid()))
            while True:
                self.turn += 1
                reason, text, progressed = self.run_turn()
                if reason:
                    tries += 1
                    if tries >= RETRY_LIMIT:
                        self.stop("попытки исчерпаны",
                                  "%d витка подряд встали без вердикта, последний повод: %s (%s); "
                                  "цикл продолжается той же командой, когда причина понята"
                                  % (tries, reason, text), 1)
                    pause = self.retry_pause()
                    self.say("виток %d встал без вердикта (%s), перезапуск после стопа без вердикта: "
                              "попытка %d из %d через %d с" % (self.turn, reason, tries + 1, RETRY_LIMIT, pause))
                    time.sleep(pause)
                    continue
                tries = 0
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
    goal_id, proj, fg, note = parse_args(argv)
    proj = resolve_proj(proj)
    loop = Loop(goal_id, proj)
    if note is not None:
        # Предполётной проверки строке хода не нужно: её пишет уже идущий
        # виток, в том числе виток живого чата, где ни autonomous, ни claude в
        # PATH оболочку не касаются.
        loop.note(note)
        return 0
    loop.preflight()
    if not fg:
        loop.launch_tmux()
        return 0
    loop.run_foreground()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
