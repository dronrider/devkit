"""Дежурный агент разбирает неизвестные отказы конвейера (DK-1323).

Механика конвейера умеет повторить только то, что уже видела: подъём упавшего
хода, снятие строки, разлив очереди. Исход, которого она не знает, кончается
записью с меткой «эскалация дежурному» в файле задачи и уведомлением. Этот
заход поднимает по такой записи дежурного: разбор без прав на правки, чей
исход это вопрос человеку (`taskctl ask`) либо черновик задачи (`taskctl
draft`). Громкий зов человеку остаётся хвостом разбора, а не его началом.

Очередь дежурства это сами записи в файлах задач (решение «очередь
дежурства»). Отдельного файла очереди нет: исход уже лежит там, куда его
положила механика, и вторая копия разошлась бы с первой. Память подъёма
держит ~/.devkit/watch.duty: подпись отказа в ней занимает только
зафиксированный результат разбора (DK-1454), а запуск без результата это
попытка с повтором тиком. По той же памяти тик не входит в разбор отказа,
который уже закрыт (граница DK-1115: одинаковые отказы это шум того
захода, неизвестный исход это предмет здесь).
"""
import json
import os
import re
import shutil
import subprocess
import sys
from datetime import datetime
from pathlib import Path

# Начало заказа дежурному. Строка одна на оболочку конвейера и на этот заход:
# task-run.py узнаёт по ней дежурную голову и держит её одним проходом, каким
# держит голову проверки (CHECK_ORDER) и голову лежащей реплики (REPLY_ORDER).
# Сторож пары test_order_is_one_for_shell_and_duty читает обе копии.
ORDER = "Разбери неизвестный отказ конвейера "

# Хвост заказа: права дежурного и два исхода разбора. Текст уезжает в заказ
# головы и читается там наравне с постановкой задачи.
CONTRACT = (
    "Ты дежурный агент, слой эскалации без прав: правок кода не делай, "
    "исправления остаются за конвейером задачи. Разберись, чем кончился "
    "неизвестный исход, и кончи одним из двух: вопросом человеку "
    "(`taskctl ask`) либо черновиком задачи (`taskctl draft --prio mid`). "
    "Громкий зов человеку это хвост разбора, а не его начало: `taskctl ask` "
    "зовёт сам, после черновика уведоми человека (hooks/notify.py)."
)

# Метка записи исхода в файле задачи. Та же строка, что пишет task_record в
# watch.py: вторая копия слова здесь держится рядом с разбором записи,
# сторож пары test_mark_is_one_for_watch_and_duty читает обе.
DUTY_MARK = "эскалация дежурному"

# Раздел файла задачи, где лежат записи исхода.
STAGE_SECTION = "Ход работы"

# Память подъёма: JSON-строки трёх видов. {"when", "sig", "run": true,
# "drafts": [...]} на запуск без исхода (попытка), {"when", "sig", "fail":
# true} на неудачный подъём, {"when", "sig", "done": "..."} на занятую
# подпись: результат разбора зафиксирован либо добит потолком попыток.
# Записи старого формата, {"when", "sig"}, это запуски без исхода: до DK-1454
# подпись занимал сам запуск, и в новом цикле такие записи не гасят повтор,
# а считаются попыткой. Ручная чистка памяти при обновлении не нужна.
DUTY_STATE = "~/.devkit/watch.duty"

# Потолок неудачных подъёмов одной записи. Ниже его подпись не пишется и
# повтор идёт ближайшим тиком; на потолке исход добивается громким зовом
# человеку и подписью, чтобы не долбить наружу вечно.
SPAWN_TRIES = 3

# Суффикс причины с ID сессии. Он различает два падения одного и того же
# отказа, а для границы DK-1115 нужна сама причина, без адреса сессии.
SESSION_TAIL = re.compile(r",\s*сессия\s+\S+\s*$")


def duty_order(task, why):
    """Заказ дежурному: имя задачи, суть исхода и права разбора."""
    return "%s%s: %s. %s" % (ORDER, task, why.strip().rstrip("."), CONTRACT)


def signature(why):
    """Подпись отказа для границы DK-1115: причина без адреса сессии.

    Два падения одной и той же поломки различаются сессией, а известным
    отказ делает именно причина. Подпись без неё была бы всегда новой, и
    шум входил бы в разбор подряд идущими заходами.
    """
    return SESSION_TAIL.sub("", (why or "").strip()).strip().rstrip(".")


def parse_mark(line):
    """(why,) из строки записи исхода либо None, если строки тут нет.

    Формат строки пишет task_record в watch.py: дата, метка, причина с
    точкой в конце.
    """
    if DUTY_MARK not in line:
        return None
    _, sep, rest = line.partition(DUTY_MARK)
    if not sep:
        return None
    rest = rest.strip()
    if rest.startswith(":"):
        rest = rest[1:].strip()
    if rest.endswith("."):
        rest = rest[:-1].strip()
    return rest or "исход без причины"


def stage_lines(text):
    """Строки раздела «Ход работы» файла задачи, без заголовка."""
    out, inside = [], False
    for ln in text.splitlines():
        if ln.startswith("## "):
            inside = ln[3:].strip() == STAGE_SECTION
            continue
        if inside:
            out.append(ln)
    return out


def task_marks(root):
    """Записи исхода по файлам задач корня: [(task, why, line), ...].

    Берётся только раздел «Ход работы»: метка в другом разделе была бы
    цитатой или сценарием, а не адресом дежурного. Файл без своего раздела
    молчит: записи туда и не писались.
    """
    found = []
    directory = Path(root) / "docs" / "tasks"
    try:
        names = sorted(p.name for p in directory.glob("*.md"))
    except OSError:
        return found
    for name in names:
        task = name[:-3] if name.endswith(".md") else name
        try:
            text = (directory / name).read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        for ln in stage_lines(text):
            why = parse_mark(ln)
            if why is not None:
                found.append((task, why, ln.strip()))
    return found


def state_path(home=None):
    """Путь памяти подъёма: дом из ключа, иначе ~ из DUTY_STATE."""
    if home is not None:
        return Path(home) / ".devkit" / "watch.duty"
    return Path(os.path.expanduser(DUTY_STATE))


def read_state(home=None):
    """Память подъёма: множество подписей, чей исход зафиксирован.

    Занятость это результат разбора, а не его запуск (DK-1454): вопрос
    человеку, черновик задачи либо потолок попыток. Записи попыток сюда не
    входят, и незакрытый исход поднимается снова.
    """
    sigs = set()
    for rec in _read_records(home):
        sig = rec.get("sig")
        if sig and rec.get("done"):
            sigs.add(sig)
    return sigs


def read_tries(home=None):
    """Число попыток по подписям: {sig: сколько раз поднимали без исхода}.

    Попытка это запуск без результата либо отказ запуска. Записи старого
    формата тоже попытки: в них лежат запуски, подписи не занимавшие.
    """
    tries = {}
    for rec in _read_records(home):
        sig = rec.get("sig")
        if sig and not rec.get("done"):
            tries[sig] = tries.get(sig, 0) + 1
    return tries


def _read_records(home=None):
    try:
        text = state_path(home).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return []
    out = []
    for ln in text.splitlines():
        try:
            rec = json.loads(ln)
        except ValueError:
            continue
        if isinstance(rec, dict):
            out.append(rec)
    return out


def write_state(sigs, home=None):
    """Дописывает занятые подписи в память подъёма, старые не переписывает."""
    path = state_path(home)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with open(str(path), "a", encoding="utf-8") as f:
            for sig in sorted(sigs):
                f.write(json.dumps({"when": datetime.now().isoformat(timespec="seconds"),
                                    "sig": sig, "done": "исход зафиксирован"},
                                   ensure_ascii=False) + "\n")
    except OSError:
        pass


def write_try(sig, drafts=None, home=None):
    """Пишет попытку подъёма: запуск без исхода (снимок черновиков при нём)
    либо отказ запуска. Подпись остаётся незанятой, повтор идёт тиком."""
    rec = {"when": datetime.now().isoformat(timespec="seconds"), "sig": sig}
    if drafts is None:
        rec["fail"] = True
    else:
        rec["run"] = True
        rec["drafts"] = sorted(drafts)
    path = state_path(home)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with open(str(path), "a", encoding="utf-8") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
    except OSError:
        pass


def ask_flag(root, task):
    """Лежит ли признак вопроса задачи: исход «вопрос человеку» зафиксирован
    тем, кто его задал, и дежурному тут разбирать нечего."""
    return os.path.isfile(os.path.join(str(root), ".devkit", "chat",
                                       "task-%s.ask" % str(task).upper()))


def draft_names(root):
    """Имена черновиков корня на сейчас."""
    try:
        return sorted(p.name for p in
                      (Path(root) / "docs" / "tasks" / "drafts").glob("*.md"))
    except OSError:
        return []


def fresh_draft(root, known, since):
    """Имя нового черновика в корне либо пусто. Известные по снимку
    пропускаются; записи без снимка, старого формата, границей имеют время
    своей попытки. Без границы вовсе черновик не узнаётся: старые черновики
    чужих задач не должны закрывать чужую подпись."""
    now = draft_names(root)
    if known is not None:
        for name in now:
            if name not in known:
                return name
        return ""
    if since is None:
        return ""
    try:
        edge = datetime.fromisoformat(since)
    except ValueError:
        return ""
    drafts = Path(root) / "docs" / "tasks" / "drafts"
    for name in now:
        try:
            if datetime.fromtimestamp((drafts / name).stat().st_mtime) > edge:
                return name
        except OSError:
            continue
    return ""


def draft_base(sig, home=None):
    """Граница черновиков подписи: (снимок имён, время попытки) последней
    записи. Снимок точнее времени: черновик, созданный между попытками чужим
    заходом, временем закрыл бы подпись зря."""
    names, when = None, None
    for rec in _read_records(home):
        if rec.get("sig") != sig or rec.get("done"):
            continue
        if "drafts" in rec:
            names = rec["drafts"]
        when = rec.get("when", when)
    return names, when


def remember(sig, home=None):
    """Кладёт подпись в память подъёма."""
    write_state({sig}, home=home)


def duty_root(root, call=None, taskctl=None, home=None, act=True):
    """Поднимает дежурного по неразобранным записям исхода корня.

    Подпись отказа занимает зафиксированный результат разбора, а не запуск
    (DK-1454): вопрос человеку либо черновик задачи. Запуск без исхода это
    попытка, и повтор идёт ближайшим тиком до результата либо до потолка
    попыток; потолок добивается громким зовом, чтобы не долбить наружу
    вечно.

    Возврат это строки отчёта, как у пробуждения и страховки. `act` гасит
    сам подъём и запись памяти: так стенд смотрит решение захода, не трогая
    наружу.
    """
    call = subprocess.run if call is None else call
    lines = []
    marks = task_marks(root)
    if not marks:
        return lines
    known = read_state(home=home)
    tries = read_tries(home=home)
    fresh = set()
    seen = set()
    for task, why, raw in marks:
        sig = signature(why)
        if sig in known or sig in seen:
            # Отказ уже разобран либо уже взят в этом заходе: граница DK-1115,
            # одинаковые отказы в разбор не входят.
            lines.append("корень %s: отказ «%s» уже известен, мимо разбора "
                         "дежурного (DK-1115)" % (root, sig))
            continue
        seen.add(sig)
        if ask_flag(root, task):
            fresh.add(sig)
            lines.append("корень %s: по %s лежит вопрос человеку, отказ «%s» "
                         "закрыт этим вопросом" % (root, task, sig))
            continue
        base, since = draft_base(sig, home=home)
        draft = fresh_draft(root, base, since)
        if draft:
            fresh.add(sig)
            lines.append("корень %s: по %s появился черновик %s, отказ «%s» "
                         "закрыт черновиком" % (root, task, draft, sig))
            continue
        if not act:
            # Разбор без подъёма не трогает наружу и память тоже: подпись
            # остаётся незанятой, и следующий заход смотрит то же решение.
            lines.append("корень %s: задача %s ждёт дежурного, отказ «%s»"
                         % (root, task, sig))
            continue
        if tries.get(sig, 0) >= SPAWN_TRIES:
            # Потолок попыток: повтор дальше жёг бы бюджет тем же отказом, и
            # исход добивается громким зовом (DK-1454).
            note, delivered = shout("разбор дежурного не поднять",
                         "Задача %s: дежурный не оставил исхода за %d попыток. "
                         "Исход записи «%s». Громкий зов это хвост исчерпанного "
                         "повтора." % (task, tries[sig], sig),
                         root, call=call, task=task)
            fresh.add(sig)
            lines.append("корень %s: дежурный по %s не оставил исхода за %d попыток, "
                         "подпись «%s» закрыта потолком; %s; %s"
                         % (root, task, tries[sig], sig, note,
                            "громкий зов человеку ушёл" if delivered else
                            "уведомление не доставлено"))
            continue
        before = draft_names(root)
        said, code = spawn(root, task, why, call=call, taskctl=taskctl)
        if code == 3:
            lines.append("корень %s: дежурный по %s ждёт свободного замка; "
                         "исход записи «%s»; запуск: %s; повтор тиком"
                         % (root, task, sig, said))
            continue
        if code == 0:
            # Запуск кончился сам: исход обязан лежать в корне, вопросом либо
            # черновиком. Лежит и не лежит, разбирать некому: попытка пишется,
            # и повтор поднял бы дежурного заново (DK-1454).
            if ask_flag(root, task):
                fresh.add(sig)
                lines.append("корень %s: дежурный по %s кончился вопросом "
                             "человеку, отказ «%s» закрыт вопросом; %s"
                             % (root, task, sig, said))
                continue
            draft = fresh_draft(root, before, None)
            if draft:
                fresh.add(sig)
                lines.append("корень %s: дежурный по %s кончился черновиком %s, "
                             "отказ «%s» закрыт черновиком; %s"
                             % (root, task, draft, sig, said))
                continue
            write_try(sig, drafts=before, home=home)
            lines.append("корень %s: дежурный по %s прошёл без исхода, отказ «%s» "
                         "(попытка %d из %d), повтор тиком; %s"
                         % (root, task, sig, tries.get(sig, 0) + 1,
                            SPAWN_TRIES, said))
            continue
        # Разбор не начался. Подпись не пишется: повтор идёт ближайшим тиком
        # и исход не виснет (замечание ревью). Постоянный сбой добивается
        # потолком попыток с реальным зовом.
        n = tries.get(sig, 0) + 1
        write_try(sig, home=home)
        if n < SPAWN_TRIES:
            lines.append("корень %s: дежурного по %s не поднять, отказ «%s» "
                         "(попытка %d из %d), повтор тиком; %s"
                         % (root, task, sig, n, SPAWN_TRIES, said))
        else:
            lines.append("корень %s: дежурного по %s не поднять, отказ «%s» "
                         "(попытка %d из %d), следующим тиком потолок добивается "
                         "громким зовом; %s"
                         % (root, task, sig, n, SPAWN_TRIES, said))
    if fresh and act:
        remember_many(fresh, home=home)
    return lines


def remember_many(sigs, home=None):
    write_state(sigs, home=home)


def spawn(root, task, why, call=None, taskctl=None):
    """Поднимает голову дежурного заказом `taskctl run`.

    Лестница и замок остаются у taskctl: вторая копия тут не нужна, а отказ
    занятого замка значит, что по этой задаче уже идёт работа, и дежурный
    к ней не подмешивается. Возврат это (слова, код).
    """
    call = subprocess.run if call is None else call
    bin = taskctl or which("taskctl")
    if not bin:
        return "бинаря taskctl нет ни в PATH, ни в каталогах релиза", 2
    order = duty_order(task, why)
    try:
        p = call([bin, "-C", str(root), "run", task, "--order", order],
                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return str(e), 2
    return " ".join((p.stdout or "").split()), p.returncode


def shout(title, body, root, call=None, task=None):
    """Громкий зов уведомителем: хвост неудавшегося подъёма.

    Тот же уведомитель, что у сторожа, громкий уровень по умолчанию.
    """
    call = subprocess.run if call is None else call
    notif = str(Path(__file__).resolve().parents[2] / "hooks" / "notify.py")
    argv = [sys.executable, notif]
    if task:
        argv += ["--task", task]
    try:
        p = call(argv + [title, body], cwd=str(root),
                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return str(e), False
    return ((p.stdout or "").strip() or ("отправлено" if p.returncode == 0
            else "код %d" % p.returncode)), p.returncode == 0


def which(name):
    """Бинарь связки из PATH или из каталогов релиза (как у lift.which)."""
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
