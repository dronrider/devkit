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

# Реплика подъёма адресная. Слова «продолжай» хватало сессии, поднятой по
# упавшему ходу: у той в окне уже лежал разговор о своей строке. Поднятая с
# нуля сессия контекста не имеет и на общей доске берёт что угодно, включая
# строку, которую прямо сейчас ведёт человек. Поэтому реплика называет строку
# и запрещает брать другую работу.
ORDER = "продолжай %s, эту строку ты уже вёл. Другую работу с доски не бери"

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


def lift_root(root, call=None, taskctl=None, agentctl=None, act=True, home=None,
              left=None):
    """Подъём осиротевших строк одного корня. Возврат это строки отчёта и число
    поднятых строк: потолок на машину считается сквозным по всем корням."""
    rows, err = board_rows(root, call=call, taskctl=taskctl)
    if err:
        return ["корень %s: доска не прочиталась, %s" % (root, err)], 0
    orphans = orphan_rows(rows)
    fresh = recent(home)
    tried = marks(home)
    held = [r for r in orphans if root + "\t" + (r.get("id") or "") in fresh]
    orphans = [r for r in orphans if root + "\t" + (r.get("id") or "") not in fresh]
    spent = [r for r in orphans
             if tried.get(root + "\t" + (r.get("id") or ""), (0, 0))[1] >= LIFT_TRIES]
    orphans = [r for r in orphans if r not in spent]
    spent_words = ["задача %s в %s: поднималась %d раза подряд, сессия не удержалась: "
                   "строка ждёт человека, подъём её больше не трогает"
                   % (r.get("id"), root, LIFT_TRIES) for r in spent]
    if not orphans:
        if spent_words:
            return spent_words, 0
        if held:
            return ["корень %s: строки %s подняты недавно, сессия ещё заводится"
                    % (root, ", ".join(r.get("id", "?") for r in held))], 0
        return ["корень %s: строк в работе без сессии нет" % root], 0
    free, why = capacity(root, rows, call=call, agentctl=agentctl)
    if left is not None:
        free = min(free, left)
        why += ", остаток потолка машины %d" % left
    lines = spent_words + ["корень %s: строк в работе без сессии %d (%s), свободных мест %d"
             % (root, len(orphans), ", ".join(r.get("id", "?") for r in orphans), free)]
    if free < 1:
        lines.append("корень %s: %s, подъём отложен до следующего тика" % (root, why))
        return lines, 0
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
        code, words = run_order(root, tid, ORDER % tid, call=call, taskctl=taskctl)
        if code == 0:
            raised += 1
            mark_lifted(root, tid, home)
            lines.append("задача %s в %s: поднята адресным заказом: %s" % (tid, root, words))
        else:
            lines.append("задача %s в %s: подъём отбит кодом %d: %s" % (tid, root, code, words))
    return lines, raised


def run_order(root, tid, order, call=None, taskctl=None):
    """Заказ головы задачи с названной репликой. Лестница носителей, предполёт и
    замок живут в `taskctl run`, второй копии тут не заводится."""
    call = subprocess.run if call is None else call
    bin = taskctl or which("taskctl")
    if not bin:
        return 1, "бинаря taskctl нет ни в PATH, ни в каталогах релиза"
    try:
        p = call([bin, "-C", root, "run", tid, "--order", order],
                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return 1, str(e)
    return p.returncode, " ".join((p.stdout or "").split())


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
            with open(pidfile, encoding="utf-8") as fh:
                pid = int(fh.read().strip() or 0)
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
