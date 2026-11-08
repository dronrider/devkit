"""Одна попытка восстановления подтверждённо исчезнувшего конвейера.

Резерв сохраняется до запуска, предел машины составляет три восстановления
за скользящие сутки. У исходного запуска должны быть известны корень, ID,
модель, подписка и погибший владелец замка. Неизвестное состояние, остановка
пользователем, ожидание или недоступная квота запрещают запуск.

Журнал recovery.jsonl и отдельные доказательства переживают перезапуск
сторожа. Исход «возобновлено» требует нового рабочего действия с теми же
корнем, задачей, попыткой и новым ID запуска. Закрытие инцидента синхронизировано
с ручной остановкой. Сухой прогон только читает состояние.
"""
import fcntl
import json
import os
import re
import subprocess
import tempfile
import time
import uuid
from contextlib import contextmanager
from datetime import datetime, timedelta
from pathlib import Path

# Бюджет первого этапа: три восстановления за скользящие сутки на машину и
# одна попытка на инцидент с корнем. Повтор после неудачи требует человека
# с устранённой причиной, а не очередного тика.
DAY_TRIES = 3
WINDOW = timedelta(hours=24)

# Предел на запуск и подтверждение, секунды. Зависший вызов не удерживает
# дежурство: срок кончается исходом no-proof либо stalled, а чужая и уже
# поднятая работа при этом не завершается.
CONFIRM_WAIT = 60.0

# Метка попытки в заказе и вид метки: полный hex uuid4, различает попытки
# без адресов и времени. Оболочка конвейера узнаёт метку той же строкой.
MARK_RE = re.compile(r"RV-[0-9a-f]{32}")

STAMP = "%Y-%m-%dT%H:%M:%S"
LOG_ENV = "DEVKIT_RECOVERY_LOG"
LOG_NAME = "recovery.jsonl"

# Исходы попытки. Всё, кроме confirmed, оставляет инцидент открытым.
CONFIRMED = "confirmed"
NO_PROOF = "no-proof"
STALLED = "stalled"
LAUNCH_FAILED = "launch-failed"
OVERTAKEN = "overtaken"

OUTCOME_WORDS = {
    CONFIRMED: "рабочий ход доказан, конвейер продолжен",
    NO_PROOF: "доказательства рабочего хода нет",
    STALLED: "доказательство есть, живого хода нет",
    LAUNCH_FAILED: "запуск не состоялся",
    OVERTAKEN: "замок задачи занят чужой работой, запуск отбит",
}


class BudgetError(Exception):
    """Журнал бюджета нечитаем либо повреждён: лимиты неизвестны, и запуск
    запрещается вместо счёта от пустоты."""


def log_path(home=None):
    """Путь журнала: ключ стенда, иначе <дом>/.devkit/recovery.jsonl."""
    given = (os.environ.get(LOG_ENV) or "").strip()
    if given:
        return given
    base = os.path.expanduser("~") if home is None else str(home)
    return os.path.join(base, ".devkit", LOG_NAME)


def proof_dir(home=None):
    """Каталог доказательств попыток, рядом с журналом: замена журнала
    стендом уводит и его."""
    return log_path(home) + ".proofs"


def lock_path(home=None):
    """Путь машинного замка операции: рядом с журналом."""
    return log_path(home) + ".lock"


def _read(home=None):
    path = log_path(home)
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, encoding="utf-8") as stream:
            raw = stream.read()
    except FileNotFoundError:
        return []
    except (OSError, UnicodeError) as error:
        raise BudgetError("журнал бюджета не читается: %s" % error)
    if raw.strip() and not raw.endswith("\n"):
        raise BudgetError("журнал бюджета повреждён: незавершённая строка")
    out = []
    for number, line in enumerate(raw.splitlines(), 1):
        if not line.strip():
            continue
        try:
            rec = json.loads(line)
            if (not isinstance(rec, dict) or rec.get("kind") not in ("reserve", "result")
                    or not isinstance(rec.get("attempt"), str)
                    or not MARK_RE.fullmatch(rec["attempt"])):
                raise ValueError("неизвестная запись")
            if rec["kind"] == "reserve" and not all(rec.get(key) for key in ("root", "task", "incident", "when")):
                raise ValueError("неполный резерв")
        except (ValueError, TypeError):
            raise BudgetError("журнал бюджета повреждён: строка %d" % number)
        out.append(rec)
    return out


def _append(rec, home=None):
    path = log_path(home)
    try:
        os.makedirs(os.path.dirname(path), mode=0o700, exist_ok=True)
        fd = os.open(path, os.O_CREAT | os.O_APPEND | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write((json.dumps(rec, ensure_ascii=False, allow_nan=False) + "\n").encode())
            stream.flush()
            os.fsync(stream.fileno())
        directory = os.open(os.path.dirname(path), os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    except (OSError, ValueError):
        return False
    return True


@contextmanager
def operation(home=None):
    """Машинный замок на выбор и запуск восстановления. Возврат внутри
    контекста это «замок взят»: False значит, что на машине уже идёт другая
    операция, и попытка расходу не подлежит. Ошибка замка это отказ, а не
    работа без блокировки: бюджет без замка молча не тратится."""
    path = lock_path(home)
    taken = False
    f = None
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        f = os.fdopen(fd, "r+")
        fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        taken = True
    except OSError:
        if f is not None:
            f.close()
            f = None
    try:
        yield taken
    finally:
        if f is not None:
            f.close()


def new_mark():
    """Метка попытки: полный hex uuid4 с префиксом, угадать и подобрать
    нельзя."""
    return "RV-%s" % uuid.uuid4().hex


def attempt_for(ident, croot, home=None):
    """Резерв по ключу «инцидент плюс корень» либо None. Возврат это
    (запись, закрыт ли исход): закрытость меряется строкой result с той же
    меткой, и незакрытый резерв это оборванная попытка без бесплатного
    повтора."""
    records = _read(home)
    done = {r.get("attempt") for r in records if r.get("kind") == "result"}
    for rec in records:
        if (rec.get("kind") == "reserve" and rec.get("incident") == ident
                and rec.get("root") == croot):
            return rec, rec.get("attempt") in done
    return None, False


def day_spent(home=None, now=None):
    """Резервы за скользящие сутки: окно отнимается от сейчас, стенд
    подставляет своё время. Резерв с нечитаемым временем считается
    израсходованным: сомнение в бюджете не превращается в разрешение."""
    edge = (time.time() if now is None else now.timestamp()) - WINDOW.total_seconds()
    spent = 0
    for rec in _read(home):
        if rec.get("kind") != "reserve":
            continue
        try:
            when = float(rec["at"]) if "at" in rec else datetime.strptime(str(rec.get("when") or ""), STAMP).timestamp()
        except (ValueError, TypeError, OverflowError):
            spent += 1
            continue
        if not (-float("inf") < when < float("inf")) or when >= edge:
            spent += 1
    return spent


def proof_for(mark, croot, task, home=None):
    """Доказательство попытки, сверенное по метке, корню и задаче, либо
    None. Запись атомарна переименованием, и нечитаемой половины не бывает:
    всё, что не разобралось, подтверждением не служит."""
    try:
        with open(os.path.join(proof_dir(home), "%s.json" % mark),
                  encoding="utf-8") as f:
            rec = json.load(f)
    except (OSError, ValueError):
        return None
    if not isinstance(rec, dict):
        return None
    if (rec.get("attempt") != mark or rec.get("root") != croot
            or str(rec.get("task") or "").upper() != task):
        return None
    return rec


def order_for(task, mark, order):
    """Заказ продолжения с меткой попытки: штатный заказ подъёма и хвост,
    по которому оболочка конвейера узнаёт восстановление."""
    return "%s. Метка восстановления %s: оставь её в заказе без правки." % (
        order.rstrip("."), mark)


def _owner_state(task, devhome):
    """Владелец замка задачи: live, dead, none либо unclear. Живой либо
    неопределённый владелец запрещает запуск, мёртвый это и есть предмет
    инцидента, отсутствующий замок спора не вызывает: оболочка конвейера
    сама владеет снятием доказанно мёртвого замка."""
    import lift
    lock = os.path.join(devhome, "task-%s.lock" % task)
    try:
        with open(os.path.join(lock, "pid"), encoding="utf-8") as fh:
            pid = int(fh.read().strip() or 0)
    except (OSError, ValueError):
        try:
            os.stat(lock)
        except OSError:
            return "none"
        return "unclear"
    if pid <= 0:
        return "unclear"
    return "dead" if lift.dead_pid(pid) else "live"


def _confirm(mark, croot, task, devhome, home=None):
    """Подтверждено ли продвижение: proof стадии turn, чей pid жив, владеет
    замком задачи и держит отметку жизни свежей. Возврат это
    (запись-либо-None, live-либо-пусто); всё остальное подтверждением не
    служит, и вызывающий ждёт срока."""
    import lift
    rec = proof_for(mark, croot, task, home=home)
    if rec is None or rec.get("stage") != "turn":
        return rec, ""
    try:
        pid = int(rec.get("pid") or 0)
    except (TypeError, ValueError):
        return rec, ""
    if pid <= 0 or lift.dead_pid(pid):
        return rec, ""
    try:
        import taskstate
        data = taskstate.read(home or os.path.expanduser("~"), croot, task)
        run = data.get("run") or {}
        if (data["stopped"] or not rec.get("run_id") or run.get("id") != rec["run_id"]
                or run.get("pid") != pid or run.get("attempt") != mark):
            return rec, ""
    except (OSError, ValueError, TypeError):
        return rec, ""

    try:
        with open(os.path.join(devhome, "task-%s.lock" % task, "pid"),
                  encoding="utf-8") as fh:
            owner = int(fh.read().strip() or -1)
    except (OSError, ValueError):
        return rec, ""
    if owner != pid:
        return rec, ""
    if lift.beat_age(os.path.join(devhome, "task-%s.lock" % task)) >= \
            lift.STALE_AFTER:
        return rec, ""
    return rec, "live"


def board_section(root, tid):
    """Секция строки задачи на доске корня, по заголовку над строкой
    таблицы. Имя приводится к виду секций у lift (in-progress, check):
    латинская часть заголовка с дефисом вместо пробела. Пусто значит, что
    строки на доске нет."""
    try:
        text = (Path(root) / "docs" / "TASKS.md").read_text(
            encoding="utf-8", errors="replace")
    except OSError:
        return ""
    section = ""
    for ln in text.splitlines():
        if ln.startswith("## "):
            section = ln[3:].strip().lower().split(" (")[0].replace(" ", "-")
        elif ln.startswith("|") and ln.split("|")[1].strip().upper() == tid.upper():
            return section
    return ""


def group_gone(group):
    if type(group) is not int or group <= 0:
        return False
    try:
        os.killpg(group, 0)
    except ProcessLookupError:
        return True
    except (OSError, OverflowError):
        return False
    return False


def wait_reason(home, root, task, run, binary, call, env):
    path = Path(home or os.path.expanduser("~")) / ".devkit" / "waits" / (task+".json")
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except FileNotFoundError:
        return ""
    with os.fdopen(fd, encoding="utf-8") as stream:
        mark = json.load(stream)
    if not isinstance(mark, dict) or mark.get("task") != task:
        raise ValueError("неизвестная отметка ожидания")
    since = datetime.strptime(mark["since"], STAMP).timestamp()
    until = datetime.strptime(mark["until"], STAMP).timestamp()
    now = time.time()
    if since > now+1 or until < since:
        raise ValueError("неизвестное время ожидания")
    if until <= now:
        return ""
    started = datetime.fromisoformat(str(run.get("started") or "").replace("Z", "+00:00")).timestamp()
    if since < started-1:
        return ""
    if mark.get("kind") == "срок":
        return "задача ждёт события, восстановление запрещено"
    result = call([binary, "-C", str(root), "wait", task, str(mark["kind"]),
                   str(mark.get("target") or ""), "--check"],
                  capture_output=True, text=True, timeout=5, env=env)
    if result.returncode == 0:
        return ""
    return ("задача ждёт события, восстановление запрещено" if result.returncode == 1
            else "состояние ожидания неизвестно, восстановление запрещено")


def readiness(rec, root, home, inspect_call=None):
    import lift
    import taskstate
    source = rec.get("identity")
    if not isinstance(source, dict) or not all(source.get(k) for k in ("id", "harness", "model", "pid", "group")):
        return "исходный запуск не установлен, автоматическое восстановление запрещено"
    try:
        data = taskstate.read(home or os.path.expanduser("~"), root, rec["task"])
        if data["stopped"]:
            return "задача остановлена пользователем, требуется явное продолжение"
        run = data.get("run") or {}
        if any(run.get(k) != source[k] for k in ("id", "harness", "model", "pid", "group")):
            return "исходный запуск изменился, автоматическое восстановление запрещено"
        owner_state = _owner_state(rec["task"], str(Path(home or os.path.expanduser("~")) / ".devkit"))
        if owner_state in ("live", "unclear"):
            return "живой владелец замка" if owner_state == "live" else "владелец замка неопределённый"
        owner_path = Path(home or os.path.expanduser("~")) / ".devkit" / ("task-%s.lock" % rec["task"]) / "pid"
        if int(owner_path.read_text().strip()) != source["pid"]:
            return "замок другого исходного запуска"
        if type(source["pid"]) is not int or source["pid"] <= 0 or not lift.dead_pid(source["pid"]):
            return "исходный процесс жив либо не определён"
        if not group_gone(source["group"]):
            return "группа исходного запуска ещё жива либо не определена"
        binary = duty_which("agentctl")
        if not binary:
            return "agentctl не найден, доступность исходной подписки неизвестна"
        call = subprocess.run if inspect_call is None else inspect_call
        env = dict(os.environ)
        env["GIT_OPTIONAL_LOCKS"] = "0"
        if home is not None:
            env["HOME"] = str(home)
        def query(args):
            result = call([binary, "-C", str(root)] + args, capture_output=True,
                          text=True, timeout=5, env=env)
            if result.returncode:
                raise ValueError("машинная проверка подписки завершилась отказом")
            return json.loads(result.stdout)
        mapping = query(["harness", "--json"])
        matches = [h for h in mapping["harnesses"] if h.get("name") == source["harness"]]
        if len(matches) != 1 or matches[0].get("enabled") is not True or matches[0].get("auto") is not True:
            return "исходная подписка выключена либо запрещена автоматике"
        if matches[0].get("hooks_gap"):
            return "обвязка исходной подписки не готова: " + str(matches[0]["hooks_gap"])
        if source["model"] not in [m.get("model") for m in matches[0].get("models", [])]:
            return "исходная модель отсутствует в разрешённой подписке"
        waiting = wait_reason(home, root, rec["task"], run, binary, call, env)
        if waiting:
            return waiting
        quota = query(["quota", "--json", "--harness", source["harness"]])
        if (quota.get("schema") != "agentctl/quota" or quota.get("version") != 1 or
                quota.get("harness") != source["harness"] or
                any(quota.get(k) is not True for k in ("fresh", "complete", "available"))):
            return "квота исходной подписки не разрешает запуск: " + str(quota.get("reason") or "неизвестное состояние")
    except (OSError, ValueError, TypeError, KeyError, subprocess.TimeoutExpired) as error:
        return "состояние исходного запуска не проверено: %s" % error
    return ""


def _machine_gates(hook, root, home, croot, task, ident, inspect_call=None):
    """Машинные запреты запуска до резерва. Возврат это причина отказа
    либо пусто; каждая проверка читает живое состояние из журнала и корня,
    а не снимок, переданный вызывающим кодом."""
    if hook is None or not ident or not task:
        return "инцидент без ID либо задачи, адрес восстановления нестроен"
    reader = getattr(hook, "read_recovery", None)
    if reader is None:
        return "нет строгого чтения журнала инцидентов, обновите весь комплект"
    try:
        records = reader(home=home)
        fresh = [r for r in records if r.get("id") == ident]
    except (OSError, ValueError):
        return "журнал инцидентов повреждён либо не читается, запуск запрещён"
    if not fresh:
        return "инцидента нет в журнале, восстановление не поднимается"
    if fresh[0].get("outcome"):
        return "инцидент уже закрыт исходом %s" % fresh[0].get("outcome")
    if fresh[0].get("reason") != hook.PROC_GONE:
        return "причина не «процесс исчез», первый этап не применяется"
    if str(fresh[0].get("task") or "").upper() != task:
        return "инцидент другой задачи, запуск запрещён"
    rec_root = str(fresh[0].get("root") or "")
    if not rec_root or os.path.realpath(rec_root) != croot:
        return "инцидент чужого либо пустого корня, восстановление не " \
               "поднимается"
    if os.path.isfile(os.path.join(str(root), ".devkit", "chat",
                                   "task-%s.ask" % task)):
        return "по задаче лежит вопрос человеку, запуск запрещён"
    import lift
    section = board_section(root, task)
    if section != "in-progress":
        return "строка задачи вне рабочих секций доски (%s), запуск " \
               "запрещён" % (section or "секции нет")
    for other in records:
        if (other.get("task") == task and not other.get("outcome")
                and os.path.realpath(str(other.get("root") or "")) == croot
                and other.get("reason") == hook.PASSES_OUT
                and other.get("id") != ident):
            return "по задаче открыт инцидент «проходы исчерпаны», запуск " \
                   "запрещён"
    return readiness(fresh[0], root, home, inspect_call)


def resume(hook, rec, root, call=None, taskctl=None, home=None, act=True,
           wait=None, order=None, inspect_call=None):
    """Одна попытка восстановления по открытому инциденту «процесс исчез».

    Возврат это строки отчёта. Инцидент закрывается исходом «возобновлено»
    только по доказанному proof с меткой попытки; отказ до резерва
    оставляет бюджет нетронутым и называется причиной. Сухой прогон
    (act=False) не пишет журнал и proof, не зовёт запуск и не закрывает
    инцидент. `order` подставляет свой текст заказа продолжения, по
    умолчанию штатный заказ подъёма из lift.ORDER. Срок ожидания меряется
    монотонными часами.
    """
    import lift
    lines = []
    ident = str(rec.get("id") or "")
    task = str(rec.get("task") or "").upper()
    croot = os.path.realpath(str(root))
    devhome = (Path(home) / ".devkit") if home else Path(
        os.path.expanduser("~/.devkit"))

    def say(line):
        lines.append("корень %s: инцидент %s, %s" % (root, ident, line))

    why = _machine_gates(hook, root, home, croot, task, ident, inspect_call)
    if why:
        say(why)
        return lines
    try:
        spent, closed = attempt_for(ident, croot, home=home)
    except BudgetError as e:
        say("%s, запуск запрещён" % e)
        return lines
    if spent is not None:
        if closed:
            say("попытка %s по этому инциденту уже израсходована, повтор "
                "требует устранённой причины" % spent.get("attempt"))
        else:
            say("попытка %s не закрыта исходом: восстановление оборвалось, "
                "бесплатного повтора нет" % spent.get("attempt"))
        return lines
    owner = _owner_state(task, str(devhome))
    if owner == "live":
        say("живой владелец замка, восстановление не запускается")
        return lines
    if owner == "unclear":
        say("владелец замка неопределённый, восстановление не запускается")
        return lines
    try:
        used = day_spent(home=home)
    except BudgetError as e:
        say("%s, запуск запрещён" % e)
        return lines
    if used >= DAY_TRIES:
        say("лимит восстановлений за 24 часа исчерпан (%d из %d), запуск "
            "запрещён" % (used, DAY_TRIES))
        return lines

    if not act:
        say("запуск восстановления разрешён (попытка %d из %d за 24 часа), "
            "сухой прогон без запуска" % (used + 1, DAY_TRIES))
        return lines

    with operation(home=home) as taken:
        if not taken:
            say("на машине уже идёт восстановление, попытка не расходуется")
            return lines
        try:
            spent, closed = attempt_for(ident, croot, home=home)
            if spent is not None:
                say("попытка по этому инциденту появилась параллельно, "
                    "запуск не повторяется")
                return lines
            used = day_spent(home=home)
        except BudgetError as e:
            say("%s, запуск запрещён" % e)
            return lines
        if used >= DAY_TRIES:
            say("лимит восстановлений исчерпан параллельным тиком, запуск "
                "запрещён")
            return lines
        why = _machine_gates(hook, root, home, croot, task, ident, inspect_call)
        if why or _owner_state(task, str(devhome)) in ("live", "unclear"):
            say(why or "владелец изменился перед запуском, запуск запрещён")
            return lines
        mark = new_mark()
        line = {"kind": "reserve", "when": datetime.now().strftime(STAMP), "at": time.time(),
                "attempt": mark, "incident": ident, "task": task,
                "root": croot}
        if not _append(line, home=home):
            say("сбой записи бюджета, запуск запрещён")
            return lines
        outcome, extra = _launch_and_confirm(
            hook, ident, mark, task, root, croot, str(devhome),
            call=call, taskctl=taskctl, home=home, wait=wait, order=order,
            original=next(r["identity"] for r in hook.read(home=home) if r.get("id") == ident))
        say(extra)
    return lines


def _launch_and_confirm(hook, ident, mark, task, root, croot, devhome,
                        call=None, taskctl=None, home=None, wait=None,
                        order=None, original=None):
    """Запуск и подтверждение под взятым замком операции. Возврат это
    (исход, строка отчёта); исход ставится в журнал и в запись инцидента,
    а слово «закрыт» звучит только при записанном результате и прошедшем
    закрытии."""
    import lift
    base = lift.ORDER % task if order is None else order
    argv = [taskctl or duty_which("taskctl"), "-C", str(root), "run", task,
            "--order", order_for(task, mark, base), "--hidden"]
    if original:
        argv += ["--harness", original["harness"], "--model", original["model"],
                 "--recovery-run", original["id"], "--recovery-attempt", mark]
    if not argv[0]:
        return _finish(hook, ident, mark, LAUNCH_FAILED, home,
                       "бинаря taskctl нет ни в PATH, ни в каталогах релиза",
                       "попытка %s закрыта отказом запуска" % mark, croot=croot, task=task)

    deadline = time.monotonic() + (CONFIRM_WAIT if wait is None else
                                   float(wait))
    proc, words, code, out = None, "", None, None
    outcome, why = LAUNCH_FAILED, "подтверждение не закончено"
    try:
        try:
            if call is None:
                out = tempfile.TemporaryFile("w+")
                proc = subprocess.Popen(argv, stdout=out,
                                        stderr=subprocess.STDOUT, text=True)
            else:
                p = call(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                         text=True, timeout=max(0.001, deadline - time.monotonic()))
                code = p.returncode
                words = " ".join((p.stdout or "").split())
        except (OSError, subprocess.TimeoutExpired) as e:
            words, code = str(e), 2
    
        outcome, why = None, ""
        while outcome is None:
            if original:
                import taskstate
                if taskstate.read(home or os.path.expanduser("~"), croot, task)["stopped"]:
                    outcome, why = STALLED, "пользователь остановил задачу"
                    break
            proof, state = _confirm(mark, croot, task, devhome, home=home)
            if time.monotonic() >= deadline:
                outcome = STALLED if proof is not None else NO_PROOF
                why = "ход не дождался подтверждения" if proof is not None else "срок подтверждения истёк"
            elif proof is not None and state == "live":
                outcome, why = CONFIRMED, ""
            elif proof is not None and proof.get("stage") == "turn" and \
                    _proof_dead(proof):
                outcome, why = STALLED, "оболочка с меткой закончилась без " \
                                        "живого хода"
            elif proof is None and code not in (None, 0):
                outcome = OVERTAKEN if code == 3 else LAUNCH_FAILED
                why = words or "код %d" % code
            elif time.monotonic() >= deadline:
                if proof is None:
                    outcome = NO_PROOF
                    why = ("запуск ещё идёт, доказательства хода нет"
                           if proc is not None and proc.poll() is None
                           else "код %d, доказательства хода нет" % code
                           if code is not None else "доказательства хода нет")
                else:
                    outcome, why = STALLED, "ход не дождался подтверждения"
            else:
                if proc is not None:
                    code = proc.poll()
                time.sleep(min(0.2, max(0, deadline - time.monotonic())))
    except (OSError, ValueError, TypeError) as error:
        outcome, why = LAUNCH_FAILED, "подтверждение не проверено: %s" % error
    finally:
        if proc is not None:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=0.2)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    try:
                        proc.wait(timeout=0.2)
                    except subprocess.TimeoutExpired:
                        pass
            else:
                proc.wait()
        if out is not None:
            try:
                out.seek(0)
                words = words or " ".join(out.read(4096).split())[:400]
            except (OSError, ValueError):
                pass
            finally:
                out.close()
    return _finish(hook, ident, mark, outcome, home, why,
                   _outcome_line(mark, outcome, why), croot=croot, task=task, devhome=devhome, deadline=deadline)


def _proof_dead(proof):
    """Мёртв ли pid из proof: сверка с владельцем замка уже прошла
    в _confirm."""
    import lift
    try:
        pid = int(proof.get("pid") or 0)
    except (TypeError, ValueError):
        return True
    return pid <= 0 or lift.dead_pid(pid)


def _outcome_line(mark, outcome, why):
    """Строка отчёта по исходу попытки."""
    base = "попытка %s: %s" % (mark, OUTCOME_WORDS[outcome])
    if why:
        base += " (%s)" % why
    return base


def _finish(hook, ident, mark, outcome, home, why, line,
            croot=None, task=None, devhome=None, deadline=None):
    def persist(outcome, why, line):
        if not _append({"kind": "result", "when": datetime.now().strftime(STAMP),
                        "attempt": mark, "outcome": outcome}, home=home):
            return outcome, line + "; исход не записался в журнал, инцидент остаётся открытым"
        if hook is None:
            return outcome, line + "; журнал инцидента недоступен"
        try:
            remaining = max(0, deadline-time.monotonic()) if deadline is not None else 1
            updated = hook.finish_recovery(ident, croot, task,
                _outcome_line(mark, outcome, why), resumed=outcome == CONFIRMED,
                home=home, timeout=remaining)
            if updated is None:
                return outcome, line + "; инцидент изменился либо уже имеет исход"
            if outcome == CONFIRMED:
                return outcome, line + "; инцидент закрыт возобновлением"
        except (OSError, ValueError) as error:
            return outcome, line + "; журнал инцидента не обновлён: %s" % error
        return outcome, line + "; инцидент остаётся открытым"

    if outcome == CONFIRMED:
        if not all((croot, task, devhome)):
            return persist(NO_PROOF, "нет адреса подтверждения", line)
        try:
            import taskstate
            with taskstate.locked(home or os.path.expanduser("~"), croot, task,
                                  timeout=max(0, deadline-time.monotonic()) if deadline is not None else 1):
                _, live = _confirm(mark, croot, task, devhome, home=home)
                if live != "live":
                    return persist(STALLED, "ход изменился перед закрытием",
                                   _outcome_line(mark, STALLED, "ход изменился перед закрытием"))
                return persist(outcome, why, line)
        except (OSError, ValueError, TypeError) as error:
            return persist(STALLED, str(error), _outcome_line(mark, STALLED, str(error)))
    return persist(outcome, why, line)


def duty_which(name):
    """Бинарь связки из PATH либо из каталогов релиза, как у duty.which."""
    import duty
    return duty.which(name)
