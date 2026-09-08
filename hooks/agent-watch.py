#!/usr/bin/env python3
"""Сторож фоновых работ devkit: сказать сессии, что её фоновая работа кончилась.

Фоновая работа отчитывается сессии уведомлением харнеса, и уведомление это
теряется. Сессия-диспетчер тогда считает отработавшего исполнителя живым,
задача стоит часами, а итог приходится собирать опросом агентов руками
(находка DK-519, отчёт по ревью DK-181). Сторож ведёт свой счёт запущенным
работам и на конце хода сдаёт сессии то, чего она не получила.

Разрядов работы два, и теряются они по-разному. Субагент кончается своим
событием, и сторожу остаётся свести его с транскриптом. У команды оболочки,
уведённой в фон, своего события нет вовсе: сессия, отдавшая в фон слияние,
ждала вести, которой некому прийти, и так за одну ночь встали две задачи на
последнем шаге (DK-571, находка DK-168 и DK-516). Конец такой команды виден
только перечнем работ сессии: команда была в перечне, а на конце хода её там
нет.

События и дела:

  запуск (ход делегирования с фоновым ответом, ход Bash с ID фоновой команды)
      запись в реестр: ID работы, разряд, чем названа, куда сложен отчёт
  конец субагента (SubagentStop)
      отметка в реестре и первая строка отчёта
  конец хода сессии (Stop)
      перечень работ: команды, которых в нём не стало, кончились, а команды,
      которых в реестре ещё нет, в него заводятся
      сдача: отработавшие работы, о которых сессия не узнала, уезжают ей
      решением block, и ход продолжается вместо сна

Сдача идёт только на конце хода, и это не экономия, а суть: пока сессия
работает, уведомление харнеса ещё может доехать само, и дублировать его незачем.
Ход, законченный при незабранном отчёте, это и есть та самая потеря, ради
которой сторож стоит.

Сторож отличает отработавшую работу от забранной по транскрипту сессии: весть
о конце фоновой работы едет к модели через очередь, и запись очереди с
`<task-id>` лежит в транскрипте рядом с самой репликой. Нет там ни того, ни
другого, значит сессия про работу не знает. Между концом субагента и записью
очереди проходит доля секунды, поэтому сторож пережидает свежий конец
(DEVKIT_AGENT_WATCH_GRACE, по умолчанию 5 секунд): весть, доехавшая на секунду
позже сдачи, стоила бы сессии лишнего хода.

Сторож ловит пропажу перечнем фоновых работ, который харнес кладёт в событие
конца хода. Субагент, которого сессия числит запущенным, а харнес в перечне уже
не называет, кончился молча: так выглядит работа, убитая перезапуском процесса.
Сторож говорит про такую отдельно, отчёта у неё нет. У команды оболочки тот же
перечень значит другое: пропав из него, команда просто кончилась, и код
возврата сторож читает из вести харнеса в транскрипте. Нет там вести, значит
сессия про конец не знает, и команда уезжает ей сдачей.

Команда, которая идёт, когда ход кончается, остаётся в журнале строкой
«ожидание»: разбудить сессию нечем, и умрёт такая команда вместе с ней. По этой
строке и разбирается потом, куда делась работа.

Реестр правится под замком: диспетчер поднимает исполнителей пачкой, ходы
инструмента идут разом, и два хука одной сессии пишут в один файл. Ожидание
вести харнеса при этом остаётся вне замка, иначе конец хода запирал бы реестр на
все пять секунд.

Синхронный субагент сторожу не интересен: он отчитывается ходом инструмента, и
сессии негде его терять. В реестр попадает только то, что запущено фоном, а
конец незнакомой работы сторож пропускает молча.

Режим один:
  agent-watch.py --hook [протокол]  событие читается со stdin и разбирается по
                                    имени протокола таблицей hookio.py (голый
                                    --hook это claude-code)

Переменные окружения:
  DEVKIT_AGENT_WATCH_OFF=1     не сторожить ничего: реестр не ведётся, сдачи нет
  DEVKIT_AGENT_WATCH_GRACE=..  сколько секунд ждать весть харнеса перед сдачей
  DEVKIT_AGENT_WATCH_DIR=..    свой каталог реестра (стенд, прогон проверки)

Реестр лежит по файлу на сессию в ~/.devkit/agents/<сессия>.json, журнал сдач
в ~/.devkit/agents.log. Жалоба «сессия проспала работу» разбирается по
журналу: в нём видно и разряд, и запуск, и конец, и сдачу с её причиной.
"""
import fcntl
import json
import os
import re
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

HOME_DIR = os.path.join(os.path.expanduser("~"), ".devkit")
REGISTRY_DIR = os.path.join(HOME_DIR, "agents")
LOG = os.path.join(HOME_DIR, "agents.log")

OFF_ENV = "DEVKIT_AGENT_WATCH_OFF"
GRACE_ENV = "DEVKIT_AGENT_WATCH_GRACE"
DIR_ENV = "DEVKIT_AGENT_WATCH_DIR"

# Сколько ждать весть харнеса, прежде чем считать её потерянной, и как часто
# перечитывать транскрипт, пока ждём.
GRACE = 5.0
POLL = 0.5
# Хвост транскрипта, по которому видно доставленную весть. Файл сессии растёт до
# десятков мегабайт, а весть о только что кончившейся работе лежит в самом
# конце: читать целиком незачем.
TAIL = 256 * 1024
# Первая строка отчёта, которую сторож несёт в сдаче.
TEXT_LIMIT = 300
# Запись реестра живёт сутки: работа, о которой за это время никто не вспомнил,
# сессии уже не нужна, а файл реестра не должен расти без края.
LIFETIME = 24 * 60 * 60

# Сколько раз и с какой паузой стучаться в замок реестра, прежде чем махнуть
# рукой: полсекунды ожидания на чужую короткую запись хватает с запасом.
LOCK_TRIES = 40
LOCK_PAUSE = 0.0125

RUNNING, DONE, LOST = "running", "done", "lost"
# Статус работы в перечне харнеса, значащий живую работу. Всё остальное
# (failed и что там ещё придёт) это конец, о котором надо сказать.
JOB_RUNNING = "running"
# Разряды работы, имена те же, что у харнеса в перечне работ сессии.
SUBAGENT_JOB = hookio.SUBAGENT_JOB
SHELL_JOB = hookio.SHELL_JOB
# Метка вести харнеса в транскрипте и код возврата в её тексте.
MARK = "<task-id>%s</task-id>"
CODE = re.compile(r"exit code (\d+)")


def off(env=None):
    env = os.environ if env is None else env
    return bool((env.get(OFF_ENV) or "").strip())


def grace(env=None):
    env = os.environ if env is None else env
    try:
        return max(0.0, float(env.get(GRACE_ENV)))
    except (TypeError, ValueError):
        return GRACE


def registry_dir(env=None):
    env = os.environ if env is None else env
    return (env.get(DIR_ENV) or "").strip() or REGISTRY_DIR


def log_path(env=None):
    """Куда писать журнал. Свой каталог реестра уводит туда и журнал: стенд не
    должен дописывать машинный файл, по которому разбирают живые жалобы."""
    env = os.environ if env is None else env
    own = (env.get(DIR_ENV) or "").strip()
    return os.path.join(own, "agents.log") if own else LOG


def registry_path(session, env=None):
    # Имя сессии приходит от харнеса, а становится именем файла, поэтому от него
    # остаётся только то, что не уводит из каталога.
    name = "".join(c for c in (session or "") if c.isalnum() or c in "-_")
    return os.path.join(registry_dir(env), (name or "unknown") + ".json")


def short(text, limit=TEXT_LIMIT):
    text = " ".join((text or "").split())
    return text if len(text) <= limit else text[:limit] + "..."


def dashless(value):
    value = " ".join((value or "").split())
    return value or "-"


def log(session, job_id, event, text, env=None, job=SUBAGENT_JOB):
    """Строка машинного журнала. Формат именованный, как у уведомителя: жалоба
    разбирается по ключевым словам, а не по счёту полей. Разряд стоит своим
    полем: субагент и команда оболочки теряются по-разному, и разбирать их
    порознь надо прямо по журналу."""
    line = "%s сессия %s разряд %s работа %s событие %s текст «%s»\n" % (
        time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime()),
        dashless(session)[:8], dashless(job), dashless(job_id),
        dashless(event), short(text, 200))
    hookio.append_capped(log_path(env), line)


def take_lock(path, tries=LOCK_TRIES, pause=LOCK_PAUSE, sleep=time.sleep):
    """Замок реестра сессии. Ходы инструмента идут пачкой (диспетчер поднимает
    исполнителей разом), и два хука одной сессии правят один файл: без замка
    запись про один запуск затирала бы запись про соседний. None значит, что
    замок занят дольше отведённого, и событие проходит мимо реестра: сторож не
    вправе терять ход сессии из-за своего файла."""
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        fd = os.open(path + ".lock", os.O_CREAT | os.O_RDWR, 0o644)
    except OSError:
        return None
    for _ in range(tries):
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return fd
        except OSError:
            sleep(pause)
    try:
        os.close(fd)
    except OSError:
        pass
    return None


def drop_lock(fd):
    try:
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)
    except OSError:
        pass


def load_registry(path):
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError):
        return {}
    agents = data.get("agents") if isinstance(data, dict) else None
    return agents if isinstance(agents, dict) else {}


def save_registry(path, session, agents, now=None):
    """Реестр целиком. Пустой файл убирается: живой файл в каталоге значит
    сессию с незакрытыми работами, и по нему видно, за кем сторож смотрит."""
    now = time.time() if now is None else now
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        if not agents:
            if os.path.exists(path):
                os.remove(path)
            return
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump({"session": session, "updated": now, "agents": agents},
                      f, ensure_ascii=False)
        os.replace(tmp, path)
    except OSError:
        pass


def sweep(agents, now):
    """Реестр без протухших записей."""
    return dict((k, v) for k, v in agents.items()
                if isinstance(v, dict) and now - float(v.get("started") or 0) < LIFETIME)


def entry_of(job, kind, description, command, output, now):
    return {"type": kind, "description": description, "command": command,
            "output": output, "started": now, "done": 0, "message": "",
            "state": RUNNING, "told": False, "warned": False, "job": job}


def launched(agents, event, now):
    agents[event.agent_id] = entry_of(event.job or SUBAGENT_JOB, event.agent_type,
                                      event.description, event.command, event.output, now)
    return agents


def job_of(entry):
    """Разряд записи. Записи, сделанные до разряда команд, все до одной про
    субагентов."""
    return entry.get("job") or SUBAGENT_JOB


def named(entry):
    """Чем работа названа в журнале и в сдаче: командная строка у команды,
    данное человеком имя у субагента."""
    return entry.get("command") or entry.get("description") or ""


def finished(agents, event, now):
    """Отметка конца работы. Незнакомый ID это синхронный субагент: он отчитался
    ходом инструмента, и сторожить его незачем."""
    entry = agents.get(event.agent_id)
    if not isinstance(entry, dict):
        return None
    entry["state"] = DONE
    entry["done"] = now
    entry["message"] = short(event.message)
    return entry


def notice(transcript, job_id, tail=TAIL):
    """Весть харнеса о конце работы из хвоста транскрипта. Весть едет через
    очередь, и запись очереди несёт `<task-id>` до того, как реплика доедет до
    модели: сторожу довольно любой из двух, обе значат весть в пути. Пустая
    строка значит, что вести там нет, и сессия про конец работы не знает.

    Транскрипта нет или он не читается: сдать лишний раз дешевле, чем
    промолчать про потерянный отчёт, поэтому нечитаемый файл это тоже пусто."""
    if not transcript or not job_id:
        return ""
    try:
        with open(transcript, "rb") as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - tail))
            text = f.read().decode("utf-8", "replace")
    except OSError:
        return ""
    at = text.find(MARK % job_id)
    if at < 0:
        return ""
    end = text.find("</task-notification>", at)
    return text[at:end if end >= 0 else len(text)]


def told_of(transcript, agent_id, tail=TAIL):
    """Знает ли сессия про конец работы."""
    return bool(notice(transcript, agent_id, tail))


def code_of(said):
    """Код возврата команды из вести харнеса. Пусто значит, что вести нет или
    код в ней не назван: у субагента его не бывает вовсе."""
    m = CODE.search(said or "")
    return m.group(1) if m else ""


def waited(transcript, pending, seconds, sleep=time.sleep):
    """Кто из работ дождался вести харнеса. Ожидание общее на всю пачку: пять
    субагентов, кончившихся разом, не должны складывать задержку конца хода,
    а срок у пачки один, самый длинный из остатков."""
    left = seconds
    pending = set(pending)
    delivered = set()
    while True:
        for agent_id in sorted(pending):
            if told_of(transcript, agent_id):
                delivered.add(agent_id)
        pending -= delivered
        if not pending or left <= 0:
            return delivered
        step = min(POLL, left)
        sleep(step)
        left -= step


def lost_of(agents, jobs):
    """Работы, кончившиеся молча: сессия числит их запущенными, а харнес в
    перечне фоновых работ уже не называет либо называет с отказом."""
    seen = dict((j.id, j) for j in jobs if j.kind == SUBAGENT_JOB)
    out = []
    for agent_id, entry in agents.items():
        if entry.get("state") != RUNNING or job_of(entry) != SUBAGENT_JOB:
            continue
        job = seen.get(agent_id)
        if job is None or job.status != JOB_RUNNING:
            entry["state"] = LOST
            out.append((agent_id, entry))
    return out


def stopped(job_id, jobs, kind):
    """Кончилась ли работа по перечню харнеса: работы там нет вовсе либо она
    названа с отказом."""
    for job in jobs:
        if job.kind == kind and job.id == job_id:
            return job.status != JOB_RUNNING
    return True


def shell_ends(agents, event, now):
    """Команды, которые харнес в перечне работ больше не числит. Своего события
    про конец команды у харнеса нет, и виден он только так: работа была в
    перечне, а на конце хода её там нет. Код возврата берётся из вести харнеса,
    и его отсутствие само по себе новость: значит, вести нет и сессия про конец
    команды не знает."""
    out = []
    for job_id, entry in agents.items():
        if job_of(entry) != SHELL_JOB or entry.get("state") != RUNNING:
            continue
        if not stopped(job_id, event.jobs, SHELL_JOB):
            continue
        entry["state"] = DONE
        entry["done"] = now
        entry["message"] = code_of(notice(event.transcript, job_id))
        out.append((job_id, entry))
    return out


def fresh_shells(agents, jobs, now):
    """Команды из перечня работ, которых в реестре ещё нет. Ход инструмента
    оболочки доходит до сторожа не на всякой машине: раскладка, положенная до
    разряда команд, зовёт сторожа одним инструментом делегирования, а перечень
    конца хода приходит всегда. Через него же попадают в счёт команды, начатые
    до того, как сторож встал в настройки."""
    out = []
    for job in jobs:
        if job.kind != SHELL_JOB or job.status != JOB_RUNNING or job.id in agents:
            continue
        agents[job.id] = entry_of(SHELL_JOB, "", job.description, job.command, "", now)
        out.append((job.id, agents[job.id]))
    return out


def live_shells(agents):
    """Команды, которые идут, а ход кончается. Разбудить сессию их концу нечем,
    и умрут они вместе с ней, поэтому сторож называет их в журнале, пока они
    ещё живы."""
    return [(job_id, entry) for job_id, entry in agents.items()
            if job_of(entry) == SHELL_JOB and entry.get("state") == RUNNING
            and not entry.get("warned")]


def done_word(entry):
    """Как назвать работу в сдаче: роль и данное ей имя."""
    name = entry.get("description") or ""
    role = entry.get("type") or "субагент"
    return "«%s» (%s)" % (name, role) if name else role


def shell_line(job_id, entry):
    """Строка сдачи про команду. Кода возврата в ней может и не быть: он
    приходит той же вестью харнеса, которая до сессии не дошла."""
    code = entry.get("message") or ""
    code = ", код возврата %s" % code if code else ""
    return ("фоновая команда «%s» (ID %s) кончилась%s, а весть о ней до сессии не дошла: "
            "харнес больше не числит её среди работ сессии"
            % (short(named(entry), 120), job_id, code))


def report_line(job_id, entry):
    if job_of(entry) == SHELL_JOB:
        return shell_line(job_id, entry)
    where = entry.get("output") or ""
    tail = ", отчёт лежит в %s" % where if where else ""
    if entry.get("state") == LOST:
        return ("фоновый субагент %s (ID %s) кончился молча: харнес больше не числит его среди "
                "работ сессии, а отчёта не прислал%s" % (done_word(entry), job_id, tail))
    said = entry.get("message") or ""
    said = ", первая строка отчёта: «%s»" % said if said else ""
    return ("фоновый субагент %s (ID %s) отработал, а весть о нём до сессии не дошла%s%s"
            % (done_word(entry), job_id, tail, said))


def handover(lines):
    """Текст сдачи. Сессия читает его вместо сна, поэтому в нём сказано и что
    случилось, и что с этим делать."""
    body = "\n".join("- %s" % line for line in lines)
    return ("Сторож фоновых работ devkit: работа кончилась, а сессия про это не узнала.\n"
            "%s\n"
            "Считать такую работу идущей нельзя: забери её итог (отчёт субагента из его файла, "
            "вывод команды из файла её вывода) и продолжай работу. Второй раз сторож про эти "
            "работы не скажет." % body)


def blocked(text, stream=None):
    """Канал сдачи: решение block на конце хода. Харнес отдаёт текст модели и
    продолжает ход вместо того, чтобы уснуть."""
    out = sys.stdout if stream is None else stream
    json.dump({"decision": "block", "reason": text}, out, ensure_ascii=False)
    out.write("\n")


def update(path, session, change, now, sleep=time.sleep):
    """Правка реестра под замком: change правит словарь записей и возвращает,
    что сказать дальше. None значит, что замок не дался и правка пропущена."""
    fd = take_lock(path, sleep=sleep)
    if fd is None:
        return None
    try:
        agents = sweep(load_registry(path), now)
        out = change(agents)
        save_registry(path, session, agents, now)
        return out
    finally:
        drop_lock(fd)


def handover_lines(agents, event, delivered, env, now):
    """Что сдать сессии, и заодно отметки в реестре. Концы команд и пропажи
    считаются заново, по перечню работ из события: реестр за время ожидания
    вести мог уехать."""
    lines = []
    for job_id, entry in fresh_shells(agents, event.jobs, now):
        log(event.session, job_id, "запуск", named(entry), env, SHELL_JOB)
    for job_id, entry in shell_ends(agents, event, now):
        code = entry.get("message") or "неизвестен"
        log(event.session, job_id, "конец", "%s, код возврата %s" % (named(entry), code),
            env, SHELL_JOB)
    for agent_id, entry in lost_of(agents, event.jobs):
        entry["told"] = True
        lines.append(report_line(agent_id, entry))
        log(event.session, agent_id, "пропажа", entry.get("description") or "", env)
    for job_id, entry in sorted(agents.items(), key=lambda kv: kv[1].get("done") or 0):
        if entry.get("state") != DONE or entry.get("told"):
            continue
        entry["told"] = True
        if job_id in delivered:
            log(event.session, job_id, "весть", "уведомление харнеса дошло само",
                env, job_of(entry))
            continue
        lines.append(report_line(job_id, entry))
        log(event.session, job_id, "сдача", entry.get("message") or "", env, job_of(entry))
    for job_id, entry in live_shells(agents):
        entry["warned"] = True
        log(event.session, job_id, "ожидание",
            "%s: ход кончился, команда идёт, и вместе с сессией она умрёт" % named(entry),
            env, SHELL_JOB)
    return lines


def handle(event, env=None, now=None, sleep=time.sleep, stream=None):
    """Одно событие: реестр обновлён, сдача напечатана, если было что сдавать."""
    now = time.time() if now is None else now
    path = registry_path(event.session, env)
    if event.kind == hookio.AGENT_LAUNCHED:
        if not event.agent_id:
            return 0
        if update(path, event.session, lambda a: launched(a, event, now), now, sleep) is not None:
            log(event.session, event.agent_id, "запуск",
                event.command or event.description, env, event.job or SUBAGENT_JOB)
        return 0
    if event.kind == hookio.SUBAGENT_DONE:
        entry = update(path, event.session, lambda a: finished(a, event, now), now, sleep)
        if entry is not None:
            log(event.session, event.agent_id, "конец", entry["message"], env)
        return 0
    if event.kind != hookio.TURN_DONE or event.active:
        # Сторож пропускает ход, продолженный стоп-хуком: второй заход закрутил
        # бы сессию в цикле, а сказанное в первый раз уже сказано.
        return 0
    stored = load_registry(path)
    agents = sweep(stored, now)
    fresh = [j for j in event.jobs if j.kind == SHELL_JOB and j.status == JOB_RUNNING
             and j.id not in agents]
    if not agents and not fresh:
        if stored:
            # Протухшие записи уехали, и реестр надо переписать даже тогда,
            # когда сдавать нечего: иначе файл сессии живёт вечно.
            update(path, event.session, lambda a: None, now, sleep)
        return 0
    # Весть харнеса пережидается до замка: держать реестр запертым пять секунд
    # значило бы ронять правки соседних ходов той же сессии.
    left = {}
    for job_id, entry in agents.items():
        if entry.get("told"):
            continue
        if entry.get("state") == DONE:
            left[job_id] = grace(env) - max(0.0, now - float(entry.get("done") or 0))
        elif (entry.get("state") == RUNNING and job_of(entry) == SHELL_JOB
              and stopped(job_id, event.jobs, SHELL_JOB)):
            # Конец команды виден только тем, что харнес перестал её называть, а
            # весть о нём ещё в пути: сроку тут столько же, сколько свежему
            # концу субагента.
            left[job_id] = grace(env)
    delivered = waited(event.transcript, left, max(left.values()), sleep) if left else set()
    lines = update(path, event.session,
                   lambda a: handover_lines(a, event, delivered, env, now), now, sleep)
    if not lines:
        return 0
    blocked(handover(lines), stream)
    return 0


def run_hook(protocol, env=None, now=None, sleep=time.sleep, stream=None):
    if off(env):
        return 0
    event = hookio.agent_event(protocol)
    if event is None or not event.session:
        # Чужое событие и событие без ID сессии сторожу не годятся: реестр
        # ведётся по сессии, а ронять чужую работу из-за своего журнала нельзя.
        return 0
    try:
        return handle(event, env, now, sleep, stream)
    except OSError:
        return 0


def main(argv):
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("agent-watch: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
