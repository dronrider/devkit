"""Подъём работы, оставшейся без живой сессии (команда lift, DK-1157).

Перезагрузка машины убивает разом все носители: панели tmux, сессии харнеса и
фоновые прогоны. Службы после старта поднимаются сами, а работа нет. Прежние
пути возобновления смотрят на носителя: подъём упавшего хода ищет панель,
страховка ожидания читает признак вопроса, пробуждение ждущих разбирает
Blocked. Строка, которая просто шла в работе, не видна ни одному из них.

Здесь признак другой. Строку выбирает доска: этап означает работу, а живой
сессии за ней нет. Такую строку поднимает обычный заказ `taskctl run` с
репликой «продолжай», по одной и в пределах свободной ёмкости. Тем же заходом
снимаются следы умерших сессий: замки задач с мёртвым владельцем и процессы
нагрузки, пережившие своего родителя.
"""
import json
import os
import re
import shutil
import subprocess
import time

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

# Реплика подъёма та же, что у сторожка при упавшем ходе.
ORDER = "продолжай"

# Потолок подъёма за один заход, когда ёмкость спросить не у кого.
FALLBACK_LIMIT = 2

# Сколько секунд строка считается только что поднятой. Сессия харнеса заводит
# транскрипт не мгновенно, и до первой записи доска честно говорит «сессии
# нет». Без этой выдержки следующий тик поднял бы ту же строку второй раз.
SETTLE = 900

# След подъёма: строка «ID корень отметка» на каждую поднятую задачу.
LIFT_LOG = "lift.log"

# Образцы команд, которые остаются сиротами от прогонов сценариев. Нагрузка
# сценария живёт дочерними процессами, и снятая обёртка уносит с собой trap,
# а не детей. Список узкий нарочно: заход убивает процессы, и широкий образец
# тут дороже пропущенного мусора.
ORPHAN_PATTERNS = (
    re.compile(r"(?i)python[^\s]*\s+-c\s+while True:\s*pass"),
)


def log_path(home=None):
    home = home or os.path.expanduser("~/.devkit")
    return os.path.join(home, LIFT_LOG)


def recent(home=None, now=None):
    """Строки, поднятые недавно: ключ «корень\tID» и отметка подъёма."""
    now = time.time() if now is None else now
    out = {}
    try:
        text = open(log_path(home), encoding="utf-8").read()
    except OSError:
        return out
    for ln in text.splitlines():
        f = ln.split("\t")
        if len(f) != 3:
            continue
        try:
            when = float(f[2])
        except ValueError:
            continue
        if now - when < SETTLE:
            out[f[0] + "\t" + f[1]] = when
    return out


def mark_lifted(root, tid, home=None, now=None):
    """След подъёма строки, по нему считается выдержка."""
    now = time.time() if now is None else now
    try:
        with open(log_path(home), "a", encoding="utf-8") as fh:
            fh.write("%s\t%s\t%.0f\n" % (root, tid, now))
    except OSError:
        pass


def board_rows(root, call=None, taskctl=None):
    """Строки доски корня с полями этапа. Разбор один на всех: доска отдаёт
    готовый json, второго парсера markdown тут не заводится."""
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
        if sec.get("key") not in ("in-progress", "check"):
            continue
        for row in sec.get("rows") or []:
            row["_section"] = sec.get("key")
            rows.append(row)
    return rows, ""


def orphan_rows(rows):
    """Строки в работе без живой сессии: этап означает работу, а taskctl про
    сессию говорит, что её нет."""
    out = []
    for row in rows:
        stage = (row.get("stage") or "").strip()
        note = (row.get("stage_session") or "").strip()
        if stage in WORK_STAGES and note.startswith(GONE):
            out.append(row)
    return out


def busy_count(rows):
    """Сколько строк корня ведёт живая сессия."""
    return sum(1 for r in rows if (r.get("stage_session") or "").startswith(ALIVE))


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


def lift_root(root, call=None, taskctl=None, agentctl=None, act=True, home=None):
    """Подъём осиротевших строк одного корня. Возврат это строки отчёта."""
    rows, err = board_rows(root, call=call, taskctl=taskctl)
    if err:
        return ["корень %s: доска не прочиталась, %s" % (root, err)]
    orphans = orphan_rows(rows)
    fresh = recent(home)
    held = [r for r in orphans if root + "\t" + (r.get("id") or "") in fresh]
    orphans = [r for r in orphans if root + "\t" + (r.get("id") or "") not in fresh]
    if not orphans:
        if held:
            return ["корень %s: строки %s подняты недавно, сессия ещё заводится"
                    % (root, ", ".join(r.get("id", "?") for r in held))]
        return ["корень %s: строк в работе без сессии нет" % root]
    free, why = capacity(root, rows, call=call, agentctl=agentctl)
    lines = ["корень %s: строк в работе без сессии %d (%s), свободных мест %d"
             % (root, len(orphans), ", ".join(r.get("id", "?") for r in orphans), free)]
    if free < 1:
        lines.append("корень %s: %s, подъём отложен до следующего тика" % (root, why))
        return lines
    import watch
    raised = 0
    for row in orphans:
        if raised >= free:
            lines.append("задача %s в %s: место кончилось, строка ждёт следующего захода"
                         % (row.get("id"), root))
            continue
        tid = row.get("id")
        if not act:
            lines.append("задача %s в %s: этап «%s» без сессии, поднялась бы заказом"
                         % (tid, root, row.get("stage")))
            raised += 1
            continue
        code, words = watch.run_head(root, tid, call=call, taskctl=taskctl)
        if code == 0:
            raised += 1
            mark_lifted(root, tid, home)
            lines.append("задача %s в %s: поднята заказом с репликой «%s»: %s"
                         % (tid, root, ORDER, words))
        else:
            lines.append("задача %s в %s: подъём отбит кодом %d: %s" % (tid, root, code, words))
    return lines


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
    """Замки задач, чей владелец умер. Взятие задачи снимает такой замок само,
    а строке, которую никто не берёт, это не помогает."""
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
            pid = int(open(pidfile, encoding="utf-8").read().strip() or 0)
        except (OSError, ValueError):
            continue
        if not dead_pid(pid):
            continue
        if not act:
            lines.append("замок %s: владелец %d мёртв, снялся бы" % (name, pid))
            continue
        try:
            shutil.rmtree(path)
            lines.append("замок %s снят: владельца %d нет" % (name, pid))
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


def sweep(home=None, call=None, act=True):
    """Уборка следов умерших сессий: замки задач и процессы нагрузки."""
    home = home or os.path.expanduser("~/.devkit")
    return stale_locks(home, act=act) + kill_orphans(call=call, act=act)


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
    for r in targets:
        lines += lift_root(r, call=call, act=act)
    for ln in lines:
        out(ln)
    return 0
