"""Подъём работы, оставшейся без живой сессии (команда lift, DK-1157).

Перезагрузка машины убивает разом все носители: панели tmux, сессии харнеса и
фоновые прогоны. Службы после старта поднимаются сами, а работа нет. Прежние
пути возобновления смотрят на носителя: подъём упавшего хода ищет панель,
страховка ожидания читает признак вопроса, пробуждение ждущих разбирает
Blocked. Строка, которая просто шла в работе, не видна ни одному из них.

Здесь признак другой. Строку выбирает доска: этап означает работу, а живой
сессии за ней нет, либо она молчит дольше рубежа. Молчание в часы при живом
процессе это смерть сессии, но доказывает её лишь молчание, начавшееся на
самом этапе: окно, молчавшее до перевода строки, этап только открыло, и
исполнителя за строкой не видно (DK-1361). Такую строку поднимает обычный
заказ `taskctl run` с репликой «продолжай», по одной и в пределах свободной
ёмкости, а исчерпав попытки, отдаёт строку исходом дежурному записью в задаче
и уведомлением. Тем же заходом снимаются следы умерших сессий: замки задач с
мёртвым владельцем и процессы нагрузки, пережившие своего родителя.

Второй признак это лежащая во входе чата реплика человека (DK-1194). Такую
строку доска не выдаёт ничем: она бывает в любом статусе, в том числе
проверенной с приёмкой человека, и ни обход ждущих, ни подъём сирот её не
берут. Живой файл `.devkit/chat/task-<ID>.in` значит, что реплика лежит
непрочитанной, и это повод для хода головы, а не событие строки: доска тут не
правится, и статус остаётся тем же. Панель зовёт подъём сразу, как только
реплика легла, а этот заход добирает пропущенное: реплику из терминала и
реплику, написанную при мёртвом дашборде.
"""
import collections
import json
import os
import re
import shutil
import subprocess
import time
from datetime import datetime
from pathlib import Path

# Этапы работы из словаря internal/stage: за ними стоит живой исполнитель, и
# строка без сессии на таком этапе это работа, которую никто не ведёт. Этапы
# ожидания («ждёт человека», «ждёт очереди», «ждёт события») сюда не входят
# сознательно: там сессии нет по смыслу, и поднимать нечего.
WORK_STAGES = ("постановка", "разработка", "вычитка", "ревью", "доработка",
               "слияние", "выкат", "проверка")

# Слова taskctl о сессии строки: печатает их stagenote.go, и разбирать их
# заново тут нечем. Живой считается только первая форма.
GONE = "сессии нет"
ALIVE = "сессия жива"

# Молчащая сессия и её возраст в тех же словах: живой процесс, не касавшийся
# реестра дольше рубежа простоя экрана (IdleAfter у peers), доска называет
# молчащей и печатает возраст минутами. Возраст разбирается отсюда: второй
# копии расчёта в python тут не заводится.
SILENT = "сессия молчит"
SILENT_RE = re.compile("^" + SILENT + " (\\d+) ")

# Рубеж молчания, после которого сессия перестаёт считаться живой для подъёма,
# секунды. Молчание в часы при живом процессе это смерть сессии (DK-1361): ход,
# не касавшийся реестра два часа, работу уже не ведёт. Короче нельзя: спокойный
# ход живого клиента тянется часами, и подъём стрелял бы по работающей строке.
# Длиннее тоже нельзя: сессии DK-1317 и DK-1323 стояли мёртвыми 17 часов, прежде
# чем их подняли руками. Два часа закрывают и обеденное окно человека за строкой,
# и инцидент, который сторожок обязан снять сам.
SILENT_AFTER = 2 * 60 * 60

# Возраст замка без признаков жизни, после которого живой владелец считается
# замерзшим (DK-1454). Признак жизни это отметка beat, которой оболочка
# касается на отметке хода и подтверждённом ожидании; живой pid сам по себе
# работой не считается. Одно число с замком internal/taskhead (lockStale) и
# оболочкой kit/skills/board-task/task-run.py (STALE_AFTER) держат сторожа
# пары в тестах обоих.
STALE_AFTER = 2 * 60 * 60

# Имя файла-отметки жизни в замке, то же, что lockBeat у internal/taskhead.
BEAT_NAME = "beat"

# Грация касания к открытию этапа, секунды. Молчание доказывает смерть
# исполнителя, только когда началось на самом этапе: касание реестра за час до
# открытия считается касанием этапа. Окно, молчавшее до перевода строки, этап
# лишь открыло (диспетчер этапом своего окна), исполнителя за строкей не видно,
# и молчание такого окна о его судьбе не говорит: подсобные исполнители
# реестра вовсе не трогают, и по голому рубежу подъём поднимал бы строку,
# которую прямо сейчас ведут.
STAGE_TOUCH_GRACE = 60 * 60

# Секции доски, где строка означает работу: из них берутся и осиротевшие
# строки, и строки с лежащей репликой. Строку Backlog никто не брал, и голова на
# ней шла бы мимо вердикта и перевода в работу; строку Blocked будит ответ на
# вопрос либо обход ждущих. Те же две секции у панели (replySection в
# tools/dashboard/chat.go).
WORK_SECTIONS = ("in-progress", "check")

# Реплика подъёма адресная. Слова «продолжай» хватало сессии, поднятой по
# упавшему ходу: у той в окне уже лежал разговор о своей строке. Поднятая с
# нуля сессия контекста не имеет и на общей доске берёт что угодно, включая
# строку, которую прямо сейчас ведёт человек. Поэтому реплика называет строку
# и запрещает брать другую работу.
ORDER = ("продолжай %s, эту строку ты уже вёл. Другую работу с доски не бери. "
         "Продолжай в существующем дереве задачи, сохрани незакоммиченные правки, "
         "проверь их и доведи незавершённый этап")

# Поручение в файле задачи. Очередь слияний кладёт его строкой записи при
# снятии (shipctl queueTrace), и подъём передаёт его исполнителю в заказе:
# уведомление без поручения не кончается разбором, и строка стоит мёртвым
# грузом (DK-1322).
ASSIGN = "поручение:"

# Раздел файла задачи, куда shipctl пишет машинные записи слияния и снятия,
# и куда ложится запись подъёма (taskform.Merged).
MERGED = "## Выкат"

# Заголовок, перед которым встаёт «Выкат», если его нет: по форме Verification
# идёт следом за Merged (taskform.Sections).
VERIFY = "## Проверка"

# Заказ голове, поднятой лежащей репликой, собирает сама лестница по флагу
# `taskctl run --reply` (taskhead.ReplyOrder): текст один на всех зовущих, и
# второй копии его на python не заводится.

# Заголовок строки цели: её переписку читает сам цикл цели, и голову задачи ей
# поднимать нельзя (та же проверка у подъёма в taskctl и у панели).
GOAL_TITLE = "Цель:"

# Коды выхода `taskctl run`, по которым решается зов человеку. Единицей лестница
# говорит, что голову поднять нечем и человека она позвала сама
# (taskhead.CodeCalled). Двойкой она отказывает на раскладке, и о таком отказе
# человека зовёт этот заход. Тройка это занятый замок, и там работа уже идёт:
# реплику прочитает та голова, что держит замок.
TASKCTL_CALLED = 1
TASKCTL_SETUP = 2

# Потолок подъёма за один заход, когда ёмкость спросить не у кого.
FALLBACK_LIMIT = 2

# Потолок подъёма на машину за один заход. Ёмкость считается по корню, а корней
# под надзором несколько, и каждый сам по себе укладывается в свой потолок.
# Разом это дало бы столько сессий, сколько корней, поэтому счёт идёт сквозной.
MACHINE_LIMIT = 3

# Сколько секунд строка считается только что поднятой. Сессия харнеса заводит
# транскрипт не мгновенно, и до первой записи доска честно говорит «сессии
# нет». Без этой выдержки следующий тик поднял бы ту же строку второй раз.
SETTLE = 900

# След подъёма: строка «корень ID отметка попытки» на каждую поднятую задачу.
LIFT_LOG = "lift.log"

# Сколько раз подряд поднимать строку, чья сессия не удержалась. Заказ уходит
# успешно, окно заводится, а сессия гибнет на старте, и строка возвращается в
# находки следующего захода. Без потолка заход дёргал бы её вечно.
LIFT_TRIES = 3

# Через сколько секунд счётчик попыток забывается. Строку, поднятую сутки
# назад, считать той же попыткой незачем.
FORGET = 86400

# Сколько секунд лежащая реплика остаётся поводом для подъёма, те же сутки.
# Первый тик после выката нашёл бы во входах реплики недельной давности к
# строкам, которые давно никто не ведёт, и потратил бы сессии на ответы
# вопросам, которых человек уже не ждёт. Реплика старше срока лежит во входе
# дальше, а заход называет такие строки в отчёте с готовой командой подъёма:
# обычная голова на строке в Check с приёмкой до реплики не доходит, стоп стоит
# раньше первого хода.
REPLY_TTL = 86400

# Готовая команда подъёма головы по лежащей реплике для зова человеку, ID и
# корень подстановками. Слово в слово та же, которой зовёт лестница
# (taskhead.ReplyRunCommand), сторож пары в internal/taskhead/raise_test.go.
REPLY_RUN = "taskctl run %s -C %s --reply"

# Метка времени в начале строки входа, той же формой, какой её пишет панель
# (lineStamp в internal/chat) и рукописная строка сценария проверки.
LINE_STAMP = "%Y-%m-%d %H:%M"
LINE_STAMP_RE = re.compile(r"^\d{4}-\d{2}-\d{2} \d{2}:\d{2}")

# Образцы команд, которые остаются сиротами от прогонов сценариев. Нагрузка
# сценария живёт дочерними процессами, и снятая обёртка уносит с собой trap,
# а не детей. Список узкий нарочно: заход убивает процессы, и широкий образец
# тут дороже пропущенного мусора.
ORPHAN_PATTERNS = (
    # Нагрузка сценариев: холостой цикл на любом питоне, как его пишут шаги
    # проверки.
    re.compile(r"(?i)python[^\s]*\s+-c\s+.{0,40}while\s+True:\s*pass"),
    # Прогон тестов и сборка во временном дереве прогона. Привязка к каталогу
    # обязательна: имя рабочего дерева задачи лезет в аргументы обычного
    # `go test`, и образец по нему унёс бы живой прогон человека под сигнал.
    re.compile(r"(?i)\bgo\s+(test|build)\b.*/T/(shipctl|taskctl)-\w+"),
    re.compile(r"(?i)/T/(shipctl-merge|taskctl-rehearse)-\d+/"),
)


# Находка захода: ID строки, заказ голове, слова причины для отчёта и признак
# реплики. У находки по реплике заказ пуст: его собирает лестница по флагу.
# Признак нужен отчёту, зову и флагу: молчание в ответ человеку и осиротевшая
# работа это разные события, а очередь подъёма у них одна.
Find = collections.namedtuple("Find", "id order why reply")


def log_path(home=None):
    home = home or os.path.expanduser("~/.devkit")
    return os.path.join(home, LIFT_LOG)


def marks(home=None, now=None):
    """Журнал подъёма: ключ «корень\tID» против пары (отметка, число попыток).
    Записи старше FORGET не читаются: счётчик попыток живёт сутки."""
    now = time.time() if now is None else now
    out = {}
    try:
        with open(log_path(home), encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        return out
    for ln in text.splitlines():
        f = ln.split("\t")
        if len(f) < 3:
            continue
        try:
            when = float(f[2])
            tries = int(f[3]) if len(f) > 3 else 1
        except ValueError:
            continue
        if now - when < FORGET:
            out[f[0] + "\t" + f[1]] = (when, tries)
    return out


def recent(home=None, now=None):
    """Строки, поднятые только что: выдержка считается по ним."""
    now = time.time() if now is None else now
    return {k: v[0] for k, v in marks(home, now).items() if now - v[0] < SETTLE}


def mark_lifted(root, tid, home=None, now=None):
    """След подъёма строки: отметка времени и номер попытки подряд. Записи
    старше суток заход уносит, иначе журнал растёт без края."""
    now = time.time() if now is None else now
    known = marks(home, now)
    was = known.pop(root + "\t" + tid, None)
    tries = (was[1] + 1) if was else 1
    keep = ["%s\t%.0f\t%d" % (key, val[0], val[1]) for key, val in known.items()]
    keep.append("%s\t%s\t%.0f\t%d" % (root, tid, now, tries))
    try:
        with open(log_path(home), "w", encoding="utf-8") as fh:
            fh.write("\n".join(keep) + "\n")
    except OSError:
        pass
    return tries


def board_rows(root, call=None, taskctl=None):
    """Строки доски корня из секций работы с полями этапа и ключом секции.
    Разбор один на всех: доска отдаёт готовый json, второго парсера markdown тут
    не заводится."""
    call = subprocess.run if call is None else call
    bin = taskctl or which("taskctl")
    if not bin:
        return [], "бинаря taskctl нет ни в PATH, ни в каталогах релиза"
    try:
        p = call([bin, "-C", root, "list", "--json"],
                 stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    except OSError as e:
        return [], str(e)
    if p.returncode != 0:
        return [], "taskctl list завершился с кодом %d" % p.returncode
    try:
        doc = json.loads(p.stdout or "{}")
    except ValueError as e:
        return [], "разбор доски не вышел, %s" % e
    rows = []
    for sec in doc.get("sections") or []:
        if sec.get("key") not in WORK_SECTIONS:
            continue
        for row in sec.get("rows") or []:
            row["_section"] = sec.get("key")
            rows.append(row)
    return rows, ""


def silence_of(row):
    """Возраст молчания сессии строки по словам доски, секунды. None значит,
    что молчание минутами не записано: у записи без времени касания возраст
    неизвестен, и подъём по такому молчанию не судит."""
    m = SILENT_RE.match((row.get("stage_session") or "").strip())
    return int(m.group(1)) * 60 if m else None


def stage_touch(row):
    """Касалась ли сессия строки реестра на самом этапе. Молчание, начавшееся
    раньше этапа, принадлежит окну, которое строку лишь перевело, и смерти
    исполнителя не доказывает (DK-1361). Этап без метки времени судить нечем,
    и тогда решает один возраст молчания."""
    silence = silence_of(row)
    since = row.get("stage_since") or 0
    if not silence or not since:
        return True
    return silence <= time.time() - since + STAGE_TOUCH_GRACE


def orphan_rows(rows):
    """Строки в работе без живой сессии: этап означает работу, а taskctl про
    сессию говорит, что её нет, либо что она молчит дольше рубежа. Мёртвый
    процесс и долгое молчание ведут себя одинаково: и то и другое говорит,
    что за строкой никого нет, и доска различает их только словами.
    Закрытый этап без головы задачи тоже требует продолжения. Его живость
    приходит полем stage_head, сессии закрытого этапа в ответе доски нет.

    Строка в Check с приёмкой человека сюда не идёт. Её проверка это дело
    человека, и голова задачи на такой строке останавливается сама, едва
    поднявшись: «задача ждёт приёмки человеком». Подъём тратил на неё три
    попытки подряд и сдавался, а строке от этого не было ни пользы, ни вреда.
    Живой прогон 25 сентября это показал на DK-937."""
    out = []
    for row in rows:
        if (row.get("title") or "").startswith(GOAL_TITLE):
            continue
        stage = (row.get("stage") or "").strip()
        note = (row.get("stage_session") or "").strip()
        silent = (silence_of(row) or 0) > SILENT_AFTER and stage_touch(row)
        headless = (row.get("stage_state") == "закрыт"
                    and row.get("stage_head") == "головы нет")
        if stage not in WORK_STAGES or not (note.startswith(GONE) or silent or headless):
            continue
        if row.get("_section") == "check" and (row.get("accept") or "agent") != "agent":
            continue
        out.append(row)
    return out


def row_why(row):
    """Слова причины находки: брошенная строка либо молчащая дольше рубежа.
    Слова молчания берутся у доски готовыми: возраст уже напечатан минутами."""
    stage = row.get("stage") or ""
    if row.get("stage_state") == "закрыт":
        return "этап «%s» закрыт, %s" % (stage, row.get("stage_head") or "головы нет")
    note = (row.get("stage_session") or "").strip()
    if note.startswith(SILENT):
        return "этап «%s», %s" % (stage, note)
    return "этап «%s» без сессии" % stage


def diagnostic_rows(rows, home=None):
    """Рассогласованные строки требуют разбора, а не слепого продолжения.
    Отсутствие этапа не доказывает обрыв исполнителя. Неснятые зависимости
    запрещают продолжение даже тогда, когда последний этап известен."""
    import watch
    orphans = {r.get("id") for r in orphan_rows(rows)}
    candidates = [r for r in rows
                  if (not (r.get("stage") or "").strip()
                      and r.get("_section") == "in-progress")
                  or (r.get("held_by") and r.get("id") in orphans)]
    if not candidates:
        return []
    ids = {r.get("id") for r in candidates}
    devhome = home or os.path.expanduser("~/.devkit")
    userhome = os.path.dirname(os.path.abspath(devhome))
    active = set()
    now = datetime.now()
    seen = set()
    for vals in watch.session_rows(userhome):
        tid = vals.get("задача", "")
        sid = vals.get("сессия")
        if tid.upper() not in ids or (tid, sid) in seen:
            continue
        seen.add((tid, sid))
        if watch.session_alive(sid, now, userhome):
            active.add(tid.upper())
    out = []
    for r in candidates:
        tid = r.get("id") or ""
        missing = not (r.get("stage") or "").strip()
        if not tid or (r.get("title") or "").startswith(GOAL_TITLE):
            continue
        if head_up(tid, devhome) or tid.upper() in active:
            continue
        if r.get("stage_head") not in (None, "", "головы нет"):
            continue
        note = (r.get("stage_session") or "").strip()
        if note and not note.startswith(GONE):
            continue
        why = ("строка In progress без записи этапа; состояние исполнителя неизвестно"
               if missing else row_why(r))
        if r.get("held_by"):
            why += "; неснятые зависимости %s, запуск работы запрещён" % ", ".join(r["held_by"])
        why += "; нужен разбор состояния задачи без разработки и снятия зависимостей"
        out.append((r, why))
    return out


def report_diagnostic(root, tid, why, call=None, act=True):
    """Запись исхода служит и адресом дежурного, и памятью повторного тика."""
    import duty
    import watch
    words = "задача %s в %s: %s" % (tid, root, why)
    if not act:
        return words + "; передалась бы дежурному"
    path = Path(root, "docs", "tasks", tid + ".md")
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        text = ""
    if any(duty.parse_mark(ln) == why for ln in duty.stage_lines(text)):
        return words + "; исход дежурному уже записан"
    note = watch.task_record(root, tid, why)
    said = watch.shout("нужен разбор состояния: задача %s" % tid, why,
                       root, call=call, task=tid, quiet=True)
    return words + "; исход дежурному, %s; %s" % (note, said)


def incident_rows(rows, root="", home=None, act=True):
    """Открытые инциденты конвейера по строкам корня (DK-1415). Возврат это
    (строки отчёта, ID приостановленных строк).

    Мёртвый замок при строке в работе значит погибший процесс конвейера:
    оболочка снимает замок на штатном выходе, и остаётся он только у убитого.
    Такая остановка получает запись инцидента, дежурного по ней поднимет тик,
    а автоматический подъём строки стоит до исхода разбора. Открытый инцидент
    любой причины держит ту же приостановку: повторные подъёмы жгли бы попытки,
    которые расходовать обязан дежурный. Ответ человеку приостановка не трогает:
    реплика поднимается своей очередью, а замок у лестницы свой, и дежурному
    подъём не запрещён."""
    import duty
    hook = duty.incidents_mod()
    if hook is None:
        return [], set()
    devhome = home or os.path.expanduser("~/.devkit")
    userhome = os.path.dirname(os.path.abspath(devhome))
    lines, halted = [], set()
    for r in rows:
        tid = str(r.get("id") or "").upper()
        if not tid or (r.get("title") or "").startswith(GOAL_TITLE):
            continue
        if r.get("_section") != "in-progress" or r.get("held_by"):
            continue
        rec = hook.open_for(tid, home=userhome)
        pidfile = os.path.join(devhome, "task-%s.lock" % tid, "pid")
        try:
            with open(pidfile, encoding="utf-8") as fh:
                pid = int(fh.read().strip() or 0)
        except (OSError, ValueError):
            pid = 0
        gone = pid > 0 and dead_pid(pid)
        if rec is None:
            if not gone:
                continue
            why = "замок задачи держит мёртвый процесс %d, конвейер исчез без стопа" % pid
            if not act:
                lines.append("задача %s: %s, записался бы инцидент" % (tid, why))
                halted.add(tid)
                continue
            ident = hook.report(tid, hook.PROC_GONE, why, root=str(root),
                                home=userhome)
            if not ident:
                # Журнал не записался: молча гасить обнаружение нельзя, но и
                # держать строку без записи некому, она идёт обычным порядком.
                lines.append("задача %s: %s, инцидент не записался" % (tid, why))
                continue
            lines.append("задача %s: %s, инцидент %s, автоматический подъём "
                         "приостановлен до разбора дежурным" % (tid, why, ident))
            halted.add(tid)
            continue
        if gone and act:
            # Мёртвый замок стоит и после записи: повторное обнаружение той же
            # остановки дополняет её счётчиком, а не плодит записи.
            hook.report(tid, rec.get("reason") or hook.PROC_GONE,
                        rec.get("why") or "", root=str(root), home=userhome)
        halted.add(tid)
        lines.append("задача %s: открыт инцидент %s (%s), автоматический подъём "
                     "приостановлен" % (tid, rec.get("id"), rec.get("reason")))
    return lines, halted


def head_up(tid, home=None):
    """Держит ли голову задачи работающий процесс. Замок это каталог
    ~/.devkit/task-<ID>.lock с pid внутри, общий у команды подъёма и у оболочки
    конвейера (internal/taskhead). Замок точнее выдержки: панель поднимает голову
    по реплике сразу и следа подъёма тут не оставляет, а живой замок виден и ей,
    и этому заходу. Живого pid мало (DK-1454): работой замок доказывает свежая
    отметка жизни, а замерзшее окно с живым pid головой не считается, и подъём
    либо разбор идут мимо него."""
    home = home or os.path.expanduser("~/.devkit")
    lock = os.path.join(home, "task-%s.lock" % tid.upper())
    try:
        with open(os.path.join(lock, "pid"), encoding="utf-8") as fh:
            pid = int(fh.read().strip() or 0)
    except (OSError, ValueError):
        return False
    if pid <= 0 or dead_pid(pid):
        return False
    return beat_age(lock) < STALE_AFTER


def beat_age(lock):
    """Возраст отметки жизни замка, секунды. Отметка это файл beat, а замкам,
    поднятым до его появления, последним признаком жизни был сам pid: его время
    и меряется. Замка без обоих файлов не бывает, ноль значит свежий подъём."""
    for name in (BEAT_NAME, "pid"):
        try:
            return time.time() - os.path.getmtime(os.path.join(lock, name))
        except OSError:
            continue
    return 0.0


def chat_hook():
    """Модуль подхвата реплики hooks/chat-in.py либо None. Берётся он у сторожка
    тем же импортом по пути: адресата реплики разбирает сам подхват, и вторая
    копия формата разъехалась бы с первой на первой же правке."""
    try:
        import watch
        return watch.chat_hook()
    except ImportError:
        return None


def reply_rows(root, rows, home=None, hook=None, now=None):
    """Строки корня с лежащей во входе чата свежей безадресной репликой
    человека, те, что заход поднимает. Устаревшие остаются за split_reply_rows."""
    return split_reply_rows(root, rows, home=home, hook=hook, now=now)[0]


def split_reply_rows(root, rows, home=None, hook=None, now=None):
    """Строки корня с лежащей во входе чата безадресной репликой человека, двумя
    списками: со свежей репликой и с устаревшей (старше REPLY_TTL).

    Реплику берёт только голова задачи, и адрес у реплики это адресат разговора:
    строка «..., сессии <ID>: ...» написана живому окну, а не задаче, и подъёма
    она не просит. Разбор адресата идёт у подхвата.

    Вход читается в корне проекта, а не в дереве задачи: панель кладёт реплику
    туда, и оболочка головы поднимается там же, значит там её и прочитает
    подхват. Из находок уходят два случая. Живая голова прочитает реплику сама.
    У цели своя оболочка, и переписку она читает сама. Секции строк тут не
    смотрятся: они отобраны у доски (WORK_SECTIONS), и припаркованная вопросом
    строка сюда не доезжает, её поднимает пробуждение ответом."""
    hook = chat_hook() if hook is None else hook
    if hook is None:
        return [], []
    now = time.time() if now is None else now
    names = {}
    for d, name, suffix in hook.chat_names(root):
        if not name.lower().startswith(hook.TASK_CHAT):
            continue
        names.setdefault(name[len(hook.TASK_CHAT):].upper(), []).append(
            os.path.join(d, name + suffix))
    fresh, stale = [], []
    for row in rows:
        tid = (row.get("id") or "").upper()
        if tid not in names or head_up(tid, home):
            continue
        if (row.get("title") or "").startswith(GOAL_TITLE):
            continue
        lines = [ln for ln in (lying_line(path, hook) for path in names[tid]) if ln]
        if not lines:
            continue
        ages = [reply_age(ln, now) for ln in lines]
        if any(age is None or age < REPLY_TTL for age in ages):
            fresh.append(row)
        else:
            stale.append(row)
    return fresh, stale


def lying_line(path, hook):
    """Первая безадресная строка входа либо пустая строка."""
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            lines = [ln.strip() for ln in fh.read().split("\n") if ln.strip()]
    except OSError:
        return ""
    for line in lines:
        if not hook.addressee(line):
            return line
    return ""


def reply_age(line, now=None):
    """Возраст реплики в секундах по метке в начале строки. None значит, что
    метки нет: рукописная строка без времени считается свежей, срок ей мерить
    нечем, а прочитает её первая же поднятая голова."""
    m = LINE_STAMP_RE.match(line)
    if not m:
        return None
    try:
        when = time.mktime(time.strptime(m.group(0), LINE_STAMP))
    except (ValueError, OverflowError):
        return None
    now = time.time() if now is None else now
    return max(0.0, now - when)


def busy_count(rows):
    """Сколько строк корня ведёт живая сессия."""
    return sum(1 for r in rows if (r.get("stage_session") or "").startswith(ALIVE)
               or r.get("stage_head") == "голова жива")


def which(name):
    """Бинарь связки из PATH или из каталогов релиза."""
    found = shutil.which(name)
    if found:
        return found
    try:
        import update
        dirs = update.BIN_DIRS
    except Exception:
        dirs = ()
    for d in dirs:
        cand = os.path.expanduser(os.path.join(d, name))
        if os.access(cand, os.X_OK):
            return cand
    return ""


def capacity(root, rows, call=None, agentctl=None):
    """Свободная ёмкость корня: потолок пачки из `agentctl budget` минус строки,
    которые уже ведут живые сессии. Возврат это число мест и слова о том,
    откуда взят потолок."""
    call = subprocess.run if call is None else call
    bin = agentctl or which("agentctl")
    limit, src = FALLBACK_LIMIT, "потолок по умолчанию"
    if bin:
        try:
            p = call([bin, "budget"], cwd=root, stdout=subprocess.PIPE,
                     stderr=subprocess.DEVNULL, text=True)
            m = re.search(r"batch:\s*(\d+)", p.stdout or "")
            if m:
                limit, src = int(m.group(1)), "потолок пачки agentctl budget"
        except OSError:
            pass
    busy = busy_count(rows)
    return max(0, limit - busy), "%s %d, живых сессий %d" % (src, limit, busy)


def lift_root(root, call=None, taskctl=None, agentctl=None, act=True, home=None,
              left=None):
    """Подъём строк одного корня, осиротевших и с лежащей репликой человека.
    Возврат это строки отчёта и число поднятых строк: потолок на машину
    считается сквозным по всем корням.

    Находки двух родов идут одной очередью, и очередь эта общая нарочно: ёмкость
    корня, выдержка и потолок попыток у них одни. Реплика стоит впереди работы:
    человек ждёт ответа, а «продолжай» увело бы голову в работу мимо его слов."""
    rows, err = board_rows(root, call=call, taskctl=taskctl)
    if err:
        return ["корень %s: доска не прочиталась, %s" % (root, err)], 0
    incident_words, halted = incident_rows(rows, root=root, home=home, act=act)
    replied, stale = split_reply_rows(root, rows, home=home)
    diagnostics = [(r, why) for r, why in diagnostic_rows(rows, home=home)
                   if str(r.get("id") or "").upper() not in halted]
    diagnosed = {r.get("id") for r, _ in diagnostics}
    diagnostic_words = [report_diagnostic(root, r["id"], why, call=call, act=act)
                        for r, why in diagnostics]
    found = [Find(r.get("id") or "", "", "во входе чата лежит реплика человека", True)
             for r in replied]
    said = {f.id for f in found}
    found += [Find(r.get("id") or "", ORDER % (r.get("id") or ""), row_why(r), False)
              for r in orphan_rows(rows)
              if (r.get("id") or "") not in said and not r.get("held_by")
              and r.get("id") not in diagnosed
              and str(r.get("id") or "").upper() not in halted]
    fresh = recent(home)
    tried = marks(home)
    held = [f for f in found if root + "\t" + f.id in fresh]
    found = [f for f in found if root + "\t" + f.id not in fresh]
    spent = [f for f in found if tried.get(root + "\t" + f.id, (0, 0))[1] >= LIFT_TRIES]
    found = [f for f in found if f not in spent]
    spent_words = incident_words + diagnostic_words + [
        "задача %s в %s: поднималась %d раза подряд, сессия не удержалась"
        % (f.id, root, LIFT_TRIES) for f in spent]
    if stale:
        spent_words.append("корень %s: реплики старше суток лежат во входах %s, подъём по ним не "
                           "идёт, поднять руками: %s"
                           % (root, ", ".join(r.get("id") or "?" for r in stale),
                              "; ".join(REPLY_RUN % (r.get("id") or "?", root) for r in stale)))
    # Строка, отлежавшая все попытки, не ждёт человека молча: реплика зовёт
    # громко и с готовой командой, а работа уходит исходом дежурному записью в
    # задаче и уведомлением, как исчерпание подъёма упавшего хода (DK-1361).
    # Дежурного поднимает тот же тик по метке записи. Зовут один раз: троттлинг
    # уведомителя короче тика, и без своей памяти баннер повторялся бы каждые
    # пять минут до конца света. Памятью служит тот же счётчик попыток: попытка
    # сверх потолка значит, что исход ушёл. Метка времени остаётся прежней,
    # иначе выдержка сочла бы строку только что поднятой.
    for f in spent:
        if not act:
            continue
        when, tries = tried[root + "\t" + f.id]
        if tries > LIFT_TRIES:
            continue
        if f.reply:
            spent_words.append(call_human(root, f.id, call=call))
        else:
            spent_words.append(escalate_duty(root, f.id, f.why, call=call))
        mark_lifted(root, f.id, home, now=when)
    if not found:
        if spent_words:
            return spent_words, 0
        if held:
            return ["корень %s: строки %s подняты недавно, сессия ещё заводится"
                    % (root, ", ".join(f.id or "?" for f in held))], 0
        return ["корень %s: строк в работе без сессии и реплик без адресата нет" % root], 0
    free, why = capacity(root, rows, call=call, agentctl=agentctl)
    if left is not None:
        free = min(free, left)
        why += ", остаток потолка машины %d" % left
    lines = spent_words + ["корень %s: строк к подъёму %d (%s), свободных мест %d"
             % (root, len(found), ", ".join(f.id or "?" for f in found), free)]
    if free < 1:
        lines.append("корень %s: %s, подъём отложен до следующего тика" % (root, why))
        return lines, 0
    raised = 0
    for f in found:
        if raised >= free:
            lines.append("задача %s в %s: место кончилось, строка ждёт следующего захода"
                         % (f.id, root))
            continue
        if not act:
            lines.append("задача %s в %s: %s, поднялась бы заказом" % (f.id, root, f.why))
            raised += 1
            continue
        order = f.order
        task = "" if f.reply else assignment(root, f.id, call=call)
        if task:
            # Поручение из файла задачи едет в заказе: поднятая сессия
            # узнаёт, что строку сняли и что с ней делать, а не только
            # продолжает старую работу (DK-1322).
            order += ". Поручение: " + task
        code, words = run_order(root, f.id, order, call=call, taskctl=taskctl, reply=f.reply)
        if code == 0:
            raised += 1
            mark_lifted(root, f.id, home)
            lines.append("задача %s в %s: %s, поднята адресным заказом: %s"
                         % (f.id, root, f.why, words))
            reason = "ответить на лежащую реплику" if f.reply else task
            lines.append(notify_lift(root, f.id, reason, call=call))
            lines.append(record_lift(root, f.id, reason, call=call))
            continue
        lines.append("задача %s в %s: подъём отбит кодом %d: %s" % (f.id, root, code, words))
        # Лестница зовёт человека сама, когда голову поднять нечем (код 1), и
        # второй баннер о том же ему не нужен. Занятый замок (код 3) это не
        # отказ вовсе, там работа уже идёт. Зовут тут на сломанную раскладку:
        # реплика лежит без адресата и без срока, и молчать о ней нельзя.
        if f.reply and code == TASKCTL_SETUP:
            lines.append(call_human(root, f.id, call=call))
    return lines, raised


def call_human(root, tid, call=None):
    """Громкий зов человеку к реплике, которую забрать некому: заголовок, тело с
    готовой командой подъёма и строка отчёта об отправке. Канала своего тут нет,
    зовёт уведомитель сторожка, и повод у события тот же, каким зовут к
    осиротевшей задаче."""
    import watch
    body = ("Реплика лежит во входе чата задачи непрочитанной, а голову поднять не вышло. "
            "Поднять руками: " + REPLY_RUN % (tid, root))
    said = watch.shout("%s: реплика задаче %s лежит недоставленной"
                       % (os.path.basename(root.rstrip("/")), tid),
                       body, root, call=call, task=tid)
    return "задача %s в %s: человек позван к лежащей реплике, %s" % (tid, root, said)


def escalate_duty(root, tid, why, call=None):
    """Исход дежурному на исчерпание попыток подъёма: запись в файле задачи и
    фоновое уведомление, тем же исходом, каким кончается исчерпание подъёма
    упавшего хода (DK-1361). Запись и уведомитель живут у сторожка, второй
    копии тут не заводится. Громкий зов человеку остаётся хвостом разбора
    дежурного, а не этим уведомлением."""
    import watch
    note = watch.task_record(root, tid, "%s, подъём не помог %d раз подряд"
                             % (why, LIFT_TRIES))
    said = watch.shout("подъём не помог: задача %s" % tid,
                       "%s в %s: строка поднималась %d раза подряд, сессия не "
                       "удержалась. Исход дежурному: запись в файл задачи и это "
                       "фоновое уведомление; поднимется дежурный"
                       % (tid, os.path.basename(root.rstrip("/")), LIFT_TRIES),
                       root, call=call, task=tid, quiet=True)
    return "задача %s в %s: исход дежурному, %s; %s" % (tid, root, note, said)


def run_order(root, tid, order, call=None, taskctl=None, reply=False):
    """Заказ головы задачи с названной репликой либо, с признаком reply, заказом
    по лежащей реплике, который лестница собирает сама. Лестница носителей,
    предполёт и замок живут в `taskctl run`, второй копии тут не заводится."""
    call = subprocess.run if call is None else call
    bin = taskctl or which("taskctl")
    if not bin:
        return 1, "бинаря taskctl нет ни в PATH, ни в каталогах релиза"
    argv = [bin, "-C", root, "run", tid] + (["--reply"] if reply else ["--order", order])
    try:
        p = call(argv,
                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return 1, str(e)
    return p.returncode, " ".join((p.stdout or "").split())


def branch_of_task(branch, tid):
    """Ветка названа по ID строчными, с хвостом-слагом или без: та же форма,
    что у branchOfTask в shipctl."""
    b, low = branch.lower(), tid.lower()
    return b == low or b.startswith(low + "-")


def task_tree(root, tid, call=None):
    """Дерево ветки задачи по списку worktree, тем же разбором, что у
    shipctl taskWorktree. Дерева может не быть (копию окна переключили),
    тогда возвращается пустая строка."""
    call = subprocess.run if call is None else call
    try:
        p = call(["git", "-C", root, "worktree", "list", "--porcelain"],
                 stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    except OSError:
        return ""
    path = ""
    for ln in (p.stdout or "").splitlines():
        if ln.startswith("worktree "):
            path = ln[len("worktree "):].strip()
        elif ln.startswith("branch refs/heads/"):
            if path and branch_of_task(ln[len("branch refs/heads/"):], tid) and os.path.isdir(path):
                return path
            path = ""
    return ""


def assignment(root, tid, call=None):
    """Поручение из файла задачи: последняя строка с меткой ASSIGN, как её
    кладёт очередь слияний при снятии. Пусто, если поручения нет либо дерева
    нет: подъём тогда идёт обычным заказом."""
    wt = task_tree(root, tid, call=call)
    if not wt:
        return ""
    try:
        with open(os.path.join(wt, "docs", "tasks", tid + ".md"),
                  encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        return ""
    found = ""
    for ln in text.splitlines():
        if ASSIGN in ln:
            found = ln.split(ASSIGN, 1)[1].strip()
    return found


def insert_into(text, heading, line):
    """Строка в конец раздела; раздел без него заводится на своём месте по
    форме: перед «Проверкой», иначе в конец файла. Порядок разделов тот же,
    что у taskform (Merged перед Verification), тут его грубая копия на одну
    строку: python-часть не тащит taskform целиком."""
    lines = text.splitlines()
    for i, ln in enumerate(lines):
        if ln.strip() != heading:
            continue
        j = i + 1
        while j < len(lines) and not lines[j].startswith("## "):
            j += 1
        k = j
        while k > i + 1 and not lines[k - 1].strip():
            k -= 1
        lines.insert(k, line)
        if k + 1 < len(lines) and lines[k + 1].strip():
            lines.insert(k + 1, "")
        return "\n".join(lines) + ("\n" if text.endswith("\n") else "")
    block = ["", heading, "", line]
    for i, ln in enumerate(lines):
        if ln.strip() == VERIFY:
            lines[i:i] = block
            return "\n".join(lines) + ("\n" if text.endswith("\n") else "")
    while lines and not lines[-1].strip():
        lines.pop()
    return "\n".join(lines + block) + "\n"


def record_lift(root, tid, reason, call=None):
    """Запись подъёма в файл задачи на ветке, коммитом туда же, как пишет
    снятие очередь. Строка несёт время до минуты и повод из того же места,
    что и уведомление о подъёме: повторы в один день различимы без сверки
    с журналом тика (DK-1449). Без дерева запись некуда писать, и об этом
    говорит строка отчёта: подъём не повод оставить грязь в основном
    чекауте (DK-1322). Пишет без замка, как appendRecord и flushStages.
    Гонка дорог теряет запись, а не данные, и грязным деревом отбивается
    следующее слияние."""
    wt = task_tree(root, tid, call=call)
    if not wt:
        return "задача %s: запись подъёма не легла, дерева ветки нет" % tid
    rel = os.path.join("docs", "tasks", tid + ".md")
    path = os.path.join(wt, rel)
    line = "- %s строка поднята тиком, %s" % (
        time.strftime("%Y-%m-%d %H:%M"),
        (ASSIGN + " " + reason) if reason else "задание: продолжать строку")
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except OSError as e:
        return "задача %s: запись подъёма не легла, %s" % (tid, e)
    try:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(insert_into(text, MERGED, line))
    except OSError as e:
        return "задача %s: запись подъёма не легла, %s" % (tid, e)
    run = subprocess.run if call is None else call
    try:
        run(["git", "-C", wt, "add", "--", rel],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        p = run(["git", "-C", wt, "commit", "-q", "-m",
                 "docs(tasks): %s строка поднята" % tid, "--", rel],
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return "задача %s: запись подъёма не закоммичена, %s" % (tid, e)
    if getattr(p, "returncode", 0) != 0:
        words = " ".join((getattr(p, "stdout", "") or "").split())
        return "задача %s: запись подъёма не закоммичена, %s" % (tid, words)
    return "задача %s: запись подъёма в %s" % (tid, rel)


def notify_lift(root, tid, task, call=None):
    """Уведомление о подъёме: поднятая строка получает задание, запись в
    задаче и уведомление, и без последнего подъём неотличим от бездействия
    (DK-1322)."""
    import watch
    body = "строка поднята тиком, " + (
        (ASSIGN + " " + task) if task else "задание: продолжать строку")
    said = watch.shout("%s: задача %s поднята"
                       % (os.path.basename(root.rstrip("/")), tid),
                       body, root, call=call, task=tid)
    return "задача %s в %s: уведомление о подъёме, %s" % (tid, root, said)


def dead_pid(pid):
    """Мёртв ли процесс: сигнал нулём не трогает живого и не врёт о чужом."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return True
    except PermissionError:
        return False
    except OSError:
        return False
    return False


def stale_locks(home, act=True):
    """Замки задач, чей владелец умер либо замерз. Взятие задачи снимает такой
    замок само, а строке, которую никто не берёт, это не помогает. Замерзший
    владелец это живой pid без признаков жизни (DK-1454): такой замок держала
    бы вечно стоящая строка, и убирает его тот же обход."""
    lines = []
    try:
        names = sorted(os.listdir(home))
    except OSError:
        return lines
    for name in names:
        if not (name.startswith("task-") and name.endswith(".lock")):
            continue
        path = os.path.join(home, name)
        pidfile = os.path.join(path, "pid")
        try:
            with open(pidfile, encoding="utf-8") as fh:
                pid = int(fh.read().strip() or 0)
        except (OSError, ValueError):
            continue
        dead = dead_pid(pid)
        frozen = not dead and beat_age(path) >= STALE_AFTER
        if not (dead or frozen):
            continue
        why = ("владельца %d нет" % pid) if dead else \
              ("владелец %d жив, но признаков жизни нет" % pid)
        if not act:
            lines.append("замок %s: %s, снялся бы" % (name, why))
            continue
        try:
            shutil.rmtree(path)
            lines.append("замок %s снят: %s" % (name, why))
        except OSError as e:
            lines.append("замок %s снять не вышло, %s" % (name, e))
    return lines


def orphan_procs(call=None):
    """Процессы нагрузки, пережившие своего родителя: пара (pid, команда)."""
    call = subprocess.run if call is None else call
    try:
        p = call(["ps", "-axo", "pid=,ppid=,command="], stdout=subprocess.PIPE,
                 stderr=subprocess.DEVNULL, text=True)
    except OSError:
        return []
    found = []
    for ln in (p.stdout or "").splitlines():
        parts = ln.strip().split(None, 2)
        if len(parts) < 3:
            continue
        try:
            pid, ppid = int(parts[0]), int(parts[1])
        except ValueError:
            continue
        if ppid != 1 or pid == os.getpid():
            continue
        cmd = parts[2]
        if any(rx.search(cmd) for rx in ORPHAN_PATTERNS):
            found.append((pid, cmd))
    return found


def kill_orphans(call=None, act=True):
    """Снятие процессов нагрузки, оставшихся без родителя."""
    found = orphan_procs(call=call)
    if not found:
        return ["сиротских процессов нагрузки нет"]
    if not act:
        return ["сиротских процессов нагрузки %d, снялись бы" % len(found)]
    killed, failed = 0, 0
    for pid, _ in found:
        try:
            os.kill(pid, 9)
            killed += 1
        except OSError:
            failed += 1
    words = "снято сиротских процессов нагрузки %d" % killed
    if failed:
        words += ", не далось %d" % failed
    return [words]


def stale_trees(act=True):
    """Брошенные деревья прогона. Разбор тот же, что у доктора: своего счёта
    возраста и своего списка каталогов тут не заводится."""
    try:
        import runtrees
    except ImportError:
        return []
    try:
        found = runtrees.abandoned()
    except Exception as e:
        return ["деревья прогона посчитать не вышло, %s" % e]
    if not found:
        return ["брошенных деревьев прогона нет"]
    if not act:
        return ["брошенных деревьев прогона %d, снялись бы" % len(found)]
    gone = 0
    for path, repo, tree in found:
        try:
            runtrees.drop(path, repo, tree)
            gone += 1
        except Exception:
            pass
    return ["снято брошенных деревьев прогона %d" % gone]


def sweep(home=None, call=None, act=True):
    """Уборка следов умерших сессий: замки задач, процессы нагрузки и деревья
    прогонов."""
    home = home or os.path.expanduser("~/.devkit")
    return (stale_locks(home, act=act) + kill_orphans(call=call, act=act)
            + stale_trees(act=act))


def roots(home=None):
    """Корни, по которым идёт обход. Берутся у сторожка: реестр целей и записи
    этапов, второго списка корней в связке нет."""
    import watch
    home = home or watch.default_home()
    seen, out = set(), []
    found = [watch.read_entry(path).get("root", "") for path in watch.entries(home)]
    found += list(watch.run_roots(home))
    for root in found:
        if root and root not in seen and os.path.isdir(root):
            seen.add(root)
            out.append(root)
    return out


def run(root=None, home=None, act=True, out=print, call=None):
    """Команда `devkitctl lift`. Печатает, что найдено и что сделано."""
    targets = [root] if root else roots(home)
    lines = []
    if not targets:
        lines.append("корней под надзором нет: поднимать нечего")
    # Уборка идёт первой: замок мёртвого владельца стоит на пути у подъёма той
    # же строки, и снятый после подъёма он помог бы только следующему заходу.
    lines += sweep(home=home, call=call, act=act)
    left = MACHINE_LIMIT
    for r in targets:
        said, raised = lift_root(r, call=call, act=act, home=home, left=left)
        lines += said
        left -= raised
        if left < 1:
            lines.append("потолок подъёма на машину %d исчерпан, остальные корни ждут "
                         "следующего захода" % MACHINE_LIMIT)
            break
    for ln in lines:
        out(ln)
    return 0
