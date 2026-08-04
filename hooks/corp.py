#!/usr/bin/env python3
"""Корп-контур на стороне хуков: признак контура, рубеж следов и цепочка.

Признак контура тут тот же, что у утилит devkit (taskctl/corp.go, corpLocal):
ключ devkit.local в конфиге клона, относительный путь считается от родителя
git-common-dir, пустой ответ значит домашний проект. Второго разбора конфига
здесь нет, есть перевод той же функции на python: хуки на shell и python, звать
Go-утилиту из них не за что.

Рубеж следов девкита короткий и потому нелживый: локальный ID доски боковой
директории (префикс берётся из её шапки) и путь самой боковой директории. Что
контур считает следом сверх этого, дописывается ключом traces в привязке.

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


def patterns(local):
    """Перечень следов: чем ловим и как называем находку."""
    out = []
    prefix = board_prefix(local)
    if prefix:
        out.append(("локальный ID доски", re.compile(r"\b%s-[0-9]+\b" % re.escape(prefix))))
    names = [local]
    base = os.path.basename(local)
    if base:
        names.append(base)
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
