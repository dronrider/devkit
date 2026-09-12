"""Уборка временных деревьев прогона, брошенных упавшим процессом (DK-968).

Слияние, regcheck и обкатка сценария выкладывают проверяемый коммит в
одноразовый git worktree под каталогом временных файлов. Своё дерево прогон
снимает сам, в том числе по ловимому сигналу, а несловимый KILL сносит его
мимо всякой уборки. Каталог при этом остаётся на месте, а запись о дереве
живёт в списке git насовсем. Prunable git считает только те записи, чей
каталог пропал, и `git worktree prune` такую не трогает.

Каталог прогона узнаётся по метке владельца, которую кладёт internal/freshtree.
Метка это файл с номером процесса, корнем репозитория и путём дерева. Живой
процесс это идущий прогон, и его каталог не трогается. Метки нет у каталогов,
оставшихся от сборок до этой правки, и такие разбираются по имени и возрасту.
"""
import os
import re
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

import say

# Имя метки владельца то же, что OwnerFile в internal/freshtree.
OWNER = "owner"

# Префиксы каталогов прогона, названные поимённо. Под тем же корнем лежат
# также каталоги редактора, браузера и системы, и уборка их не должна
# касаться.
PREFIXES = ("shipctl-merge-", "regcheck-", "taskctl-rehearse-")

# Возраст, с которого каталог без метки считается брошенным. Метку кладут все
# прогоны начиная с DK-968. Без метки остаются каталоги старых сборок и
# каталог, чей прогон умер между mkdir и записью метки. Прогон слияния с
# тестами идёт минуты, час это запас поверх самого долгого.
NO_OWNER_AGE = 3600

TREES = ("дерево", "дерева", "деревьев")


def pid_alive(pid):
    """Жив ли процесс. Чужой процесс тоже считается живым. Сигнал 0 отдаёт
    отказ доступа, а не отсутствие, и это ответ «такой процесс есть»."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return True
    return True


def read_owner(path):
    """Метка владельца словарём; пустой словарь значит «метки нет»."""
    data = {}
    try:
        text = Path(path, OWNER).read_text(encoding="utf-8")
    except OSError:
        return data
    for ln in text.splitlines():
        if "=" in ln:
            k, v = ln.split("=", 1)
            data[k.strip()] = v.strip()
    return data


def abandoned(root=None, now=None):
    """Брошенные каталоги прогонов тройками (каталог, репозиторий, дерево).

    Репозиторий и путь дерева берутся из метки. Снимать запись надо в том
    репозитории, чей прогон её завёл. Без метки известен один каталог, и тогда
    оба поля пустые. Такой каталог убирается с диска, а запись о дереве
    снимает `git worktree prune`, потому что каталога не стало.
    """
    now = now or time.time()
    base = Path(root or tempfile.gettempdir())
    out = []
    try:
        entries = sorted(base.iterdir())
    except OSError:
        return out
    for path in entries:
        if not path.is_dir() or not path.name.startswith(PREFIXES):
            continue
        owner = read_owner(path)
        pid = owner.get("pid", "")
        if not re.fullmatch(r"[0-9]+", pid):
            try:
                age = now - path.stat().st_mtime
            except OSError:
                continue
            if age < NO_OWNER_AGE:
                continue
            out.append((str(path), "", ""))
            continue
        if pid_alive(int(pid)):
            continue
        out.append((str(path), owner.get("root", ""), owner.get("tree", "")))
    return out


def drop(path, repo, tree):
    """Снимает один брошенный каталог, запись дерева в репозитории и сам
    каталог. Отказ git уборку не роняет. Каталог всё равно уходит, и запись
    после этого снимает `git worktree prune`."""
    if repo and tree:
        subprocess.run(["git", "-C", repo, "worktree", "remove", "--force", tree],
                       capture_output=True, text=True)
    shutil.rmtree(path, ignore_errors=True)
    if repo:
        subprocess.run(["git", "-C", repo, "worktree", "prune"],
                       capture_output=True, text=True)


def prunable(repo):
    """Сколько записей о деревьях в репозитории git считает висячими. У такой
    записи каталога уже нет, и держится она до `git worktree prune`."""
    if not repo:
        return 0
    out = subprocess.run(["git", "-C", str(repo), "worktree", "list", "--porcelain"],
                         capture_output=True, text=True).stdout
    return sum(1 for ln in out.splitlines() if ln.startswith("prunable"))


def check(fix, root=None, now=None, repo=None):
    """Находка доктора про брошенные деревья и её починка по --fix.

    repo это репозиторий, в котором снимаются висячие записи. Каталог прогона
    без метки владельца уносится вместе с ними. Сам он снять запись не может:
    из каталога не видно, чей это репозиторий.
    """
    left = abandoned(root, now)
    stale = prunable(repo)
    if not left and not stale:
        return [], []
    count = say.counted(len(left) + stale, TREES)
    if not fix:
        return ["временных деревьев от упавших прогонов: %s; висят в "
                "`git worktree list`, держат старые коммиты от сборки мусора и "
                "занимают диск; убрать: devkitctl doctor --fix" % count], []
    for path, owner_repo, tree in left:
        drop(path, owner_repo or repo, tree)
    if repo:
        subprocess.run(["git", "-C", str(repo), "worktree", "prune"],
                       capture_output=True, text=True)
    return [], ["снято временных деревьев прогона: %s" % count]
