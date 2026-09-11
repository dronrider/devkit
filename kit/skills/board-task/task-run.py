#!/usr/bin/env python3
"""Оболочка конвейера задачи. Ведёт работу по строке доски, пока она не закрыта.
Живучесть конвейера от живучести сессии тут не зависит, как и у цикла цели
(goal-run.py). Состояние задачи целиком лежит на диске, доска, git и дерево
задачи, а оболочка только заказывает следующий проход и решает, когда перестать
заказывать.

  task-run.py <ID> [-C <корень проекта>] [--order <заказ первого прохода>]
              [--again <заказ следующих>] [--passes N] [--project <имя>]
              [--headless] [-- <команда клиента без -p>]

Голов у конвейера две, и выбирает между ними окружение.

Живая голова это одна интерактивная сессия в окне tmux, поднятая тут же, в
панели оболочки. Первый заказ едет клиенту первым аргументом, следующие
приходят в тот же процесс через `tmux send-keys`, той же дорогой, какой панель
дашборда подаёт реплику человека. Процесс не выходит между проходами. Поэтому
долгая команда и фоновый субагент доживают до конца, уведомление доходит до
головы, разгон не сгорает, а в реестре чатов на задачу приходится одна запись.

Печатная голова это прежняя череда проходов `claude -p`. Каждый проход свой
процесс, и конец хода это его выход. Она остаётся запасным входом и берётся
там, где живую вести негде. Нет tmux, нет имени окна в DEVKIT_TMUX либо
конвейер позвали с флагом `--headless`.

Конец прохода у живой головы оболочка узнаёт отметкой хука Stop
(hooks/turn-mark.py). Процесс не выходит, кода возврата нет, транскрипт растёт
и на середине долгого хода, а панель терминала читается глазами, но не
разбором; харнес же говорит про конец хода прямо, своим событием. Отметки
ложатся в ~/.devkit/turns.log, оболочка читает оттуда только строки своей
сессии, а сессию окна называет реестр чатов (~/.devkit/sessions.log, имя окна
там пишет лишь клиент со своим терминалом, DK-673). Оба журнала общие на машину и режутся хуком по
размеру, поэтому позиции в файле оболочка не держит: после реза смещение
прошлого чтения вернуло бы уже прочитанную отметку второй раз. Вместо этого она
отбирает свои строки и помнит те, что уже прочла. Слово «начат» в тех же
отметках держит оболочку от заказа поверх чужого хода. Реплику человека панель
подаёт в это же окно.

Три случая, на которых живая голова зовёт человека уведомителем. Сессия
упёрлась в незнакомый запрос разрешения, сессия молчит получасом без единой
отметки (отметка хода не подключена либо окно замёрзло) и клиент вышел раньше
задачи. Молчаливого стопа у конвейера нет. С экрана дашборда закрывшееся окно
неотличимо от штатного конца.

Вопрос сессии оболочка узнаёт двумя каналами. Первый это отметка «ждёт» с
поводом запроса, второй строка того же повода в журнале уведомителя
(~/.devkit/notify.log). Второй канал не роскошь. Отметки пишет хук
turn-mark.py, и сессия, поднятая до того, как он лёг в настройки харнеса, не
пишет их ни одной. Так простоял двенадцать минут конвейер DK-724, а
уведомитель на том же событии сработал и строку записал. Один вопрос приходит
обоими каналами, и второй зов о нём придержан. Придержание держится за время
самого события, а конец и начало хода его снимают: второй вопрос той же
причины это другой вопрос, и молчать о нём нельзя.

Замок на задачу оболочка держит всю свою жизнь. Это каталог
~/.devkit/task-<ID>.lock с её pid, общий с командой подъёма `taskctl run`
(internal/taskhead, DK-931). Команда берёт замок первой и передаёт его
оболочке переменной DEVKIT_TASK_LOCK_FROM, а оболочка вписывает туда свой pid.
Поднятая мимо команды оболочка (дашборд, терминал) берёт замок сама. Занятый
живым владельцем замок это стоп кодом 3, и вторая голова по задаче не встаёт.
Перезагрузка машины разбирается сама собой. Pid в замке мёртв, замок снимается
как брошенный, и работа поднимается заново с доски и из дерева задачи.

Заказ проходу оболочка не сочиняет. Слова приходят флагами от того, кто её
позвал, и лежат в одном месте (у дашборда это runPrompt). Умолчание держится
только на случай ручного запуска из терминала.

Клиента тоже называет зовущий, хвостом после `--`. Без хвоста оболочка берёт
его из профиля харнеса: ключ client секции [head] в kit/harness/<имя>.toml, имя
из DEVKIT_HARNESS, по умолчанию claude-code. Прибитой строки клиента в оболочке
нет. Профиль с turn_end = "exit" говорит, что отметки конца хода у клиента нет,
и голова тогда идёт печатной чередой.

Проход кончается концом хода, каким бы он ни был. Ни код возврата, ни слово
отметки тут не вердикт, вердикт это статус строки на доске. Закрытая задача
останавливает цикл, запаркованная тоже (её ждёт человек), а незакрытая ведёт к
следующему заказу, и строка о конце прохода уходит в журнал утилит .devkit/log.

Воронка это три коротких прохода подряд, не сдвинувших строку; на ней оболочка
встаёт. Трёх ожиданий она воронкой не считает. Задача с видом приёмки mixed или
user стоит в check и ждёт человека, а проход, кончившийся при живых субагентах
сессии, ждёт их конца. В первом случае оболочка встаёт по-хорошему, во втором
заказа не подаёт и ждёт следующего конца хода. Голова просыпается по концу
субагента.

Третье ожидание это машинное событие снаружи сессии: отбитое слияние ждёт
коммита соседа, фоновое дело идёт своим чередом. Такой ход сессия отмечает
командой `agentctl wait` (условие и срок, запись в ~/.devkit/waits на задачу), а
оболочка читает условие с диска и ждёт за неё. Конец процесса и час она
проверяет сама, слияние и закрытие соседней строки спрашивает у
`agentctl wait --check`, а потолок срока берёт из [head] wait_cap профиля
харнеса. Заказа на такой проход она не
подаёт, в воронку и в потолок проходов его не считает и возвращается к работе по
событию либо по сроку. Снимать запись за исполнителя оболочка не берётся,
поэтому помнит отработанную отметку и второй раз по ней не встаёт, а отметку
старше себя самой и отметку с истёкшим сроком не берёт вовсе.

Коды возврата: 0 штатный стоп (задача закрыта, запаркована или ждёт приёмки),
1 стоп оболочки (проходы исчерпаны, воронка, живая голова вышла), 2 ошибка
вызова или окружения, 3 замок занят (голова задачи уже поднята).
"""
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
NOTIFIER = os.path.normpath(os.path.join(HERE, "..", "..", "..", "hooks", "notify.py"))
# Проходов подряд, после которых оболочка встаёт сама. Потолок нужен не от
# осторожности: голова, которая выходит и не двигает строку, крутила бы цикл до
# конца квоты, а разбирать такое человеку надо со свежей задачей, а не с сотней
# проходов позади.
PASS_LIMIT = 6
# Пустой проход это выход головы без движения строки и быстрее этого срока:
# сессия, упавшая на подъёме (нет логина, кончилась квота, чужой флаг), уходит
# за секунды, и долбить в неё шесть раз незачем.
IDLE_SECONDS = 90
IDLE_LIMIT = 3
# Пауза между проходами: обрыв на стороне API её переживает, а человек за
# экраном успевает прочесть строку о выходе головы.
PASS_PAUSE = 10
PAUSE_ENV = "DEVKIT_TASK_PASS_PAUSE"
WATCH_ENV = "DEVKIT_TASK_WATCH_STEP"
SESSION_ENV = "DEVKIT_TASK_SESSION_WAIT"
MUTE_ENV = "DEVKIT_TASK_MUTE"
STAMP = "%Y-%m-%dT%H:%M:%S"
# Статусы, на которых цикл кончается сам. Архив это закрытая задача, blocked
# парковка: и в том, и в другом случае поднимать голову больше не за чем.
DONE = "архиве"
PARKED = "blocked"
# Вид приёмки из пометки, которую taskctl show печатает строке в check («код
# слит, вид mixed, строка не двигалась 0 дней»). Пометка идёт с отступа, а сама
# строка доски с решётки таблицы, и слово «вид» из чужого заголовка сюда не
# попадёт. До DK-870 оболочка искала тут слово «пользовательск», которого show
# не печатает с DK-298, и ждущую человека задачу не узнавала вовсе.
CHECK_KIND = re.compile(r"\bвид\s+(\w+)")
# Виды приёмки, на которых остаток сценария за человеком.
USER_KINDS = ("mixed", "user")

# Машинные журналы дома. Реестр чатов называет сессию окна, отметки хода
# говорят, чем ход кончился. Оба общие на машину, и доска у них не одна.
HOME_DIR = os.path.join(os.path.expanduser("~"), ".devkit")
SESSIONS_LOG = os.path.join(HOME_DIR, "sessions.log")
TURNS_LOG = os.path.join(HOME_DIR, "turns.log")
# Второй канал вопроса: журнал уведомителя. Отметку хода пишет хук turn-mark.py,
# и сессия, поднятая до того, как он лёг в настройки харнеса, не пишет их вовсе.
# Так простоял двенадцать минут конвейер DK-724. Вопрос разрешения был, а
# отметки не было. Уведомитель на том же событии сработал и строку записал,
# поэтому оболочка читает оба журнала, а вопросом считает первое, что придёт.
NOTIFY_LOG = os.path.join(HOME_DIR, "notify.log")
# Отметки машинного ожидания. Их пишет agentctl wait, по файлу на задачу
# (tools/agentctl/wait.go), и имена полей у записи с оболочкой общие. Каталог
# подменяется тем же ключом, каким подменяет его себе сама утилита: настоящий
# общий на машину, и стенду в нём не место.
WAITS_DIR = os.path.join(HOME_DIR, "waits")
WAITS_ENV = "DEVKIT_WAIT_DIR"
# Виды условия. Голый срок ждёт только время, остальные смотрят на цель:
# появление пути, его пропажу, чистоту рабочего дерева. Незнакомый вид оболочка
# не выдумывает, а называет вслух и ожидание не берёт: ждать вслепую значит
# встать до срока неизвестно на чём.
WAIT_TIMER = "срок"
WAIT_HERE = "есть"
WAIT_GONE = "нет"
WAIT_CLEAN = "чисто"
WAIT_MERGED = "слита"
WAIT_CLOSED = "закрыта"
WAIT_PROC = "процесс"
WAIT_HOUR = "час"
WAIT_KINDS = (WAIT_TIMER, WAIT_HERE, WAIT_GONE, WAIT_CLEAN,
              WAIT_MERGED, WAIT_CLOSED, WAIT_PROC, WAIT_HOUR)
# Условия, которые оболочка спрашивает у `agentctl wait --check`. Признак
# «слита» лежит в общем go-модуле (internal/merged), и его копия на python
# разошлась бы с оригиналом на первой же правке. Выход 0 значит, что событие
# пришло, 1 что ещё нет, прочее что проверить нечем.
WAIT_ASKED = (WAIT_MERGED, WAIT_CLOSED)
WAIT_ASK_LIMIT = 60
# Потолок ожидания читается из ключа wait_cap секции [head] профиля харнеса,
# того же, откуда его берёт agentctl wait. Проверка стоит с обеих сторон: файл
# кладёт утилита, но читает его оболочка, и запись с длинным сроком подвесила
# бы окно на полдня. Профилю без ключа достаётся умолчание.
WAIT_CAP_KEY = "wait_cap"
WAIT_CAP_DEFAULT = "2h"
WAIT_STAMP = "%Y-%m-%dT%H:%M:%S"
# Исходы ожидания: чем оно кончилось. Первые два ведут к заказу, третий отдаёт
# ход чужой реплике, четвёртый это стоп конвейера.
WAIT_EVENT = "событие"
WAIT_OVER = "срок вышел"
WAIT_TAKEN = "ход начат"
WAIT_DEAD = "голова вышла"

# Реестр субагентов сессии. Его пишет сторож hooks/agent-watch.py, по файлу на
# сессию. Каталог подменяется тем же ключом, каким подменяет его себе сам хук.
# Иначе стенд читал бы машинный реестр живых сессий.
AGENTS_DIR = os.path.join(HOME_DIR, "agents")
AGENTS_ENV = "DEVKIT_AGENT_WATCH_DIR"
AGENT_RUNNING = "running"
# Разряд записи реестра. Считаются тут только субагенты: фоновая команда
# оболочки тоже лежит в реестре (DK-571), но конца её голова не дождётся, и
# проход, отложенный ради такой записи, встал бы навсегда.
AGENT_SUBAGENT = "subagent"
# Журнал отметок читается тем же ключом, каким его подменяет себе сам хук.
# Стенд, разведший записи хука по временной директории, обязан развести и
# чтение. Иначе оболочка смотрела бы в общий машинный журнал.
TURNS_ENV = "DEVKIT_TURN_MARK_LOG"
# Ключевые слова строк обоих журналов. Значение поля собирается до следующего
# слова, и поэтому пробел в пути дерева строку не рассыпает.
SESSION_KEYS = ("сессия", "задача", "проект", "дерево", "транскрипт", "источник",
                "повод", "tmux", "панель", "родитель")
TURN_KEYS = ("сессия", "ход", "повод", "дерево")
# Ключевые слова строки журнала уведомителя. Оболочке нужны два первых, но
# перечень обязан назвать и остальные. Значение поля собирается до следующего
# ключа, и без слова «уровень» повод забрал бы себе весь хвост строки.
NOTE_KEYS = ("сессия", "повод", "уровень", "бэкенд", "цель", "задача", "проект")
# Слова хода из отметки: чем ход кончился и что он начался.
TURN_OVER = ("кончен", "упал")
TURN_STARTED = "начат"
TURN_WAITS = "ждёт"
# Поводы вопроса, на которых сессия стоит намертво. Ожидание ввода (idle_prompt)
# сюда не идёт. Харнес присылает его через минуту после конца хода, и это
# штатная тишина, а не остановка.
STANDING = ("permission_prompt", "agent_needs_input", "elicitation_dialog")
# Окно, в котором две записи считаются рассказом об одном и том же вопросе.
# Отметка хода и строка уведомителя ложатся в свои журналы на одном событии
# харнеса и расходятся секундой, а два разных вопроса разделены ответом
# человека. Считается окно по времени самого события из строки журнала, не по
# часам оболочки: ключ по одной причине и часам придержал бы второй вопрос той
# же причины, а его строка к тому времени уже вычитана, и человек не узнал бы о
# нём никогда.
SAME_QUESTION = 5
# Метка печатной сессии для рубежа синхронности. Живой голове она не ставится.
# Окно у сессии есть, фоновый ребёнок переживает конец хода, и отказ рубежа
# стоил бы конвейеру как раз того, ради чего живая голова заводилась.
HEADLESS_ENV = "DEVKIT_HEADLESS"
TMUX_ENV = "DEVKIT_TMUX"
# Как часто оболочка заглядывает в журнал отметок. Реже держать незачем, файл
# крошечный, чаще незачем тоже. Между проходами и так стоит пауза.
WATCH_STEP = 3
# Сколько ждать записи реестра про поднятую сессию. Хук старта пишет её первой
# же секундой, и минуты тут с запасом на холодный старт клиента.
SESSION_WAIT = 180
# Молчание живой сессии. Ни одной отметки за этот срок. Долгий ход столько
# молчать не может, отметка «начат» приходит сразу за заказом. Поэтому такая
# тишина значит неподключённую отметку хода либо замёрзшее окно.
MUTE_SECONDS = 1800
# Пауза между текстом реплики и переводом строки при подаче в живой процесс.
# Клиент читает ввод построчно и рисует его в поле; Enter, пришедший в том же
# пакете, обгоняет отрисовку, и в поле остаётся половина заказа (chats.go).
SEND_PAUSE = 0.25

# Замок головы задачи (DK-931). Команда подъёма кладёт в него свой pid и
# называет его этой переменной, оболочка вписывает свой. Замок без pid моложе
# LOCK_YOUNG секунд ещё занят: каталог и pid пишутся двумя шагами.
LOCK_FROM_ENV = "DEVKIT_TASK_LOCK_FROM"
LOCK_YOUNG = 5
BUSY = 3
# Профиль харнеса, из которого берётся клиент, когда хвоста после `--` нет.
HARNESS_DIR = os.path.normpath(os.path.join(HERE, "..", "..", "harness"))
HARNESS_ENV = "DEVKIT_HARNESS"
DEFAULT_HARNESS = "claude-code"
TURN_EXIT = "exit"

USAGE = __doc__


def die(text, code=2):
    sys.stderr.write("task-run: %s\n" % text)
    sys.exit(code)


def head_keys(path):
    """Секция [head] профиля харнеса. Значения там строки и массивы строк в
    двойных кавычках, их запись совпадает с JSON, и разбор идёт им."""
    keys, sect = {}, ""
    with open(path, encoding="utf-8") as f:
        for n, raw in enumerate(f, 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("[") and line.endswith("]") and "=" not in line:
                sect = line[1:-1].strip()
                continue
            if sect != "head" or "=" not in line:
                continue
            key, val = line.split("=", 1)
            try:
                keys[key.strip()] = json.loads(val.strip())
            except ValueError:
                die("профиль %s:%d: в [head] ключ %s не разобран" % (path, n, key.strip()))
    return keys


def profile_client(name=None):
    """Клиент головы из профиля харнеса и признак печатной череды."""
    name = name or (os.environ.get(HARNESS_ENV) or "").strip() or DEFAULT_HARNESS
    path = os.path.join(HARNESS_DIR, name + ".toml")
    if not os.path.isfile(path):
        die("клиент не назван: хвоста после -- нет, а профиля харнеса %s нет" % path)
    keys = head_keys(path)
    client = keys.get("client")
    if not isinstance(client, list) or not client or not all(isinstance(c, str) for c in client):
        die("клиент не назван: в профиле %s нет [head] client массивом строк" % path)
    return client, keys.get("turn_end") == TURN_EXIT


def parse_span(text):
    """Срок в секундах из записи вида 90s, 10m, 2h, 1h30m, той же, какой его
    пишет agentctl. None значит, что запись не разобрана или срок нулевой."""
    got = re.fullmatch(r"(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?", (text or "").strip())
    if not got or not any(got.groups()):
        return None
    h, m, s = (int(g or 0) for g in got.groups())
    return h * 3600 + m * 60 + s or None


def profile_cap(name=None):
    """Потолок срока ожидания в секундах и место, откуда он взят. Профиль
    выбирается так же, как у клиента головы: DEVKIT_HARNESS, иначе
    claude-code, и так же его выбирает agentctl wait. Кривое значение
    останавливает оболочку: молча подменённый потолок неотличим от
    настоящего."""
    name = name or (os.environ.get(HARNESS_ENV) or "").strip() or DEFAULT_HARNESS
    path = os.path.join(HARNESS_DIR, name + ".toml")
    default = parse_span(WAIT_CAP_DEFAULT)
    if not os.path.isfile(path):
        return default, "умолчание %s, профиля %s нет" % (WAIT_CAP_DEFAULT, path)
    raw = head_keys(path).get(WAIT_CAP_KEY)
    if raw is None:
        return default, "умолчание %s, в профиле %s нет [head] %s" % (WAIT_CAP_DEFAULT, path, WAIT_CAP_KEY)
    span = parse_span(raw) if isinstance(raw, str) else None
    if not span:
        die("профиль %s: [head] %s = %r не разобран, жду вид 90m, 2h" % (path, WAIT_CAP_KEY, raw))
    return span, "%s из профиля %s" % (raw, path)


def pid_gone(target):
    """Кончился ли процесс. Сигнал 0 проверяет только наличие, отказ в
    правах значит чужой, но живой процесс. Зомби сигнал тоже принимает, хотя
    процесс уже вышел и ждёт, чтобы родитель его прибрал, поэтому состояние
    спрашивается у ps. Цель не числом событием не считается: ждать тогда
    нечего, и ожидание кончится сроком."""
    try:
        pid = int(target)
    except (TypeError, ValueError):
        return False
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return True
    except PermissionError:
        pass
    except OSError:
        return False
    try:
        p = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)],
                           capture_output=True, text=True)
    except OSError:
        return False
    return p.stdout.strip().startswith("Z")


def lock_owner(pidfile):
    try:
        with open(pidfile, encoding="utf-8") as f:
            return f.read().strip()
    except OSError:
        return ""


def pid_alive(owner):
    """Жив ли процесс с pid из замка. Чужой процесс без права на сигнал жив."""
    if not owner.isdigit():
        return False
    try:
        os.kill(int(owner), 0)
    except PermissionError:
        return True
    except OSError:
        return False
    return True


def young(path):
    try:
        return time.time() - os.stat(path).st_mtime < LOCK_YOUNG
    except OSError:
        return False


def parse_args(argv):
    """Разбор своими руками, а не argparse: хвост после `--` это чужая команда
    целиком, включая её собственные `--`, и отдавать её разбор библиотеке
    нельзя."""
    if "--" in argv:
        cut = argv.index("--")
        head, client = argv[:cut], argv[cut + 1:]
    else:
        head, client = argv, []
    if not head or head[0].startswith("-"):
        die(USAGE)
    opts = {"id": head[0], "proj": os.getcwd(), "order": "", "again": "",
            "passes": PASS_LIMIT, "project": "", "client": client, "headless": False}
    rest = head[1:]
    keys = {"-C": "proj", "--order": "order", "--again": "again",
            "--project": "project", "--passes": "passes"}
    while rest:
        key = rest[0]
        if key == "--headless":
            # Запасной вход называется словом, а не выводится из окружения.
            # Машина с tmux бывает нужна и для печатной череды, а гадать про
            # намерение зовущего оболочке не по чину.
            opts["headless"] = True
            rest = rest[1:]
            continue
        if key not in keys:
            die("неизвестный ключ %s\n\n%s" % (key, USAGE))
        if len(rest) < 2:
            die("ключу %s нужно значение" % key)
        opts[keys[key]] = rest[1]
        rest = rest[2:]
    try:
        opts["passes"] = max(1, int(opts["passes"]))
    except ValueError:
        die("--passes ждёт число, а пришло %s" % opts["passes"])
    if not opts["order"]:
        opts["order"] = "Продолжай выполнение " + opts["id"]
    if not opts["again"]:
        opts["again"] = opts["order"]
    if not opts["client"]:
        opts["client"], exit_only = profile_client()
        if exit_only:
            opts["headless"] = True
    opts["proj"] = os.path.abspath(opts["proj"])
    if not os.path.isdir(opts["proj"]):
        die("корень проекта %s не каталог" % opts["proj"])
    if not opts["project"]:
        opts["project"] = os.path.basename(opts["proj"])
    return opts


class Pipeline:
    def __init__(self, opts):
        self.id = opts["id"]
        self.proj = opts["proj"]
        self.project = opts["project"]
        self.order = opts["order"]
        self.again = opts["again"]
        self.passes = opts["passes"]
        self.client = opts["client"]
        self.headless = opts["headless"]
        # Имя окна везёт та же переменная, которой поднятая сессия называет себя
        # в реестре: своего имени у оболочки нет, она живёт внутри этого окна.
        self.name = os.environ.get(TMUX_ENV, "").strip()
        self.sess = self.name or "без окна"
        # Живая голова: процесс клиента, ID его сессии, память прочитанных
        # строк обоих журналов и очередь снятых отметок.
        self.head = None
        self.sid = ""
        self.turns_seen = {}
        self.sess_seen = {}
        self.note_seen = {}
        self.pending = []
        # Старт оболочки и отметка ожидания, по которой она уже ждала: отметку
        # свою пишет исполнитель, а снимать её за него оболочка не берётся,
        # поэтому отработанное она помнит сама.
        self.started = time.time()
        self.wait_seen = None
        # Потолок срока ожидания из профиля харнеса и условие, про которое
        # оболочка уже сказала, что проверить его нечем: слепоту она называет
        # один раз на отметку, а не на каждом шаге ожидания.
        self.wait_cap, self.wait_cap_from = profile_cap()
        self.wait_blind = None
        # Время события, о котором оболочка звала человека, по причине вопроса.
        # Один вопрос приходит двумя каналами, и второй зов о нём человеку не
        # нужен. Конец и начало хода память чистят.
        self.said_stuck = {}
        self.lock = ""

    # -- замок --------------------------------------------------------------

    def take_lock(self):
        """Замок на задачу до разговора с доской. Вторая голова по той же
        задаче заказывала бы проходы поверх первой, и занятый живым владельцем
        замок это стоп кодом 3."""
        lock = os.path.join(HOME_DIR, "task-%s.lock" % self.id.upper())
        pidfile = os.path.join(lock, "pid")
        handed = (os.environ.get(LOCK_FROM_ENV) or "").strip()
        os.makedirs(HOME_DIR, exist_ok=True)
        took = False
        for _ in range(2):
            try:
                os.mkdir(lock)
                took = True
                break
            except FileExistsError:
                owner = lock_owner(pidfile)
                if handed and owner == handed:
                    took = True
                    break
                if pid_alive(owner) or (not owner and young(lock)):
                    die("голова %s уже поднята: замок %s держит pid %s"
                        % (self.id, lock, owner or "без записи"), BUSY)
                shutil.rmtree(lock, ignore_errors=True)
        if not took:
            die("голова %s уже поднимается: замок %s перехвачен" % (self.id, lock), BUSY)
        with open(pidfile + ".tmp", "w", encoding="utf-8") as f:
            f.write("%d\n" % os.getpid())
        os.replace(pidfile + ".tmp", pidfile)
        self.lock = lock

    def drop_lock(self):
        """Снять свой замок на выходе. Чужой, перехваченный после ребута, не
        трогается."""
        if self.lock and lock_owner(os.path.join(self.lock, "pid")) == str(os.getpid()):
            shutil.rmtree(self.lock, ignore_errors=True)

    # -- состояние доски ----------------------------------------------------

    def show(self):
        """Строка задачи словами taskctl. Пусто значит, что спросить не вышло, и
        такой ответ разбирается наверху отдельно: слепая оболочка не должна
        поднимать голову вслепую."""
        try:
            p = subprocess.run(["taskctl", "show", self.id], cwd=self.proj,
                               capture_output=True, text=True)
        except OSError:
            return ""
        if p.returncode != 0:
            return ""
        return p.stdout

    def status(self, said):
        """Секция строки из первого слова после «в». Формат этот taskctl печатает
        человеку («DK-691 в in-progress», «DK-678 в архиве (закрыта ...)»), и
        другого машинного ответа про секцию у него нет."""
        first = (said or "").split("\n")[0].strip()
        mark = " в "
        if mark not in first:
            return ""
        return first.split(mark, 1)[1].split()[0].strip()

    def waits_user(self, said, sect):
        """Проверенная задача с пользовательской приёмкой: закрывает её человек
        с экрана, и голова тут ничего не сделает. Вид приёмки лежит в пометке
        строки в check, а её show печатает с отступа."""
        if sect != "check":
            return False
        for line in (said or "").split("\n"):
            if not line.startswith((" ", "\t")):
                continue
            got = CHECK_KIND.search(line)
            if got:
                return got.group(1) in USER_KINDS
        return False

    # -- голос наружу -------------------------------------------------------

    def say(self, msg):
        if self.head is not None:
            # Панель окна принадлежит живой голове. Её TUI рисуется в этом же
            # терминале, и строка оболочки поверх него испортила бы экран.
            # Голос тогда уходит в журнал утилит, туда же, где лежит остальной
            # след конвейера.
            self.log(msg, 0)
            return
        sys.stdout.write("%s task-run %s: %s\n" % (time.strftime(STAMP), self.id, msg))
        sys.stdout.flush()

    def log(self, text, code):
        """Строка в журнал утилит .devkit/log, тем же форматом, каким пишут туда
        taskctl и agentctl. Естественный выход головы до этой задачи не писал
        никто: 39 строк «agentctl exec 0» с 26.08 не отличались друг от друга и
        не говорили ни про задачу, ни про её статус."""
        dirp = os.path.join(self.proj, ".devkit")
        if not os.path.isdir(dirp):
            return
        try:
            with open(os.path.join(dirp, "log"), "a", encoding="utf-8") as f:
                f.write("%s\ttask-run\t%s\t%d\n" % (time.strftime(STAMP), text, code))
        except OSError as e:
            self.say("строка журнала не записана (%s)" % e)

    def shout(self, reason, title, text):
        """Позвать человека уведомителем. Стоп конвейера молчать не должен: окно
        закрылось, а с экрана дашборда это неотличимо от штатного конца."""
        if not os.path.isfile(NOTIFIER):
            self.say("уведомитель не нашёлся (%s), стоп остался без строки в ленте" % NOTIFIER)
            return
        cmd = ["python3", NOTIFIER, "--reason", reason, "--task", self.id,
               "--project", self.project, title, text]
        try:
            subprocess.run(cmd, cwd=self.proj, capture_output=True, text=True)
        except OSError as e:
            self.say("уведомление не отправлено (%s)" % e)

    def stop(self, code, sect, why, reason="", loud=True):
        self.say("стоп: %s (задача в %s)" % (why, sect or "неизвестно где"))
        self.drop_head()
        self.log("конвейер %s встал: %s, %s в %s" % (self.sess, why, self.id, sect or "неизвестно где"), code)
        if loud:
            self.shout(reason or "run_stop",
                       "%s: %s конвейер встал" % (self.project, self.id), why)
        sys.exit(code)

    # -- живая голова -------------------------------------------------------

    def log_path(self, env_name, fallback):
        """Журнал дома с подменой на стенде: настоящие ~/.devkit/turns.log и
        sessions.log общие на машину, и самопроверке в них не место."""
        return (os.environ.get(env_name) or "").strip() or fallback

    def live_ready(self):
        """Годится ли окружение под живую голову. Нет tmux, нет имени окна или
        позвали с флагом, значит идём печатной чередой, как шли."""
        if self.headless or not self.name:
            return False
        try:
            p = subprocess.run(["tmux", "has-session", "-t", "=" + self.name],
                               capture_output=True, text=True)
        except OSError:
            return False
        return p.returncode == 0

    def fresh(self, path, seen, keep):
        """Свои строки журнала, которых оболочка ещё не видела, и пополненная
        память. Возврат (строки, память). `keep` отбирает свои строки.

        Позиции в файле тут нет нарочно. Журналы хуков общие на машину и
        режутся по размеру (`hookio.append_capped`), а после реза смещение
        прошлого чтения указывает в середину чужой строки. Оболочка тогда
        читает файл заново и принимает свою старую отметку за конец текущего
        прохода. Поэтому строка узнаётся по себе самой.

        Память держит только свои строки и не забывает их. Чужих на машине
        тысячи, своих за весь конвейер набирается десяток, и памяти на них
        уходит меньше килобайта. Сжимать её до того, что лежит в файле, нельзя:
        рез это перезапись файла целиком, чтение попадает на пустую середину, и
        сжатая по такому чтению память вернула бы прочитанное второй раз.

        Помнится не строка, а сколько раз она встречалась. Время харнес пишет до
        секунды, и два одинаковых слова хода в одну секунду дают две дословно
        одинаковых строки: конец мгновенно отбитого хода приходит в ту же
        секунду, что его начало. Память множеством считала бы такую пару одной
        отметкой и теряла бы вторую.

        Строка без перевода строки на конце недописана: чтение попало на
        середину записи. Такой обрывок пропускается и дочитывается следующим
        заходом.

        Нечитаемый журнал оставляет память нетронутой."""
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                text = f.read()
        except OSError:
            return [], seen
        lines = [l for l in text.split("\n")[:-1] if l.strip() and keep(l)]
        out, count = [], {}
        for line in lines:
            count[line] = count.get(line, 0) + 1
            if count[line] > seen.get(line, 0):
                out.append(line)
        memo = dict(seen)
        for line, times in count.items():
            memo[line] = max(memo.get(line, 0), times)
        return out, memo

    def window_line(self, line):
        """Своя ли это запись реестра чатов. Своя та, что называет наше окно:
        имя окна пишет только клиент со своим терминалом (DK-673)."""
        return self.fields(line, SESSION_KEYS).get("tmux") == self.name

    def own_mark(self, line):
        """Своя ли это отметка хода. Отметку харнес подписывает первыми восемью
        знаками ID сессии, а реестр чатов пишет ID целиком, поэтому сводятся они
        началом строки, а не равенством."""
        sid = self.fields(line, TURN_KEYS).get("сессия", "")
        return bool(self.sid) and bool(sid) and self.sid.startswith(sid)

    def own_note(self, line):
        """Своя ли это строка журнала уведомителя. Сессию он подписывает так же,
        как харнес отметку хода, первыми восемью знаками."""
        sid = self.fields(line, NOTE_KEYS).get("сессия", "")
        return bool(self.sid) and bool(sid) and self.sid.startswith(sid)

    def when(self, line):
        """Время события в секундах: первым словом строки журнала его пишут и
        хуки, и `agentctl wait` в полях отметки. Пусто значит, что время не
        разобралось, и вопрос тогда считается новым: промолчать тут дороже, чем
        позвать второй раз."""
        head = (line or "").split(" ", 1)[0]
        try:
            return time.mktime(time.strptime(head, STAMP))
        except ValueError:
            return None

    def fields(self, line, keys):
        """Строка журнала парами «ключ значение». Значение собирается до
        следующего ключевого слова, и поэтому пробел в пути строку не рассыпает."""
        out, key = {}, None
        for word in line.split()[1:]:
            if word in keys:
                key, out[key] = word, ""
            elif key:
                out[key] = (out[key] + " " + word).strip()
        return out

    def head_alive(self):
        return self.head is not None and self.head.poll() is None

    def raise_head(self, order):
        """Подъём живой головы в своей же панели. Клиент забирает терминал
        окна и рисует в нём свой TUI, оболочка остаётся его родителем и дальше
        только смотрит за отметками. Заказ едет первым аргументом. Интерактивный
        клиент берёт его как первый вопрос и остаётся стоять."""
        env = dict(os.environ)
        # Живой сессии метка печатного режима не ставится. Окно у неё есть,
        # фоновый ребёнок переживает конец хода, и рубеж синхронности отбивал бы
        # ей как раз то, ради чего она заведена (DK-678, DK-724).
        env.pop(HEADLESS_ENV, None)
        cmd = list(self.client) + [order]
        # Память реестра набирается до подъёма. Прошлый запуск конвейера
        # поднимал окно под тем же именем task-<ID>, и его запись оболочка
        # приняла бы за свою.
        _, self.sess_seen = self.fresh(SESSIONS_LOG, {}, self.window_line)
        self.say("живая голова поднята: %s" % " ".join(shlex.quote(c) for c in cmd[:-1]))
        try:
            self.head = subprocess.Popen(cmd, cwd=self.proj, env=env)
        except OSError as e:
            die("клиент не поднялся (%s): %s" % (e, " ".join(self.client)))

    def wait_session(self):
        """ID сессии живой головы из реестра чатов. Пустая строка значит, что
        реестр промолчал. Имя окна пишет только клиент со своим терминалом
        (DK-673), и без записи оболочке не отличить отметки своей сессии от
        отметок соседней."""
        until = time.time() + self.secs(SESSION_ENV, SESSION_WAIT)
        while time.time() < until:
            lines, self.sess_seen = self.fresh(SESSIONS_LOG, self.sess_seen,
                                               self.window_line)
            for line in lines:
                got = self.fields(line, SESSION_KEYS)
                if got.get("сессия", "-") != "-":
                    return got["сессия"]
            if not self.head_alive():
                return ""
            time.sleep(1)
        return ""

    def send_order(self, text):
        """Заказ в живую голову клавиатурой окна, той же дорогой, какой панель
        дашборда подаёт реплику человека. Текст идёт литералом (-l). Иначе tmux
        разобрал бы слова вроде «Enter» как имена клавиш, а многострочный заказ
        едет в скобках вставки. Без них перенос строки читается как Enter, и
        клиент отправляет первую строку, а остальные разбирает отдельными
        репликами."""
        body = "\033[200~" + text + "\033[201~" if "\n" in text else text
        target = "=" + self.name + ":"
        try:
            p = subprocess.run(["tmux", "send-keys", "-t", target, "-l", body],
                               capture_output=True, text=True)
            if p.returncode != 0:
                return False
            time.sleep(SEND_PAUSE)
            p = subprocess.run(["tmux", "send-keys", "-t", target, "Enter"],
                               capture_output=True, text=True)
            return p.returncode == 0
        except OSError:
            return False

    def marks(self):
        """Новые отметки хода своей сессии, снятые с журнала и сложенные в
        очередь. Очередь нужна затем, что заглянуть в отметки приходится и перед
        заказом. Без неё прочитанный на такой заглядке конец хода пропал бы мимо
        ожидания."""
        lines, self.turns_seen = self.fresh(self.log_path(TURNS_ENV, TURNS_LOG),
                                            self.turns_seen, self.own_mark)
        for line in lines:
            got = self.fields(line, TURN_KEYS)
            self.pending.append((got.get("ход", ""), got.get("повод", "-"),
                                 self.when(line)))
        out, self.pending = self.pending, []
        return out

    def notes(self):
        """Поводы своей сессии из журнала уведомителя. Отметка хода тут первая
        по точности, а это запасной канал. Он держится на другом хуке и
        переживает то, чего не переживает первый."""
        lines, self.note_seen = self.fresh(NOTIFY_LOG, self.note_seen, self.own_note)
        return [(self.fields(line, NOTE_KEYS).get("повод", "-"), self.when(line))
                for line in lines]

    def taken(self):
        """Начат ли ход чужим заказом. Реплику человека панель подаёт в это же
        окно (DK-430), и свой заказ поверх неё встал бы второй строкой ввода, а
        сама реплика ушла бы в ход вместе с ним."""
        self.pending = self.marks() + self.pending
        return any(word == TURN_STARTED for word, _, _ in self.pending)

    def busy(self):
        """Виды живых субагентов своей сессии по реестру сторожа
        (hooks/agent-watch.py). Пустой список значит, что фоновой работы нет.
        Нечитаемый реестр тоже считается пустым. Гадать оболочке тут не о чем,
        а долгое молчание головы после такого прохода назовёт mute."""
        if not self.sid:
            return []
        own = (os.environ.get(AGENTS_ENV) or "").strip()
        name = "".join(c for c in self.sid if c.isalnum() or c in "-_")
        path = os.path.join(own or AGENTS_DIR, (name or "unknown") + ".json")
        try:
            with open(path, encoding="utf-8") as f:
                data = json.load(f)
        except (OSError, ValueError):
            return []
        got = data.get("agents") if isinstance(data, dict) else None
        if not isinstance(got, dict):
            return []
        return [str(v.get("type") or "-") for v in got.values()
                if isinstance(v, dict) and v.get("state") == AGENT_RUNNING
                and (v.get("job") or AGENT_SUBAGENT) == AGENT_SUBAGENT]

    def wait_mark(self):
        """Отметка машинного ожидания этого прохода или None. Пишет её
        `agentctl wait` файлом на задачу, а условие и срок оболочка разбирает
        сама: разбирать слова хода ей нечем.

        Чужая отметка сюда не попадает: файл называется ID задачи, и своя у
        конвейера ровно одна. Отработанная отметка отсеивается дважды. Снимать
        за исполнителя запись оболочка не берётся, поэтому помнит ту, по которой
        уже ждала, и второй раз по ней не встаёт. Отметка старше самой оболочки
        осталась от прошлого запуска, а отметка с истёкшим сроком это уже не
        ожидание. Время в полях записи идёт до секунды, и секунда допуска в
        сравнении со стартом оболочки покрывает эту грубость.

        Незнакомое условие ожиданием не считается и называется вслух. Молчание
        тут стоило бы прохода: оболочка ждала бы до срока неизвестно чего. Так
        же оболочка поступает со сроком выше потолка: своей проверкой она
        держит потолок и на записи, положенной мимо утилиты."""
        own = (os.environ.get(WAITS_ENV) or "").strip()
        path = os.path.join(own or WAITS_DIR, self.id + ".json")
        try:
            with open(path, encoding="utf-8") as f:
                mark = json.load(f)
        except (OSError, ValueError):
            return None
        if not isinstance(mark, dict):
            return None
        since, until = self.when(mark.get("since")), self.when(mark.get("until"))
        if since is None or until is None or since < self.started - 1 or until <= time.time():
            return None
        seen = (since, until, mark.get("kind"), mark.get("target"))
        if seen == self.wait_seen:
            return None
        if until - since > self.wait_cap:
            self.say("отметка ожидания со сроком больше потолка (%s), проход "
                     "считается обычным" % mark.get("until"))
            self.log("отметка ожидания %s со сроком %s больше потолка %d с (%s), %s"
                     % (path, mark.get("until"), self.wait_cap, self.wait_cap_from, self.id), 0)
            return None
        if mark.get("kind") not in WAIT_KINDS:
            self.say("отметка ожидания с незнакомым условием (%s), проход считается "
                     "обычным" % mark.get("kind"))
            self.log("отметка ожидания %s с незнакомым условием %s, %s"
                     % (path, mark.get("kind"), self.id), 0)
            return None
        self.wait_seen = seen
        return mark

    def wait_what(self, mark):
        """Условие ожидания словами, для журнала и панели."""
        out = str(mark.get("kind") or "-")
        if mark.get("target"):
            out += " " + str(mark["target"])
        if mark.get("note"):
            out += " (%s)" % mark["note"]
        return out

    def wait_done(self, mark):
        """Пришло ли событие, которого ждёт отметка. Голый срок события не
        имеет и кончается временем."""
        kind, target = mark.get("kind"), str(mark.get("target") or "")
        if kind == WAIT_HERE:
            return os.path.exists(target)
        if kind == WAIT_GONE:
            return not os.path.exists(target)
        if kind == WAIT_CLEAN:
            return self.tree_clean(target)
        if kind == WAIT_PROC:
            return pid_gone(target)
        if kind == WAIT_HOUR:
            at = self.when(target)
            return at is not None and time.time() >= at
        if kind in WAIT_ASKED:
            return self.asked(kind, target)
        return False

    def asked(self, kind, target):
        """Событие «слита» или «закрыта» по ответу `agentctl wait --check`.
        Своей копии признака у оболочки нет, спрашивает она ту же функцию, по
        которой считают утилиты. Выход 0 значит, что событие пришло, 1 что ещё
        нет. Прочее значит, что проверить нечем: agentctl нет в PATH, он старый
        и флага не знает, git не ответил. Такое ожидание кончится сроком, а
        слепота называется вслух один раз на условие."""
        cmd = ["agentctl", "wait", self.id, kind, target, "--check", "-C", self.proj]
        try:
            p = subprocess.run(cmd, capture_output=True, text=True, timeout=WAIT_ASK_LIMIT)
        except (OSError, subprocess.TimeoutExpired) as e:
            why = str(e)
        else:
            if p.returncode in (0, 1):
                return p.returncode == 0
            why = (p.stderr or p.stdout).strip() or "выход %d" % p.returncode
        if self.wait_blind != (kind, target):
            self.wait_blind = (kind, target)
            self.say("условие %s %s проверить нечем (%s), ожидание кончится сроком"
                     % (kind, target, why))
            self.log("условие ожидания %s %s не проверено agentctl: %s, %s"
                     % (kind, target, why, self.id), 0)
        return False

    def tree_clean(self, path):
        """Нет ли в рабочем дереве незакоммиченного. Так ждут соседа: слияние
        отбивается грязной доской, и ждать приходится его коммита (DK-893).
        Дерево, про которое git ничего не сказал, чистым не считается: ошибка
        тут значит не чистоту, а неизвестность, и ожидание кончится сроком."""
        try:
            p = subprocess.run(["git", "-C", path, "status", "--porcelain"],
                               capture_output=True, text=True)
        except OSError:
            return False
        return p.returncode == 0 and not p.stdout.strip()

    def held(self, n, after):
        """Ожидание вместо холостого прохода. Возврат это слово исхода или
        пустая строка, когда отметки не было и проход обычный.

        Ход, кончившийся ожиданием машинного события, до DK-899 считался
        холостым наравне с головой, вылетевшей на подъёме: воронка снимала окно
        на третьем таком проходе вместе с работой (DK-893 и DK-510 за одну
        ночь). Разницу говорит сама сессия отметкой, а оболочка ждёт за неё:
        заказа на такой проход не подаёт, в воронку и в потолок его не считает
        и возвращается к работе по событию либо по сроку."""
        mark = self.wait_mark()
        if not mark:
            return ""
        what = self.wait_what(mark)
        self.log("проход %d кончился ожиданием (%s), срок до %s, %s в %s: заказ "
                 "не подаётся, в потолок проходов проход не идёт"
                 % (n, what, mark.get("until"), self.id, after), 0)
        self.say("проход %d ждёт: %s, срок до %s" % (n, what, mark.get("until")))
        why = self.hold(mark)
        self.log("ожидание прохода %d кончилось (%s): %s, %s"
                 % (n, why, what, self.id), 0)
        self.say("ожидание прохода %d кончилось (%s)" % (n, why))
        if why == WAIT_DEAD:
            self.stop(1, after, "живая голова вышла в ожидании (%s)" % what)
        return why

    def hold(self, mark):
        """Ждать события или срока. Живая голова при этом стоит в своём окне и
        никуда не девается, а реплика человека обрывает ожидание: заказ поверх
        начатого хода оболочка не шлёт."""
        until = self.when(mark.get("until"))
        while True:
            if self.wait_done(mark):
                return WAIT_EVENT
            if until is None or time.time() >= until:
                return WAIT_OVER
            if self.head is not None:
                if not self.head_alive():
                    return WAIT_DEAD
                if self.taken():
                    return WAIT_TAKEN
            time.sleep(self.secs(WATCH_ENV, WATCH_STEP))

    def wait_turn(self, n):
        """Ждать конца прохода. Возврат это слово отметки («кончен», «упал») или
        пустая строка, когда живая голова вышла раньше."""
        started, mute, said = time.time(), time.time(), False
        while True:
            # Запасной канал читается первым. Конец хода в той же пачке
            # отметок обрывает разбор возвратом, и строка уведомителя досталась
            # бы следующему проходу, у которого память зова уже чиста.
            for why, when in self.notes():
                # Отметки хода у сессии может не быть вовсе, а вопрос она
                # задала. Значит, человека зовём по строке уведомителя. Тишину
                # журнала это не отменяет, её называет mute.
                if why in STANDING:
                    self.stuck(n, why, when)
            got = self.marks()
            for i, (word, why, when) in enumerate(got):
                mute = time.time()
                if word in TURN_OVER:
                    # Хвост пачки кладётся обратно в очередь. За концом хода в
                    # ней уже может лежать начало следующего, и потерять его
                    # значило бы послать заказ поверх чужого хода.
                    self.pending = got[i + 1:] + self.pending
                    self.said_stuck = {}
                    return word
                if word == TURN_STARTED:
                    # Заказ дошёл до сессии, свой или человека. Оболочке тут
                    # разницы нет, ход пошёл в обоих случаях. Прошлый вопрос
                    # закрыт: ход пошёл дальше, и придержание зова снимается.
                    said = False
                    self.said_stuck = {}
                if word == TURN_WAITS and why in STANDING:
                    self.stuck(n, why, when)
            if not self.head_alive():
                # Отметка конца хода и выход клиента приходят почти разом, и
                # порядок их не обещан. Последние строки журнала дочитываются
                # перед тем, как назвать проход оборванным.
                for word, _, _ in self.marks():
                    if word in TURN_OVER:
                        return word
                return ""
            if not said and time.time() - mute > self.secs(MUTE_ENV, MUTE_SECONDS):
                self.mute(n, int(time.time() - started))
                said = True
            time.sleep(self.secs(WATCH_ENV, WATCH_STEP))

    def stuck(self, n, why, when=None):
        """Сессия упёрлась в вопрос, на который сама не ответит. Права машинного
        контура покрывают не всё (DK-739), и такая остановка снаружи неотличима
        от долгой работы. Каналов у вопроса два, отметка хода и строка
        уведомителя. Про один и тот же вопрос человеку говорится один раз, а
        второй вопрос той же причины зовёт заново, даже пришедший следом."""
        said = self.said_stuck.get(why)
        if when is not None and said is not None and abs(when - said) <= SAME_QUESTION:
            return
        self.said_stuck[why] = when if when is not None else time.time()
        self.say("проход %d стоит на вопросе сессии (%s), нужен человек" % (n, why))
        self.log("живая голова %s ждёт человека: %s, %s" % (self.sess, why, self.id), 0)
        self.shout("wait_human", "%s: %s ждёт человека" % (self.project, self.id),
                   "сессия конвейера упёрлась в вопрос (%s) и стоит" % why)

    def mute(self, n, spent):
        """Полчаса без единой отметки. Отметка «начат» приходит сразу за
        заказом. Поэтому такая тишина значит неподключённый хук отметки хода
        либо замёрзшее окно, а не долгий ход."""
        self.say("проход %d молчит %d с: ни одной отметки хода" % (n, spent))
        self.log("живая голова %s молчит %d с без отметок хода, %s"
                 % (self.sess, spent, self.id), 0)
        self.shout("run_stop", "%s: %s конвейер молчит" % (self.project, self.id),
                   "живая сессия не отмечает ходы: хук turn-mark.py не подключён "
                   "либо окно замёрзло")

    def drop_head(self):
        """Снять живую голову на выходе оболочки. Окно закрывается вместе с
        нею, и дашборд снова считает задачу свободной. Живая tmux-сессия у него
        и есть признак идущей работы."""
        if not self.head_alive():
            return
        try:
            self.head.terminate()
            self.head.wait(timeout=10)
        except (OSError, subprocess.SubprocessError):
            try:
                self.head.kill()
            except OSError:
                pass

    def run_live(self):
        """Конвейер одной живой сессией: первый заказ аргументом, следующие
        клавиатурой окна, конец прохода отметкой хука."""
        sect = self.preflight()
        self.raise_head(self.order)
        self.sid = self.wait_session()
        if not self.sid:
            # Без ID сессии отметки не свести с окном, и подпинывать голову
            # оболочке нечем. Работу это не отменяет. Живая сессия доводит
            # первый проход сама, а молчание после него будет названо вслух.
            self.say("реестр чатов не назвал сессию окна %s: заказ следующего "
                     "прохода подать некому" % self.sess)
            self.shout("run_stop", "%s: %s конвейер без реестра" % (self.project, self.id),
                       "реестр чатов не назвал сессию окна %s, проходы дальше первого "
                       "не заказываются" % self.sess)
        idle, holds, n, mine = 0, 0, 1, True
        while True:
            begun = time.time()
            word = self.wait_turn(n)
            spent = time.time() - begun
            told = self.show()
            after = self.status(told) or sect
            self.log("конец прохода живой головы %s (%s), %s в %s, проход %d из %d, %d с"
                     % (self.sess, word or "голова вышла", self.id, after, n, self.passes,
                        int(spent)), 0)
            self.say("проход %d кончился (%s) за %d с, задача в %s"
                     % (n, word or "голова вышла", int(spent), after))
            if after == DONE:
                self.stop(0, after, "задача закрыта", loud=False)
            if after == PARKED:
                self.stop(0, after, "задача запаркована и ждёт человека", reason="wait_human")
            if self.waits_user(told, after):
                self.stop(0, after, "задача ждёт приёмки человеком", reason="task_check")
            if not word:
                self.stop(1, after, "живая голова вышла на проходе %d, задача не закрыта" % n)
            # Голова раздала работу фоновым субагентам и ждёт их конца. Ход её
            # при этом короток, строка стоит, и воронка сняла бы окно вместе с
            # субагентами, потеряв их работу (так погиб исполнитель DK-864).
            # Такой проход в счёт не идёт и заказа не получает. Голова
            # просыпается по концу субагента так же, как по реплике человека.
            alive = self.busy()
            if alive:
                self.log("проход %d кончился при живых субагентах (%s), %s: заказ не "
                         "подаётся, ход поднимет их конец"
                         % (n, ", ".join(alive), self.id), 0)
                self.say("проход %d кончился при живых субагентах (%s): заказ не "
                         "подаётся, ход поднимет их конец" % (n, ", ".join(alive)))
                sect = after
                mine = False
                continue
            # Ход кончился ожиданием машинного события. Оболочка ждёт за
            # сессию и проход этот не считает ни воронкой, ни расходом потолка:
            # иначе шесть коротких ожиданий сняли бы окно тем же способом, каким
            # его снимала воронка.
            waited = self.held(n, after)
            if waited == WAIT_TAKEN:
                sect = after
                mine = False
                continue
            if waited:
                holds += 1
            # Воронка. Ход кончается быстро и строку не двигает. Считаются тут
            # только свои проходы, короткая реплика человеку законна, и цикл она
            # останавливать не должна.
            elif mine and after == sect and spent < IDLE_SECONDS:
                idle += 1
                self.say("проход %d прошёл вхолостую (%d с, строка на месте), подряд таких %d из %d"
                         % (n, int(spent), idle, IDLE_LIMIT))
                if idle >= IDLE_LIMIT:
                    self.stop(1, after, "%d прохода подряд вхолостую, конвейер жжёт бюджет" % idle)
            elif mine:
                idle = 0
            sect = after
            if n - holds >= self.passes:
                self.stop(1, after, "проходы исчерпаны (%d), задача не закрыта" % self.passes)
            time.sleep(self.pause())
            if self.taken():
                self.say("заказ прохода %d не послан: ход уже начат репликой человека" % (n + 1))
                mine = False
                continue
            if not self.send_order(self.again):
                self.stop(1, after, "заказ прохода %d не дошёл до окна %s" % (n + 1, self.sess))
            n, mine = n + 1, True

    # -- проход -------------------------------------------------------------

    def run_pass(self, order):
        """Один подъём головы. Вывод клиента идёт в панель как есть, не через
        оболочку: панель сессии читают глазами, и прятать ход работы в буфер
        оболочки значило бы гасить экран на всё время прохода."""
        cmd = list(self.client) + ["-p", order]
        self.say("проход поднят: %s" % " ".join(shlex.quote(c) for c in cmd[:-1]))
        started = time.time()
        try:
            p = subprocess.run(cmd, cwd=self.proj)
        except OSError as e:
            die("клиент не поднялся (%s): %s" % (e, " ".join(self.client)))
        return p.returncode, time.time() - started

    def secs(self, name, default):
        """Срок в секундах с подменой из окружения. Подмена нужна стенду.
        Самопроверка не должна ждать по три минуты там, где проверяется не срок,
        а поведение."""
        try:
            return max(0, int(os.environ.get(name, "")))
        except ValueError:
            return default

    def pause(self):
        return self.secs(PAUSE_ENV, PASS_PAUSE)

    def preflight(self):
        """Строка доски перед заказом прохода. Возврат это секция строки, а
        конец работы отсюда уходит стопом. Слепая оболочка не должна поднимать
        голову вслепую, а закрытой задаче голова не нужна вовсе."""
        said = self.show()
        if not said:
            die("taskctl не сказал про %s ничего: доски тут нет либо строка "
                "пропала, поднимать голову вслепую нельзя" % self.id)
        sect = self.status(said)
        if sect == DONE:
            self.stop(0, sect, "задача закрыта", loud=False)
        if sect == PARKED:
            self.stop(0, sect, "задача запаркована и ждёт человека", reason="wait_human")
        if self.waits_user(said, sect):
            self.stop(0, sect, "задача ждёт приёмки человеком", reason="task_check")
        return sect

    def run(self):
        if self.live_ready():
            return self.run_live()
        return self.run_passes()

    def run_passes(self):
        # Проходы считаются вычетом: ожидание это не проход, и потолок его не
        # съедает. Поэтому череда идёт while, а не for по числу проходов.
        idle, holds, n = 0, 0, 1
        while True:
            sect = self.preflight()
            code, spent = self.run_pass(self.order if n == 1 else self.again)
            after = self.status(self.show()) or sect
            # Строка о выходе головы пишется всегда, а не только на аварии: до
            # этой задачи естественный выход не писал никто, и пропажу окна
            # разбирали по тому, что успел сделать покойник.
            self.log("выход головы конвейера %s, %s в %s, проход %d из %d, %d с"
                     % (self.sess, self.id, after, n, self.passes, int(spent)), code)
            self.say("голова вышла кодом %d за %d с, задача в %s" % (code, int(spent), after))
            if after == DONE:
                self.stop(0, after, "задача закрыта", loud=False)
            if after == PARKED:
                self.stop(0, after, "задача запаркована и ждёт человека", reason="wait_human")
            # Ход кончился ожиданием машинного события. Печатной череде оно
            # нужно тем же, чем живой голове: проход этот не холостой, и потолка
            # он не тратит.
            if self.held(n, after):
                holds += 1
            # Воронка: голова выходит быстро и строку не двигает. Обычно это
            # сломанное окружение (нет логина, кончилась квота, чужой флаг), и
            # шесть попыток тут ничем не лучше трёх.
            elif after == sect and spent < IDLE_SECONDS:
                idle += 1
                self.say("проход %d прошёл вхолостую (%d с, строка на месте), подряд таких %d из %d"
                         % (n, int(spent), idle, IDLE_LIMIT))
                if idle >= IDLE_LIMIT:
                    self.stop(1, after, "%d прохода подряд вхолостую, конвейер жжёт бюджет" % idle)
            else:
                idle = 0
            if n - holds >= self.passes:
                break
            n += 1
            time.sleep(self.pause())
        self.stop(1, self.status(self.show()), "проходы исчерпаны (%d), задача не закрыта" % self.passes)


def main(argv):
    if not argv or argv[0] in ("-h", "--help"):
        sys.stdout.write(USAGE)
        return 0
    pipe = Pipeline(parse_args(argv))
    pipe.take_lock()
    try:
        pipe.run()
    finally:
        pipe.drop_lock()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
