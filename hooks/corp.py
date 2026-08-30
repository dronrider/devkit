#!/usr/bin/env python3
"""Корп-контур на стороне хуков: признак контура, рубеж следов и цепочка.

Признак контура тут тот же, что у утилит devkit (tools/taskctl/corp.go, corpLocal):
ключ devkit.local в конфиге клона, относительный путь считается от родителя
git-common-dir, пустой ответ значит домашний проект. Второго разбора конфига
здесь нет, есть перевод той же функции на python: хуки на shell и python, звать
Go-утилиту из них не за что.

Рубеж следов девкита короткий и потому нелживый: локальный ID доски боковой
директории (префикс берётся из её шапки) и путь самой боковой директории. Что
контур считает следом сверх этого, дописывается ключом traces в привязке. На
доске, чей префикс совпал с ключом проекта в трекере, правило про локальный ID
снимается: там ID строки и ключ тикета неотличимы, и рубеж валил бы каждый
коммит по конвенции компании (DK-124).

Цепочка нужна потому, что core.hooksPath в корп-клоне выключил бы .git/hooks
целиком вместе с чужими хуками проекта. Вместо этого в .git/hooks кладётся
обёртка с маркером в шапке: она гоняет проверки devkit, а следом чужой хук,
переехавший при подключении в соседнее имя <хук>.chained. Ненулевой код любой
стороны валит коммит. Перезаписанная чужим инсталлером обёртка узнаётся по
пропавшему маркеру, чинит её повторный install_chain.
"""
import os
import re
import shutil
import stat
import subprocess

# Маркер своего файла. Первую строку обёртки занимает shebang, без него git не
# запустит хук, поэтому маркер стоит следующей строкой, а опознание смотрит
# шапку файла.
MARKER = "devkit-corp-chain"
MARKER_LINES = 3

BOARD = os.path.join("docs", "TASKS.md")
TRACKER = os.path.join(".devkit", "tracker.local")
# Каталог обвязки клона это ссылка на боковую директорию (DK-583). В дереве она
# лежит открыто, чтобы доску видели поиск редактора и сессия, поэтому в индекс
# она попадает одним «git add .», и стережёт её тут pre-commit.
LINK_DIR = ".devkit"
# Шапка блока, которым devkit пишет свои строки в .git/info/exclude. Тонкие
# файлы харнесов зависят от включённых профилей, и перечислять их в хуке нечем:
# что спрятано этим блоком, то и есть обвязка.
EXCLUDE_MARK = "# devkit: обвязка корп-контура, в индекс не едет."

PREFIX_RE = re.compile(r"\(префикс ([A-ZА-Я]+)\)")

WRAPPER = """#!/bin/sh
# %(marker)s: проверки devkit, следом чужой хук проекта <хук>.chained.
# Файл раскладывает «devkitctl corp», правится он не здесь, а в devkit
# (hooks/corp.py). Ненулевой код любой стороны валит коммит.
name=${0##*/}
hooks=%(hooks)s
if [ -x "$hooks/$name" ]; then
    "$hooks/$name" "$@" || exit $?
fi
case $name in
    pre-commit) python3 "$hooks/check-traces.py" --staged || exit $? ;;
    commit-msg) python3 "$hooks/check-traces.py" --msg "$1" || exit $? ;;
esac
chained=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/$name.chained
if [ -x "$chained" ]; then
    exec "$chained" "$@"
fi
exit 0
"""


def git(start, *args):
    """Ответ git одной строкой. Пусто значит, что спрашивать было негде: вне
    репозитория и без ключа это домашнее поведение, а не поломка."""
    try:
        out = subprocess.run(["git", "-C", start] + list(args),
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    except OSError:
        return ""
    if out.returncode != 0:
        return ""
    return out.stdout.decode("utf-8", "replace").strip()


def checkout(start):
    """Директория основного чекаута, то есть родитель git-common-dir. От неё
    считается относительный путь редиректа: у дерева ветки вершина своя, и от
    неё путь сошёлся бы, только пока дерево лежит сиблингом проекта."""
    common = git(start, "rev-parse", "--git-common-dir")
    if not common:
        return ""
    if not os.path.isabs(common):
        common = os.path.join(os.path.abspath(start), common)
    return os.path.dirname(os.path.normpath(common))


def local_dir(start="."):
    """Боковая директория корп-контура, пусто у домашнего проекта."""
    val = git(start, "config", "--local", "--get", "devkit.local")
    if not val:
        return ""
    if os.path.isabs(val):
        return os.path.normpath(val)
    base = checkout(start)
    if not base:
        return ""
    return os.path.normpath(os.path.join(base, val))


def git_common(start):
    """Каталог .git репозитория (общий у всех деревьев ветки)."""
    common = git(start, "rev-parse", "--git-common-dir")
    if not common:
        return ""
    if not os.path.isabs(common):
        common = os.path.join(os.path.abspath(start), common)
    return os.path.normpath(common)


def hidden_names(start="."):
    """Имена, спрятанные блоком devkit в .git/info/exclude: тонкие файлы
    контекста харнесов. Читается ровно блок, чужие строки exclude не наши."""
    common = git_common(start)
    if not common:
        return []
    path = os.path.join(common, "info", "exclude")
    out, inside = [], False
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for raw in f:
                line = raw.strip()
                if line == EXCLUDE_MARK:
                    inside = True
                    continue
                if not line or line.startswith("#"):
                    inside = False
                    continue
                if inside:
                    out.append(line)
    except OSError:
        return []
    return out


def staged_rig(start="."):
    """Обвязка devkit, оказавшаяся в индексе корп-коммита. Ссылка .devkit и
    тонкий файл контекста живут в дереве клона, а чужому репозиторию не нужны:
    «git add .» кладёт их в индекс молча, и это последнее место, где видно."""
    out = git(start, "diff", "--cached", "--name-only")
    if not out:
        return []
    watch = set(hidden_names(start)) | {LINK_DIR}
    found = []
    for path in out.split("\n"):
        path = path.strip()
        if not path:
            continue
        head = path.split("/")[0]
        if (path in watch or head in watch) and path not in found:
            found.append(path)
    return found


def board_prefix(local):
    """Префикс ID из шапки доски боковой директории. Читается ровно шапка, до
    первой секции: ниже начинаются строки задач, и «(префикс XX)» в чужом
    заголовке задачи за шапку сойти не должен."""
    try:
        with open(os.path.join(local, BOARD), encoding="utf-8", errors="replace") as f:
            for line in f:
                if line.startswith("## "):
                    break
                m = PREFIX_RE.search(line)
                if m:
                    return m.group(1)
    except OSError:
        pass
    return ""


def tracker_value(local, key):
    """Значение ключа из привязки боковой директории. Формат плоский,
    «key = value» с решёткой на комментарий, как у deploy.local."""
    try:
        with open(os.path.join(local, TRACKER), encoding="utf-8", errors="replace") as f:
            for raw in f:
                line = raw.strip()
                if not line or line.startswith("#"):
                    continue
                name, sep, val = line.partition("=")
                if not sep or name.strip() != key:
                    continue
                val = val.strip()
                if len(val) >= 2 and val[0] == val[-1] and val[0] in "\"'":
                    val = val[1:-1]
                return val
    except OSError:
        pass
    return ""


def prefix_collision(local):
    """Совпадает ли префикс доски с ключом проекта в привязке. На такой паре
    локальный ID доски и ключ тикета это одна и та же строка, и отличить их
    рубеж не может ничем: номера у доски свои, у трекера свои, и «AC-001»
    бывает и строкой доски, и тикетом (DK-124)."""
    prefix = board_prefix(local)
    key = tracker_value(local, "key").strip()
    return bool(prefix) and prefix.upper() == key.upper()


def patterns(local):
    """Перечень следов: чем ловим и как называем находку."""
    out = []
    prefix = board_prefix(local)
    # Ключ тикета в сообщении корп-коммита это конвенция компании, а не след
    # (DK-074, «Граница доски и тикета»), и на совпадающих префиксах правило
    # про локальный ID валило бы каждый нормальный коммит. Рубеж, который врёт,
    # отключают целиком вместе с остальными проверками (git commit
    # --no-verify), поэтому он честно снимает то правило, которое на этой паре
    # решить не может, и остаётся при пути боковой директории и словах контура.
    # Что рубеж на проекте ослаблен, говорит devkitctl doctor.
    if prefix and not prefix_collision(local):
        out.append(("локальный ID доски", re.compile(r"\b%s-[0-9]+\b" % re.escape(prefix))))
    names = [local]
    # Коротким именем зовётся директория контура («acme-local»), а не проект
    # внутри неё: боковая директория теперь общая на контур, и её последнее
    # звено это имя самого проекта, слово в корп-репозитории обычное (DK-583).
    parts = [p for p in os.path.normpath(local).split(os.sep) if p]
    mark = ""
    for part in reversed(parts):
        if part.endswith("-local"):
            mark = part
            break
    if not mark and parts:
        mark = parts[-1]
    if mark:
        names.append(mark)
    out.append(("путь боковой директории",
                re.compile("|".join(re.escape(n) for n in names))))
    for word in tracker_value(local, "traces").split(","):
        word = word.strip()
        if word:
            out.append(("слово контура", re.compile(re.escape(word), re.IGNORECASE)))
    return out


def scan(rules, text):
    """Виды следов, найденные в строке. Порядок правил сохраняется, повторов
    одного вида в строке не бывает: находка называет вид, а не место."""
    kinds = []
    for kind, rx in rules:
        if rx.search(text) and kind not in kinds:
            kinds.append(kind)
    return kinds


def is_chain(path):
    """Наш ли это файл: маркер стоит в шапке обёртки. Пропавший маркер значит,
    что обёртку перезаписал чужой инсталлер."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for _ in range(MARKER_LINES):
                line = f.readline()
                if not line:
                    break
                if MARKER in line:
                    return True
    except OSError:
        pass
    return False


def quote(path):
    return "'" + path.replace("'", "'\\''") + "'"


def wrapper_text(devkit_hooks):
    return WRAPPER % {"marker": MARKER, "hooks": quote(os.path.abspath(devkit_hooks))}


def install_chain(hooks_dir, name, devkit_hooks):
    """Развернуть цепочку для хука name в .git/hooks клона.

    Чужой хук переезжает в <имя>.chained, на его место встаёт обёртка. Прогон
    повторяемый: свою обёртку функция узнаёт по маркеру и чужим хуком её не
    считает, а совпадающее содержимое не переписывает. Отдаёт пару (что стало,
    куда переехал чужой хук): «установлена», «обновлена» или «на месте».
    """
    os.makedirs(hooks_dir, exist_ok=True)
    path = os.path.join(hooks_dir, name)
    chained = ""
    if os.path.exists(path) and not is_chain(path):
        chained = path + ".chained"
        shutil.move(path, chained)
        mode = os.stat(chained).st_mode
        os.chmod(chained, mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    text = wrapper_text(devkit_hooks)
    if os.path.exists(path):
        with open(path, encoding="utf-8", errors="replace") as f:
            same = f.read() == text
        if same:
            return "на месте", chained
        state = "обновлена"
    else:
        state = "установлена"
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    os.chmod(path, 0o755)
    return state, chained
