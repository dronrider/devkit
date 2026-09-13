#!/usr/bin/env python3
"""Держатель хода цикла цели: не дать сессии чата уснуть посреди цели.

Цель в живом чате ведёт сама сессия, и правило у неё одно с 19 августа: пока
цель не кончилась стоп-маркером или вопросом человеку, ход не отдаётся. Правило
это проза, и 12 сентября она не удержала дважды за день. Сессия отчиталась о
сделанном, ход отдала, следующей работы не взял никто, и цель простояла ночь
при живой квоте (DK-971). Держатель ставит на то же место машинный рубеж:
конец хода он разбирает сам и решением `block` возвращает сессию в работу.

Кого держать, держатель узнаёт из реестра целей `~/.devkit/goals`: запись туда
кладёт гейт бюджета `agentctl spend --goal`, и в ней названы сессия цикла и его
носитель. Держится только своя сессия с носителем `chat`. Виток оболочки
`goal-run` кончается маркером и ход обязан отдать, соседняя сессия того же
проекта цели не ведёт вовсе.

Ход отдаётся сам, без держателя, в четырёх случаях:

  стоп-маркер   в записи лежит `marker` не `continue`: цикл кончился
                (`agentctl lap --marker done|over|wait-human|stuck`)
  цель не в работе  строка цели ушла с доски, в Check, в Done либо
                припаркована: работы за ней нет
  вопрос        рядом с разговором лежит свежий признак ожидания этой сессии:
                человека спросили и ждут ответа
  воронка       три конца хода подряд с одним и тем же следом цикла: ни новой
                строки журнала, ни новой записи «Журнала». Тогда держатель
                отпускает ход и зовёт человека громко: сессия крутится
                вхолостую, и жечь на это квоту незачем. Число то же, что у
                воронки оболочки

Движением цикла считаются его собственные следы, журнал `.devkit/goal-<ID>.log`
и «Журнал» файла цели, а не правки доски: доску в том же чекауте правят соседние
сессии, и по ней холостой круг неотличим от работы.

Режим один:
  goal-hold.py --hook [протокол]  событие читается со stdin и разбирается по
                                  имени протокола таблицей hookio.py (голый
                                  --hook это claude-code)

Переменные окружения:
  DEVKIT_GOAL_HOLD_OFF=1   ничего не держать: ход отдаётся как без хука
  DEVKIT_GOAL_HOLD_DIR=..  свой каталог реестра целей (стенд, прогон проверки)

Журнал держателя лежит в ~/.devkit/goal-hold.log: по нему разбирается и
удержанный ход, и отпущенный, и причина отпускания.
"""
import json
import os
import re
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

HOME_DIR = os.path.join(os.path.expanduser("~"), ".devkit")
GOALS_DIR = os.path.join(HOME_DIR, "goals")
LOG = os.path.join(HOME_DIR, "goal-hold.log")
NOTIFIER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "notify.py")

OFF_ENV = "DEVKIT_GOAL_HOLD_OFF"
DIR_ENV = "DEVKIT_GOAL_HOLD_DIR"

# Носитель цикла, который держится. Виток оболочки помечен словом «shell» и
# кончается маркером сам.
CHAT = "chat"
# Маркер продолжения: он работу не кончает, и запись с ним держится дальше.
GO_ON = "continue"
# Сколько концов хода подряд с одним и тем же следом цикла считать воронкой.
# Число то же, что у оболочки: три пустых прохода это уже не заминка.
IDLE_LIMIT = 3
# Ключи держателя в записи реестра: счёт холостых кругов и слепок следов цикла,
# по которому холостой круг отличается от рабочего.
IDLE_KEY = "hold_idle"
MARK_KEY = "hold_mark"
# Раздел доски, в котором цель считается идущей.
IN_PROGRESS = "In progress"
BOARD = "docs/TASKS.md"
GOAL_LOG = ".devkit/goal-%s.log"
# Признак ожидания ответа человека лежит рядом со входом разговора задачи
# (LLD DK-430, решение 2). Первой строкой в нём срок либо метка «без срока»,
# ниже поле с сессией, которая ждёт.
CHAT_DIRS = ("chat", "mail")
ASK_GLOB = ".ask"
ASK_SESSION = "сессия:"
ASK_FOREVER = "без срока"
ASK_STAMP = re.compile(r"^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2})?$")
# Повод уведомления о воронке. Слово то же, что у стопов цикла в скилле
# goal-loop: человек читает ленту по поводам.
NOTIFY_REASON = "goal_stop"


def off(env=None):
    env = os.environ if env is None else env
    return bool((env.get(OFF_ENV) or "").strip())


def goals_dir(env=None):
    env = os.environ if env is None else env
    return (env.get(DIR_ENV) or "").strip() or GOALS_DIR


def log_path(env=None):
    return os.path.join(os.path.dirname(goals_dir(env)) or HOME_DIR, "goal-hold.log")


def log(goal, event, text, env=None):
    """Строка машинного журнала. Формат именованный, как у соседних хуков:
    разбирается по ключевым словам, а не по счёту полей."""
    line = "%s цель %s событие %s текст «%s»\n" % (
        time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime()), goal or "-", event, text)
    hookio.append_capped(log_path(env), line)


def read_entry(path):
    """Запись реестра словарём: строки «ключ = значение», решётка комментарий."""
    data = {}
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            text = f.read()
    except OSError:
        return data
    for ln in text.splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#"):
            continue
        key, sep, val = ln.partition("=")
        if sep:
            data[key.strip()] = val.strip()
    return data


def write_entry(path, data):
    """Запись обратно в файл. Порядок ключей тут не держится: своё держатель
    дописывает в хвост, а порядок известных полей знают их писатели, гейт
    бюджета и сторожок."""
    lines = ["%s = %s" % (k, v) for k, v in sorted(data.items()) if v]
    try:
        with open(path, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
    except OSError:
        pass


def entry_of(session, env=None):
    """Запись реестра, которую ведёт эта сессия: (путь, запись). Пустая пара
    значит, что цели за сессией нет и держать нечего."""
    d = goals_dir(env)
    try:
        names = sorted(os.listdir(d))
    except OSError:
        return None, {}
    for name in names:
        if not name.endswith(".watch"):
            continue
        path = os.path.join(d, name)
        data = read_entry(path)
        if data.get("session") and data.get("session") == session:
            return path, data
    return None, {}


def board_section(root, goal):
    """Раздел доски, в котором стоит строка цели. Пустая строка значит, что цели
    на доске нет либо доски нет вовсе. Доска читается напрямую: хук обязан
    работать и тогда, когда бинарей devkit нет в PATH."""
    try:
        with open(os.path.join(root, BOARD), encoding="utf-8", errors="replace") as f:
            text = f.read()
    except OSError:
        return ""
    section = ""
    for ln in text.splitlines():
        if ln.startswith("## "):
            section = ln[3:].strip()
            continue
        if not ln.startswith("|"):
            continue
        cell = ln.split("|")[1].strip() if ln.count("|") > 1 else ""
        if cell == goal:
            return section
    return ""


def asked(root, session):
    """Ждёт ли эта сессия ответа человека: рядом со входом разговора лежит её
    признак ожидания. Вопрос, без которого работа не едет, это законный конец
    хода, и держать такую сессию нельзя."""
    for sub in CHAT_DIRS:
        d = os.path.join(root, ".devkit", sub)
        try:
            names = sorted(os.listdir(d))
        except OSError:
            continue
        for name in names:
            if not name.endswith(ASK_GLOB):
                continue
            try:
                with open(os.path.join(d, name), encoding="utf-8", errors="replace") as f:
                    lines = f.read().split("\n")
            except OSError:
                continue
            first = (lines[0] if lines else "").strip()
            if first != ASK_FOREVER and not ASK_STAMP.match(first):
                continue
            for ln in lines[1:]:
                ln = ln.strip()
                if ln.startswith(ASK_SESSION) and ln[len(ASK_SESSION):].strip() == session:
                    return True
    return False


def trace(root, goal, file_path):
    """Слепок следов цикла: длина журнала цикла и время правки файла цели.
    Сменился слепок, значит круг был рабочим. Доска в слепок не входит: её в том
    же чекауте правят соседние сессии (DK-971)."""
    marks = []
    for path in (os.path.join(root, GOAL_LOG % goal) if goal else "", file_path or ""):
        try:
            st = os.stat(path)
            marks.append("%d.%d" % (st.st_size, st.st_mtime_ns))
        except OSError:
            marks.append("-")
    return ":".join(marks)


def shout(goal, root, turns, call=None):
    """Громкий зов человеку: цикл крутится вхолостую, и держатель его отпустил."""
    call = subprocess.run if call is None else call
    title = "цель %s: цикл встал вхолостую" % goal
    body = ("%d хода подряд кончились одним и тем же следом цикла, ни строки журнала, "
            "ни записи «Журнала»; ход отдан человеку, продолжение решать ему" % turns)
    try:
        call([sys.executable, NOTIFIER, "--reason", NOTIFY_REASON, "--task", goal, title, body],
             cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError:
        pass


def hold_text(goal, root):
    """Что сказано сессии на конце хода. Текст короткий и с выходами: держатель
    возвращает работу, а не ругается."""
    return (
        "Держатель хода devkit: цель %s ведёт эта сессия, и ход не отдаётся.\n"
        "Работа над целью кончается стоп-маркером, а не отчётом: возьми следующую "
        "работу тем же ходом по скиллу goal-loop (`taskctl slot`, полка фона), "
        "а поворот помечай строкой журнала цикла "
        "(`python3 ~/projects/devkit/kit/skills/goal-loop/goal-run.py %s --say \"<что сделано>\"`).\n"
        "Работы нет прямо сейчас, значит ожидание кладётся на диск: короткое "
        "`agentctl wait`, долгое парковкой цели машинной причиной. Без человека "
        "цель дальше не едет, значит "
        "`agentctl lap --goal docs/tasks/%s.md --marker wait-human --note \"<чего ждём>\"` "
        "и вопрос человеку. Корень проекта: %s." % (goal, goal, goal, root))


def decide(entry, path, event, env=None, call=None):
    """Что держатель сделал с концом хода: (держим ли, строка отчёта). Запись
    правится тут же: счёт холостых кругов и слепок следов живут в ней."""
    goal, root = entry.get("goal"), entry.get("root")
    if not goal or not root or not os.path.isdir(root):
        return False, "запись без цели или корня"
    if (entry.get("carrier") or CHAT) != CHAT:
        return False, "цикл ведёт оболочка, ход её"
    marker = entry.get("marker", "")
    if marker and marker != GO_ON:
        return False, "цикл кончился маркером %s" % marker
    section = board_section(root, goal)
    if section and section != IN_PROGRESS:
        return False, "цель стоит в разделе «%s»" % section
    if asked(root, event.session):
        return False, "сессия ждёт ответа человека"
    mark = trace(root, goal, entry.get("file"))
    idle = 0
    try:
        idle = int(entry.get(IDLE_KEY, "0"))
    except ValueError:
        idle = 0
    idle = idle + 1 if mark == entry.get(MARK_KEY) else 1
    entry[IDLE_KEY], entry[MARK_KEY] = str(idle), mark
    if idle >= IDLE_LIMIT:
        entry[IDLE_KEY] = "1"
        write_entry(path, entry)
        shout(goal, root, IDLE_LIMIT, call)
        return False, ("воронка: %d хода подряд с одним следом цикла, зову человека"
                       % IDLE_LIMIT)
    write_entry(path, entry)
    return True, "ход удержан, холостых кругов подряд %d" % idle


def blocked(text, stream=None):
    """Канал держателя: решение block на конце хода. Харнес отдаёт текст модели
    и продолжает ход вместо того, чтобы уснуть."""
    out = sys.stdout if stream is None else stream
    json.dump({"decision": "block", "reason": text}, out, ensure_ascii=False)
    out.write("\n")


def handle(event, env=None, stream=None, call=None):
    """Одно событие конца хода."""
    path, entry = entry_of(event.session, env)
    if not path:
        return 0
    held, why = decide(entry, path, event, env, call)
    log(entry.get("goal"), "держу" if held else "отпускаю", why, env)
    if held:
        blocked(hold_text(entry.get("goal"), entry.get("root")), stream)
    return 0


def run_hook(protocol, env=None, stream=None, call=None):
    if off(env):
        return 0
    event = hookio.agent_event(protocol)
    if event is None or event.kind != hookio.TURN_DONE or not event.session:
        # Чужое событие и событие без ID сессии держателю не годятся: цель
        # находится по сессии, и держать наугад нельзя.
        return 0
    try:
        return handle(event, env, stream, call)
    except OSError:
        return 0


def main(argv):
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("goal-hold: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
