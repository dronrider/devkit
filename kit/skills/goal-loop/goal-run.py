#!/usr/bin/env python3
"""Оболочка режима цели: поднимает витки скилла goal-loop одноразовыми
headless-сессиями, пока виток отвечает маркером continue. Живучесть цикла от
живучести сессии тут не зависит: состояние цели целиком лежит на диске (файл
цели, доска, git, деревья задач), а оболочка только зовёт следующий виток и
решает, когда перестать звать.

  goal-run.py <ID> [-C <корень проекта>] [--harness <подписка>] [--tier <ярус>]
                   [--foreground]
  goal-run.py <ID> [-C <корень проекта>] --say <строка хода>
  goal-run.py <ID> [-C <корень проекта>] --ask <вопрос человеку>

Без --foreground цикл уходит в свою tmux-сессию goal-<ID>, как съёмщик панели
/usage поднимает клиента в одноразовой сессии, и оболочка возвращается сразу.
С --foreground витки идут в этом же процессе: так цикл смотрят вживую и так
его гоняют тесты.

Ключ --say цикла не поднимает вовсе: он дописывает строку хода в тот же журнал
и возвращается. Пишет её сам виток, пока работает, и по этим строкам видно
живой виток, чей вывод сессии буферизуется до конца.

Ключ --ask цикла не поднимает тоже: он зовёт человека уведомлением и ждёт
ответа во «Входящих» файла цели, до срока. Дождавшись, он отмечает съеденную
строку во входе цели и печатает её витку, не дождавшись, печатает «ответа
нет»; истёкшее ожидание это не авария, а обычный возврат.

Коды возврата: 0 штатный стоп по маркеру витка, 1 цикл остановила сама
оболочка (исчерпанные попытки поднять вставший виток, воронка), 2 ошибка вызова
или окружения, 3 цикл этой цели уже идёт.
"""
import importlib.util
import json
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
HOOKS = os.path.normpath(os.path.join(HERE, "..", "..", "..", "hooks"))
NOTIFIER = os.path.join(HOOKS, "notify.py")
PERMS = os.path.normpath(os.path.join(HERE, "..", "..", "..", "tools", "devkitctl", "perms.py"))
# Утилита раскладки подписок: она же поднимает клиента чужой подписки своей
# обвязкой (agentctl exec), и пары окружения второй подписки собирает она, а не
# оболочка цикла.
AGENTCTL = "agentctl"
FUNNEL_LIMIT = 3  # витков подряд без записи в «Журнале», после которых стоп
# Стоп без вердикта (упавшая сессия, обрыв соединения, ответ без маркера) это
# не приговор цели: состояние лежит на диске, и продолжение механическое, той
# же командой. Поэтому такой виток поднимается заново, а предел держит цикл от
# бесконечного долбления в сломанное, как FUNNEL_LIMIT держит его от воронки.
RETRY_LIMIT = 3
RETRY_PAUSE = 20  # секунд между попытками: обрыв на стороне API переживает пауза
PAUSE_ENV = "DEVKIT_GOAL_RETRY_PAUSE"
MARKERS = ("continue", "done", "over", "wait-human", "stuck")
# Маркер wait-human остаётся в перечне, а повод у него сузился до уровня цели:
# вопрос или окружение одной задачи виток закрывает парковкой строки
# (taskctl move ... blocked, DK-402) и отвечает continue, стоп-механика
# оболочки не меняется (LLD DK-400, решение 6). Нового слова протоколу нет:
# маркер это вердикт витка перед оболочкой, а парковка состояние доски.
# Повод стопа словом для журнала уведомителя: по нему лента дашборда отличает
# вставший цикл от цикла, который зовёт человека к делу. Остальные маркеры и
# стопы без вердикта идут общим поводом, разбор стоит в самом заголовке.
STOP_KEY = "goal_stop"
STOP_REASONS = {"wait-human": "wait_human"}
# Вопрос человеку зовётся тем же поводом, что и маркер wait-human: лента
# дашборда отбирает по нему зов человека, а вопрос витка это он и есть.
ASK_KEY = "wait_human"
# Срок ожидания ответа. Потолок хода Bash у харнеса 600 секунд, и упираться в
# него нельзя: ход, убитый потолком, не снимет за собой признак ожидания сам, и
# вход цели простоит запертым до его срока.
ASK_WAIT = 300
ASK_WAIT_ENV = "DEVKIT_GOAL_ASK_WAIT"
# Секунда между опросами «Входящих»: чтение файла цели против ожидания
# человека это ничто, а опрос пореже добавляет задержку ответа к минутам, что
# человек и так провёл за клавиатурой.
ASK_POLL = 1
# Сколько ключ ждёт замок отметок. Подхват реплики под чужим замком уходит тихим
# нолём, а тут молчание это потерянный ответ на прямой вопрос, поэтому ключ
# ждёт, и ждёт коротко: замок держат на одну запись файла.
ASK_LOCK_WAIT = 5
STAMP = "%Y-%m-%dT%H:%M:%S"

USAGE = """\
goal-run.py <ID> [-C <корень проекта>] [--foreground | --say <строка> | --ask <вопрос>]

Витки цели <ID> одноразовыми сессиями, пока виток отвечает marker: continue.
Любой другой маркер завершает цикл и зовёт уведомитель громким поводом.

  -C <корень>    проект с доской, по умолчанию текущая директория
  --harness <имя> подписка, чьей квотой платятся витки; без флага подписка
                 по умолчанию, как было
  --tier <ярус>  ярус модели витка (mini, base, pro, max); без флага pro.
                 В модель ярус разворачивает раскладка машины (agentctl
                 harness --json), и второй подписке модель не называется:
                 её профиль называет её сам
  --foreground   держать цикл в этом процессе, а не в tmux-сессии goal-<ID>
  --say <строка> дописать строку хода в журнал цели и выйти, цикла не поднимая
  --ask <вопрос> задать вопрос человеку и ждать ответа во «Входящих» цели

Вопрос уезжает громким уведомлением и ложится строкой хода в журнал цели, а
ключ ждёт ответа 300 секунд (перебивает DEVKIT_GOAL_ASK_WAIT, это для стендов).
Ответом считается первая строка «Входящих» без отметки доставки: ключ пишет
отметку в <корень>/.devkit/goal-<ID>.mail и печатает строку витку как есть,
вместе со временем, которое в ней стоит. Не дождавшись, он печатает «ответа
нет» и выходит нолём: срок вышел это обычный возврат, дальше виток решает сам.

Пока ключ ждёт, вход цели принадлежит ему: срок ожидания лежит в файле
<корень>/.devkit/goal-<ID>.ask, и подхват hooks/chat-in.py при непротухшем
сроке не доставляет витку ничего, чтобы ответ на вопрос не уехал мимо ключа
чужим ходом инструмента. Файл снимается на любом выходе, а срок внутри держит
канал открытым, если ход ожидания убили.

Виток, вставший без вердикта (упал, оборвался, ответил без маркера),
поднимается заново, попыток подряд не больше трёх. Паузу между ними (20 секунд)
перебивает DEVKIT_GOAL_RETRY_PAUSE, это для стендов.

Ход цикла пишется в <корень>/.devkit/goal-<ID>.log: время, номер витка,
маркер, код выхода, а строками --say туда же ложится ход самого витка.

Каждый виток поднимается со своим именем сессии (--session-id), и оно лежит
файлом session в каталоге замка рядом с pid: по нему подхват hooks/chat-in.py
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
    question = None
    harness = ""
    tier = ""
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
        elif arg == "--ask":
            if i + 1 >= len(argv):
                die("у --ask нет значения: вопрос человеку это его аргумент")
            question = argv[i + 1]
            if question.strip() == "":
                die("вопрос пустой, отвечать человеку не на что")
            i += 2
        elif arg == "--harness":
            if i + 1 >= len(argv):
                die("у --harness нет значения: имя подписки это его аргумент")
            harness = argv[i + 1]
            if harness.strip() == "":
                die("имя подписки пустое, поднимать виток нечем")
            i += 2
        elif arg == "--tier":
            if i + 1 >= len(argv):
                die("у --tier нет значения: имя яруса это его аргумент")
            tier = argv[i + 1]
            if tier.strip() == "":
                die("имя яруса пустое, разворачивать в модель нечего")
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
    if fg and question is not None:
        die("--ask и --foreground вместе не ходят: вопрос человеку цикла не поднимает")
    if note is not None and question is not None:
        die("--say и --ask вместе не ходят: вопрос и так пишет свою строку хода")
    return goal_id, proj, fg, note, question, harness, tier


def resolve_proj(proj):
    abspath = proj if os.path.isabs(proj) else os.path.join(os.getcwd(), proj)
    abspath = os.path.normpath(abspath)
    if not os.path.isdir(abspath):
        die("корня проекта %s нет" % proj)
    return abspath


def harness_layout():
    """Раскладка подписок машины. Знает её agentctl, своего перечня у оболочки
    нет вовсе, иначе включённая завтра подписка сюда бы не доехала."""
    p = subprocess.run([AGENTCTL, "harness", "--json"], stdout=subprocess.PIPE,
                        stderr=subprocess.STDOUT, text=True)
    if p.returncode != 0:
        die("раскладка подписок не прочиталась (%s harness --json): %s"
            % (AGENTCTL, (p.stdout or "").strip()))
    try:
        return json.loads(p.stdout or "{}")
    except ValueError as e:
        die("раскладка подписок не разобралась: %s" % e)


def tier_model(name, tier):
    """Модель яруса по раскладке машины. Имён моделей оболочка не сочиняет:
    лестницу ярусов держит подписка, и завтрашняя её правка доезжает сюда без
    правки оболочки. Пустой ответ значит «называть модель нечем»: такого яруса
    у подписки нет либо раскладка о ярусах молчит, и виток идёт как шёл."""
    if not tier:
        return ""
    data = harness_layout()
    want = name or data.get("default") or ""
    for h in data.get("harnesses", []):
        if h.get("name") != want:
            continue
        for m in h.get("models", []):
            if m.get("tier") == tier:
                return m.get("model") or ""
        names = ", ".join(m.get("tier", "") for m in h.get("models", []))
        die("яруса %s у подписки %s нет%s"
            % (tier, want, (", ярусы подписки: " + names) if names else ""))
    return ""


def harness_client(name):
    """Клиент подписки по её имени. Пустой ответ значит подписку по умолчанию:
    её клиент зовётся как звался, без обвязки."""
    data = harness_layout()
    for h in data.get("harnesses", []):
        if h.get("name") != name:
            continue
        if not h.get("enabled", True):
            die("подписка %s выключена в раскладке машины, витки ей не платятся" % name)
        if h.get("default"):
            return ""
        client = h.get("bin") or ""
        if not client:
            die("у подписки %s в раскладке нет клиента, поднимать виток нечем" % name)
        return client
    die("подписки %s в раскладке машины нет: включённые называет %s harness"
        % (name, AGENTCTL))


class Loop:
    """Состояние одного цикла: пути цели, лог, замок и подсчёт воронки. Класс
    только группирует то, что в sh было переменными верхнего уровня, своей
    логики сверх методов ниже у него нет."""

    def __init__(self, goal_id, proj, harness="", tier=""):
        self.id = goal_id
        self.proj = proj
        # Подписка, чьей квотой платятся витки. Пусто это подписка по
        # умолчанию, ровно то, что цикл делал всегда. Имя живёт весь прогон:
        # витку его не выбирают заново, платит цель одним кошельком.
        self.harness = harness
        # Ярус модели витка. Пусто значит «как звался клиент», а имя ярусом
        # разворачивается в модель раскладкой машины, один раз на прогон:
        # витки цели идут одним весом, и решать это заново каждому витку не за
        # чем.
        self.tier = tier
        self.model = ""
        self.devdir = os.path.join(proj, ".devkit")
        self.deploy = os.path.join(self.devdir, "deploy.local")
        self.goal = os.path.join(proj, "docs", "tasks", "%s.md" % goal_id)
        self.log = os.path.join(self.devdir, "goal-%s.log" % goal_id)
        self.lock = os.path.join(self.devdir, "goal-%s.lock" % goal_id)
        # Носитель цели и признак ожидания: имена общие с подхватом реплики,
        # он читает те же файлы (hooks/chat-in.py, ask_until и serve).
        self.mailfile = os.path.join(self.devdir, "goal-%s.mail" % goal_id)
        self.askfile = os.path.join(self.devdir, "goal-%s.ask" % goal_id)
        self.pidfile = os.path.join(self.lock, "pid")
        # Имя текущего витка: по нему подхват (hooks/chat-in.py) отличает виток
        # цели от соседнего окна того же корня. Имя заводится на виток, а не на
        # замок: одно имя на весь прогон второй виток поднимал бы уже занятым,
        # и клиент либо отбил бы его кодом, либо поднял виток поверх чужого
        # контекста.
        self.sessfile = os.path.join(self.lock, "session")
        self.sess = "goal-%s" % goal_id
        self.prompt = "продолжай цель %s по скиллу goal-loop" % goal_id
        self.turn = 0

    # -- предполётные проверки --------------------------------------------

    def turn_cmd(self, sid):
        """Команда витка. Своя подписка поднимается своей же обвязкой, и режим
        разрешений ей называется флагом: свежий профиль второй подписки поднял
        бы клиента в ручном режиме, а одобрять запросы в headless-витке
        некому."""
        turn = ["-p", "--session-id", sid, self.prompt]
        client = getattr(self, "client", "")
        # Ярус называется только своей подписке: у второй модель называет её
        # собственный профиль, и флаг тут спорил бы с её настройкой. Без флага
        # клиент берёт свой дефолт, а он бывает верхним ярусом, которого витку
        # никто не назначал.
        model = ["--model", self.model] if self.model else []
        if not self.harness or not client:
            return ["claude"] + model + turn
        return [AGENTCTL, "exec", "--harness", self.harness, "--", client,
                "--permission-mode", "auto"] + turn

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
        if self.harness:
            # Клиент чужой подписки поднимается её обвязкой, и проверять надо
            # именно её: самого claude в PATH при этом может не быть вовсе.
            if shutil.which(AGENTCTL) is None:
                die("%s в PATH нет, а виток заказан подпиской %s: поднимать его нечем"
                    % (AGENTCTL, self.harness))
            self.client = harness_client(self.harness)
        elif shutil.which("claude") is None:
            die("claude в PATH нет, поднимать витки нечем")
        # Ярус разворачивается в модель до первого витка: раскладку спрашиваем
        # один раз, а отказ «такого яруса нет» человек обязан увидеть сразу, а
        # не после первого сожжённого витка.
        if self.tier and not getattr(self, "client", ""):
            self.model = tier_model(self.harness, self.tier)
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
        # пережить оболочку, иначе подхват адресовал бы реплику сессии, которой
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

    # -- ключ --ask: вопрос человеку -----------------------------------------

    def chat_hook(self):
        """Модуль подхвата реплики hooks/chat-in.py. Носитель у ключа с ним
        общий, поэтому разбор «Входящих», формат отметки доставки и замок
        берутся у него готовыми: свой второй разбор тех же файлов разъехался бы
        с первым на первой же правке формата, а платит за такой разъезд человек,
        чей ответ приехал витку дважды. Дефис в имени файла не годится для
        import, поэтому модуль грузится по пути."""
        path = os.path.join(HOOKS, "chat-in.py")
        sys.path.insert(0, HOOKS)
        try:
            spec = importlib.util.spec_from_file_location("devkit_chat_in", path)
            if spec is None or spec.loader is None:
                raise ImportError("модуль не собрался из %s" % path)
            mod = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(mod)
            return mod
        except (OSError, ImportError, SyntaxError) as e:
            die("подхват реплики в %s не прочитать: %s; носитель цели у ключа с ним общий"
                % (HOOKS, e))
        finally:
            sys.path.pop(0)

    def hold(self, until):
        """Признак ожидания: срок, до которого вход цели принадлежит вопросу.
        Кладётся до первого опроса, иначе окно между зовом человека и первым
        чтением входа остаётся подхвату. Срок лежит внутри файла затем, чтобы
        убитый ход ожидания не запер канал навсегда: признак с прошедшим сроком
        подхват читает как его отсутствие."""
        os.makedirs(self.devdir, exist_ok=True)
        tmp = self.askfile + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            f.write(time.strftime(STAMP, time.localtime(until)) + "\n")
        os.replace(tmp, self.askfile)

    def drop_hold(self):
        try:
            os.remove(self.askfile)
        except OSError:
            pass

    def marks_lock(self, hook, until):
        """Замок отметок, взятый с ожиданием. Ключ держит его на один опрос
        входа, а не на всё ожидание: замок про то, идёт ли прямо сейчас запись
        файла отметок, а признак ожидания про то, занят ли вход вопросом."""
        while True:
            fd = hook.take_chat_lock(self.mailfile + ".lock")
            if fd is not None:
                return fd
            if time.time() >= until:
                return None
            time.sleep(min(0.2, max(0.0, until - time.time())))

    def reply(self, hook):
        """Первая строка «Входящих» без отметки доставки либо None. Отметка
        встаёт до печати и по тем же правилам, по каким её ставит подхват
        реплики: отмечает строку тот, кто отдал её витку, иначе съеденная
        вопросом реплика приедет витку вторым экземпляром на первом же ходе."""
        fd = self.marks_lock(hook, time.time() + ASK_LOCK_WAIT)
        if fd is None:
            return None
        try:
            with open(self.goal, encoding="utf-8", errors="replace") as f:
                lines = hook.goal_lines(f.read())
            marks = hook.read_marks(self.mailfile)
            # «Входящие» читаются раньше отметок: отметка, чьей строки там
            # больше нет, не считается и в новый файл не переносится.
            lying = set(lines)
            kept = [m for m in marks if m.line in lying]
            known = {m.line for m in kept}
            fresh = [line for line in lines if line not in known]
            if not fresh:
                return None
            stamp = time.strftime(STAMP)
            hook.write_marks(self.mailfile, kept
                             + [hook.Mark(stamp, self.turn_name() or "-", fresh[0])])
            return fresh[0]
        except OSError as e:
            self.say("ответ не прочитать: %s" % e)
            return None
        finally:
            os.close(fd)

    def turn_name(self):
        """Имя витка из замка оболочки. У витка живого чата, где замка нет
        вовсе, оно пустое, и дашборд читает пустое поле отметки именно так."""
        try:
            with open(self.sessfile, encoding="utf-8") as f:
                return f.read().strip()
        except OSError:
            return ""

    def ask(self, question, wait):
        """Вопрос человеку с ожиданием ответа. Ключ цикла не поднимает: его
        зовёт идущий виток, которому ответ нужен сейчас, а не через виток."""
        if not os.path.isfile(self.goal):
            die("файла цели %s нет: вопрос задаётся по своей цели" % self.goal)
        hook = self.chat_hook()
        try:
            self.append("вопрос человеку: %s" % question)
        except OSError as e:
            die("вопрос не записать в %s: %s" % (self.log, e))
        self.shout(ASK_KEY, "цель %s: вопрос человеку" % self.id, question)
        until = time.time() + wait
        self.hold(until)
        try:
            while True:
                line = self.reply(hook)
                if line is not None:
                    self.say("ответ человека получен")
                    print(line)
                    return 0
                if time.time() + ASK_POLL >= until:
                    break
                time.sleep(ASK_POLL)
            self.say("ответа нет: срок ожидания %d с вышел" % wait)
            return 0
        finally:
            # Признак снимается на любом выходе, ответом, сроком и падением:
            # оставленный признак запирает вход цели до своего срока, и человек
            # всё это время пишет витку в пустоту.
            self.drop_hold()

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
        args = [os.path.join(HERE, "goal-run.py"), self.id, "-C", self.proj]
        # Подписка едет в свою же tmux-сессию: цикл там поднимает витки сам, и
        # без флага он платил бы подпиской по умолчанию, а человек выбирал
        # другую.
        if self.harness:
            args += ["--harness", self.harness]
        # Ярус едет туда же и по той же причине: цикл в своей сессии поднимает
        # витки сам, и без флага виток пошёл бы дефолтом клиента.
        if self.tier:
            args += ["--tier", self.tier]
        args.append("--foreground")
        cmd = " ".join(shlex.quote(a) for a in args)
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
        # Своё имя витку выдаётся до подъёма клиента и кладётся в замок: реплика
        # человека едет в идущий виток по этому имени, и файл переписывается
        # целиком, чтобы имя прошлого витка не осталось хвостом. Между витками
        # в файле лежит имя того, кто уже кончился, и читается это правильно:
        # реплика тогда никому не уезжает и её прочитает шаг 1 следующего витка.
        sid = str(uuid.uuid4())
        try:
            with open(self.sessfile, "w", encoding="utf-8") as f:
                f.write(sid + "\n")
        except OSError as e:
            self.say("имя витка %d не записано в замок (%s): живая реплика доедет "
                      "не раньше следующего витка" % (self.turn, e))
        p = subprocess.run(self.turn_cmd(sid), cwd=self.proj,
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


def ask_wait():
    """Срок ожидания ответа в секундах. Перебивает его окружение, и это для
    стендов: живой ключ ждёт минутами, а тесту столько ждать незачем."""
    raw = (os.environ.get(ASK_WAIT_ENV) or "").strip()
    if not raw:
        return ASK_WAIT
    try:
        wait = float(raw)
    except ValueError:
        die("%s=%s это не число секунд" % (ASK_WAIT_ENV, raw))
    if wait <= 0:
        die("%s=%s: ждать ответа надо хоть сколько-то" % (ASK_WAIT_ENV, raw))
    return wait


def main(argv):
    goal_id, proj, fg, note, question, harness, tier = parse_args(argv)
    proj = resolve_proj(proj)
    loop = Loop(goal_id, proj, harness, tier)
    if question is not None:
        # Предполётной проверки вопросу не нужно по той же причине, что и
        # строке хода: его задаёт уже идущий виток, в том числе виток чата.
        return loop.ask(question, ask_wait())
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
