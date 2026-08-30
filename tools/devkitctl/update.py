#!/usr/bin/env python3
"""Установка и обновление devkit готовыми бинарями: одна команда на оба случая.

Решение в docs/lld/DK-149-binary-delivery.md, разделы «Решение 4» (шаги
команды), «Решение 6» (обёртка devkitctl) и «Решение 7» (каталог назначения).
На голой машине команда просто проходит все шаги впервые, и отличается
установка одним ключом --pin, которым человек разрешает отвязать свежий клон от
ветки.

Тулчейна тут не нужно: бинари приезжают тарболлом релиза, а качает их
urllib из stdlib. Он есть везде, где есть python3, и сам читает https_proxy из
окружения, а curl на голом linux бывает не поставлен.

Адрес релизов подменяется переменной DEVKIT_RELEASE_BASE. Это шов для тестов и
для прогона в чистом окружении: гитхаб в тестах не участвует, релиз им отдаёт
локальный сервер.
"""
import hashlib
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

import build
import say

RELEASE_BASE = "https://github.com/dronrider/devkit/releases/download"
RELEASE_ENV = "DEVKIT_RELEASE_BASE"
BIN_ENV = "DEVKIT_BIN"
# Каталоги назначения по порядку: первый, который уже стоит в PATH, а не
# первый существующий. Имя ~/go/bin говорит про тулчейн, которого на машине
# потребителя может не быть вовсе, поэтому на чистой машине выигрывает
# общепринятое ~/.local/bin.
BIN_DIRS = ("~/go/bin", "~/.local/bin")
WRAPPER = "devkitctl"
MARKER = "# devkit:generated"
WRAPPER_TEXT = '#!/bin/sh\n%s\nexec python3 "%s" "$@"\n'
# Флаг самоперезапуска: git-часть уже сделана, идёт вторая половина. Он же
# признак того, что код тега вообще знает про такую передачу работы.
RESTART_FLAG = "--restarted"
TMP_PREFIX = ".devkit-update-"
# Рефспек фетча тегов: только релизные v*, поимённо, а не --tags со всем
# содержимым refs/tags/*. В devkit живёт служебный тег deployed, который
# shipctl двигает на каждом выкате, и голый --tags после первого же расхождения
# отвечает «would clobber existing tag» на чужой тег, который команде не нужен
# вовсе. Один рефспек тут не огораживает: у git есть автослежение за тегами, и
# посторонний тег на уже известном клону коммите приезжает даже при явном
# v*-рефспеке, рефспек его не называет, а автослежение об этом не спрашивает.
# Огораживает связка с --no-tags на вызове fetch: она глушит автослежение, а
# сами v*-теги рефспек продолжает тянуть и пруннить именем, deployed не трогая
# ни при каком расхождении с origin.
TAG_REFSPEC = "refs/tags/v*:refs/tags/v*"
HOW = "python3 %s/tools/devkitctl/devkitctl.py"
# Три формы слова для счёта в отчёте: одна утилита, две утилиты, шесть утилит.
UTILS = ("утилита", "утилиты", "утилит")
# Порог давности похода за тегами. Мера косвенная, потому что прямой нет, и
# порог редкий: теги ставит человек и нечасто.
FETCH_MAX_AGE = 14 * 24 * 3600


# Доктор спрашивает у git одно и то же по многу раз за прогон: режим чекаута,
# теги и коммиты читаются и в сводке, и в проверке бинарей, и в находке про
# релиз. Каждый git это свой процесс, поэтому ответы чтений запоминаются, пока
# кеш включён, а любая меняющая команда его чистит: fetch и checkout меняют
# репозиторий, и теги после fetch читаются заново. Кеш не включён по умолчанию:
# прямые вызовы из тестов ходят в git между собственными мутациями мимо этого
# шлюза, и обязанность включать лежит на команде, у которой повторы внутри.
# «tag <имя>» и «symbolic-ref <имя> <цель>» меняют репозиторий так же, как
# fetch и checkout, а первым словом от чтений не отличаются, поэтому чтение
# опознаётся по первому аргументу после подкоманды: у чтений это флаг или rev,
# у мутации имя.
GIT_MUTATIONS = frozenset(("fetch", "checkout", "commit", "push", "reset",
                           "pull", "clone", "add", "rebase", "merge",
                           "cherry-pick", "revert", "stash"))
_git_cache = {}
_git_cached = False


def _is_read(args):
    """Читает ли команда репозиторий, не меняя его."""
    if not args:
        return False
    head = str(args[0])
    if head in GIT_MUTATIONS:
        return False
    if head in ("tag", "symbolic-ref") and len(args) > 1 \
            and not str(args[1]).startswith("-"):
        return False
    return True


def git_cache(on):
    """Включить или выключить кеш git-чтений и вычистить его."""
    global _git_cached
    _git_cached = on
    _git_cache.clear()


def git(devkit, *args):
    key = (str(devkit),) + tuple(str(a) for a in args)
    read = _is_read(args)
    if read and _git_cached and key in _git_cache:
        return _git_cache[key]
    p = subprocess.run(["git", "-C", str(devkit)] + [str(a) for a in args],
                       capture_output=True, text=True)
    res = (p.returncode, (p.stdout + p.stderr).strip())
    if _git_cached:
        if read:
            _git_cache[key] = res
        else:
            _git_cache.clear()
    return res


def bin_dir():
    """Каталог назначения бинарей и обёртки (решение 7).

    Профиль оболочки при этом не правится: это единственный файл в списке
    раскладки, который человек ведёт сам, а правка в уже запущенной сессии всё
    равно не действует. Каталог мимо PATH уезжает в «осталось сделать» и в
    находку доктора.
    """
    named = os.environ.get(BIN_ENV)
    if named:
        return Path(os.path.expanduser(named))
    seen = set()
    for entry in os.environ.get("PATH", "").split(os.pathsep):
        if entry:
            seen.add(os.path.normpath(os.path.expanduser(entry)))
    for cand in BIN_DIRS:
        path = Path(os.path.expanduser(cand))
        if os.path.normpath(str(path)) in seen:
            return path
    return Path(os.path.expanduser(BIN_DIRS[-1]))


def in_path(directory):
    directory = os.path.normpath(str(directory))
    for entry in os.environ.get("PATH", "").split(os.pathsep):
        if entry and os.path.normpath(os.path.expanduser(entry)) == directory:
            return True
    return False


def asset_name(version, pair=None):
    """Имя тарболла своей платформы. Вторым отдаёт находку, когда пары нет в релизе."""
    pair = pair or build.host_pair()
    if pair not in build.PLATFORMS:
        pairs = ", ".join("%s/%s" % p for p in build.PLATFORMS)
        return None, ("релиза под пару %s/%s нет: devkit выкладывает четыре пары (%s), "
                      "и эта не из них" % (pair[0], pair[1], pairs))
    return build.ASSET % (version, pair[0], pair[1]), None


def release_base():
    return os.environ.get(RELEASE_ENV) or RELEASE_BASE


def latest_tag(devkit):
    """Новейший релизный тег, известный клону. Спрашивается git, а не API гитхаба:
    так обходятся и лимит неавторизованных запросов, и разбор JSON, и лишний хост
    в диагностике сетевых отказов.
    """
    rc, out = git(devkit, "tag", "--list", "v*", "--sort=-v:refname")
    if rc != 0 or not out:
        return ""
    return out.splitlines()[0].strip()


def head_tag(devkit):
    """Релизный тег, на котором стоит HEAD. Служебные теги (deployed) не в счёт."""
    rc, out = git(devkit, "tag", "--points-at", "HEAD", "--list", "v*", "--sort=-v:refname")
    if rc != 0 or not out:
        return ""
    return out.splitlines()[0].strip()


def current_branch(devkit):
    """Имя ветки либо пустая строка на отвязанном HEAD."""
    rc, out = git(devkit, "symbolic-ref", "--quiet", "--short", "HEAD")
    return out.strip() if rc == 0 else ""


def head_commit(devkit):
    rc, out = git(devkit, "rev-parse", "--short=12", "HEAD")
    return out.strip() if rc == 0 else ""


def checkout_mode(devkit):
    """Режим чекаута одной строкой: на релизе, на ветке впереди релиза, отвязан.

    Доктор называет его вслух рядом со сверкой версий: расхождение бинаря с
    чекаутом на машине разработчика это норма до пересборки, а на машине
    потребителя поломка, и без режима одно читается как другое.
    """
    if not head_commit(devkit):
        return "не git-чекаут: сверять версию бинарей не с чем"
    tag, branch, latest = head_tag(devkit), current_branch(devkit), latest_tag(devkit)
    if branch and tag:
        return "на ветке %s, и на её вершине стоит релиз %s" % (branch, tag)
    if branch and latest:
        return "на ветке %s, впереди релиза %s" % (branch, latest)
    if branch:
        return "на ветке %s, релизных тегов в клоне нет" % branch
    if tag:
        return "на релизе %s" % tag
    return "отвязан на коммите %s, ни на одном релизном теге" % head_commit(devkit)


def on_release(devkit):
    """HEAD отвязан и стоит ровно на релизном теге v*: чекаут потребителя.

    Тот же признак, что различает ветку «на релизе» в checkout_mode (тег есть,
    имени ветки нет), вынесен отдельно: doctor гасит по нему находки, которые
    адресованы только тому, кто devkit правит, а машине потребителя (поставила
    devkit по CONNECT.md, чекаут детач на теге) не нужны и читаются как
    «поставилось криво». На ветке разработчика признак всегда ложный.
    """
    return bool(head_tag(devkit)) and not current_branch(devkit)


def fetch_head(devkit):
    """Файл FETCH_HEAD основного чекаута: по нему меряется давность похода за тегами."""
    rc, out = git(devkit, "rev-parse", "--git-dir")
    if rc != 0:
        return None
    path = Path(out.strip())
    if not path.is_absolute():
        path = Path(devkit) / path
    return path / "FETCH_HEAD"


def check_release(devkit):
    """Две находки про вышедший релиз: точная и косвенная.

    Точная сравнивает тег чекаута с новейшим тегом, известным клону, и порога у
    неё нет. Косвенная отвечает на второй вопрос, давно ли вообще спрашивали, и
    меряется давностью FETCH_HEAD. В сеть тут не ходит ни та, ни другая: обе
    считаются по локальному клону, и диагностика остаётся рабочей без сети.
    """
    findings = []
    if not head_commit(devkit):
        # Не чекаут вовсе (распакованный архив, свежий git init без коммитов):
        # про релизы тут сказать нечего, и звать за тегами тоже некуда.
        return findings
    tag, latest = head_tag(devkit), latest_tag(devkit)
    if tag and latest and tag != latest:
        findings.append("на машине стоит devkit %s, а вышел %s: обновиться одной командой, "
                        "devkitctl update" % (tag, latest))
    path = fetch_head(devkit)
    if path is None:
        return findings
    how = "спросить: devkitctl update --check"
    if not path.exists():
        findings.append("за тегами devkit не ходили ни разу (нет %s): про вышедший релиз "
                        "узнать тут неоткуда, %s" % (path, how))
    else:
        age = time.time() - os.path.getmtime(str(path))
        if age > FETCH_MAX_AGE:
            findings.append("за тегами devkit не ходили %d дней (порог %d): вышедший релиз "
                            "клону неизвестен, %s"
                            % (age // (24 * 3600), FETCH_MAX_AGE // (24 * 3600), how))
    return findings


def dirty_tracked(devkit):
    """Правка отслеживаемого: переход на тег унёс бы её. Часть меры «своя работа
    не потеряется», которая не ходит в сеть и гоняется до fetch тегов, чтобы
    человек с грязным деревом и без сети узнавал об этом первым сообщением, а
    не отказом самого fetch.
    """
    rc, out = git(devkit, "status", "--porcelain", "--untracked-files=no")
    if rc == 0 and out:
        return ("в чекауте %s правлено отслеживаемое (%s): переход на тег унёс бы правку. "
                "Спрятать её (git stash) либо закоммитить, потом повторить"
                % (devkit, out.splitlines()[0].strip()))
    return ""


def unpushed_commits(devkit):
    """Коммиты, которых не достаёт ни ветке origin, ни известному релизному тегу.
    Меряется без @{u}: у отвязанного HEAD upstream нет вовсе, и проверка через
    него отказывала бы на машине потребителя каждый раз.

    Якорь на тегах сужен до `--tags=v*` (второй круг DK-164, форвард-фикс
    исходного `--tags`), и вызывать функцию нужно только после свежего
    `git fetch --no-tags --prune origin refs/tags/v*:refs/tags/v*`, того же
    самого, каким update и так тянет релизные теги. Причина в двух дырах,
    которые голый `--tags` не видел:

    - тег с чужим именем (`git tag foo`, `git tag backup`) никогда не приезжает
      этим fetch и не должен считаться источником из origin, поэтому якорь взят
      маской `v*`, точно как рефспек;
    - но и маска сама по себе не спасает: локальный тег вида `v9.9.9-local`
      совпадает с ней, будучи придуман руками. Спасает то, что `--prune` того же
      fetch стоит на destination-паттерне `refs/tags/v*` и убирает из клона
      именно такого самозванца раньше, чем эта функция успеет на него
      посмотреть. Живой прогон это подтверждает: `git tag v9.9.9-local` в
      клоне, следом тот самый fetch, и тег пропадает. Тег с именем вне маски
      `v*` fetch не трогает никогда, и его на всякий случай дополнительно
      отсекает сам паттерн меры.

    Untracked-файлы и грязное отслеживаемое сюда не входят: это отдельная мера,
    dirty_tracked, она не про сравнение с origin.
    """
    rc, out = git(devkit, "rev-list", "--count", "HEAD", "--not", "--remotes", "--tags=v*")
    if rc == 0 and out.isdigit() and int(out) > 0:
        return ("в чекауте %s коммитов, до которых не достаёт ни одна ветка origin и ни один "
                "известный релизный тег: %s. Перевод на тег их не удалит, а вот найти их потом "
                "будет нечем: запушить и повторить" % (devkit, out))
    return ""


def work_at_risk(devkit):
    """Композиция обеих мер «своя работа не потеряется» для прямых вызовов (тесты,
    доктор), которым фетч тегов вокруг них не нужен: местные фикстуры и так уже
    держат теги, эквивалентные пришедшим из origin. Внутри run() шаги разнесены
    порознь вокруг fetch, см. unpushed_commits.
    """
    return dirty_tracked(devkit) or unpushed_commits(devkit)


def code_stamp(devkit):
    """Слепок python-части devkitctl: по нему видно, что переход на тег сменил тот
    самый код, который доделывает работу.
    """
    h = hashlib.sha256()
    here = Path(devkit) / "tools" / "devkitctl"
    for p in sorted(here.glob("*.py")):
        h.update(p.name.encode("utf-8"))
        h.update(p.read_bytes())
    return h.hexdigest()


def understands_restart(devkit):
    """Знает ли код тега про передачу второй половины работы.

    Тег, собранный до появления update, такого флага не понимает, и перезапуск в
    него уронил бы обновление разбором аргументов уже после перевода чекаута.
    Смотрится вся python-часть, а не один devkitctl.py: флаг он объявляет
    константой соседнего модуля, и по одному файлу его не видно.
    """
    here = Path(devkit) / "tools" / "devkitctl"
    for p in sorted(here.glob("*.py")):
        try:
            if RESTART_FLAG in p.read_text(encoding="utf-8", errors="replace"):
                return True
        except OSError:
            pass
    return False


def restart_now(script, args):
    # Вторую половину делает уже код тега: иначе новые бинари раскладывала бы
    # логика, прочитанная до перехода. Буферы перед этим сбрасываются руками:
    # execv не зовёт ничего на выход, и сказанное первой половиной пропало бы,
    # как только вывод уходит не в терминал, а в конвейер.
    sys.stdout.flush()
    sys.stderr.flush()
    os.execv(sys.executable, [sys.executable, str(script)] + list(args))


def binary_stamp(path):
    """Пара (версия, коммит) со слов самого бинаря. Отдельного файла состояния у
    devkit нет: записью служит бинарь, --version спрашивают у него.
    """
    try:
        p = subprocess.run([str(path), "--version"], capture_output=True, text=True, timeout=20)
    except (OSError, subprocess.SubprocessError):
        return None
    line = (p.stdout or p.stderr).strip().splitlines()
    if p.returncode != 0 or not line:
        return None
    parts = line[0].split()
    if len(parts) < 3 or not parts[2].startswith("("):
        return None
    return parts[1], parts[2].strip("()")


def installed(dest, names):
    """Что уже лежит в каталоге назначения: имя -> (версия, коммит)."""
    found = {}
    for name in names:
        path = Path(dest) / name
        if path.exists():
            stamp = binary_stamp(path)
            if stamp:
                found[name] = stamp
    return found


def path_divergence(dest, expect):
    """Утилиты, у которых в PATH выигрывает не только что положенная сборка.

    Свежий бинарь лежит в каталоге назначения, а команда на машине зовётся по
    PATH: копию из чужого каталога (стенд POC, старый go install) укладка не
    двигает, и правка молча не доезжает до команды (DK-599). Поэтому после
    укладки каждая утилита сверяется по победителю PATH, и расхождение это
    отказ, а не совет. Отсутствие имени в PATH расхождением не считается: про
    каталог мимо PATH говорят находки раскладки, здесь судится подмена.
    """
    findings = []
    for name in sorted(expect):
        won = shutil.which(name)
        if not won:
            continue
        got = binary_stamp(won)
        if got == expect[name]:
            continue
        fresh = "%s (%s) в %s" % (expect[name] + (dest,))
        if got is None:
            findings.append("%s: в PATH выигрывает %s, и на --version он не отвечает, а "
                            "свежая сборка %s осталась в тени: убрать эту копию из PATH"
                            % (name, won, fresh))
        else:
            findings.append("%s: в PATH выигрывает %s со сборкой %s (%s), а выкат положил "
                            "%s: правка до команды не доехала, убрать заслоняющую копию "
                            "из PATH" % ((name, won) + got + (fresh,)))
    return findings


def fetch_url(url, dest, timeout=60):
    """Скачать в файл. Отдаёт (код ответа, текст ошибки); 0 это «до хоста не дошли»."""
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            with open(str(dest), "wb") as f:
                shutil.copyfileobj(r, f)
    except urllib.error.HTTPError as e:
        e.close()
        return e.code, "%s: %s %s" % (url, e.code, e.reason)
    except Exception as e:  # URLError, таймаут, обрыв записи
        return 0, "%s: %s" % (url, e)
    return 200, ""


def parse_sums(text):
    sums = {}
    for ln in text.splitlines():
        parts = ln.split()
        if len(parts) == 2:
            sums[parts[1]] = parts[0]
    return sums


def verify(path, sums):
    """Сверка суммы скачанного. Подменённый байт роняет установку тут."""
    name = Path(path).name
    want = sums.get(name)
    if not want:
        return "в SHA256SUMS нет строки про %s: сверять сумму нечем, установка остановлена" % name
    got = build.sha256(path)
    if got != want:
        return ("сумма %s не сошлась (ждали %s, получили %s): скачанное подменено либо побилось "
                "по дороге, бинари остались прежними" % (name, want, got))
    return ""


def unpack(archive, into):
    """Распаковать тарболл релиза. Внутри плоско лежат утилиты, и только они."""
    with tarfile.open(str(archive)) as tf:
        members = [m for m in tf.getmembers() if m.isfile() and "/" not in m.name]
        if not members:
            return [], "в тарболле %s нет ни одного бинаря" % Path(archive).name
        if hasattr(tarfile, "data_filter"):
            tf.extractall(str(into), members=members, filter="data")
        else:
            tf.extractall(str(into), members=members)
    return sorted(m.name for m in members), ""


def place(staged, names, dest):
    """Перенести бинари в каталог назначения через os.replace.

    Именно переименованием, а не записью поверх: на linux запись в работающий
    файл даёт ETXTBSY, а переименование атомарно и уже запущенный процесс не
    трогает.
    """
    Path(dest).mkdir(parents=True, exist_ok=True)
    for name in names:
        src = Path(staged) / name
        os.chmod(str(src), 0o755)
        target = Path(dest) / name
        os.replace(str(src), str(target))
        # Время правки берётся не из тарболла, а моментом установки: в архиве
        # лежит момент сборки релиза, а исходники в чекауте появились сейчас,
        # переходом на тег, и доктор посчитал бы свежепоставленный бинарь
        # устаревшим.
        os.utime(str(target), None)
    return list(names)


def wrapper_text(devkit):
    return WRAPPER_TEXT % (MARKER, Path(devkit) / "tools" / "devkitctl" / "devkitctl.py")


def check_wrapper(devkit, fix, from_main=True):
    """Обёртка devkitctl рядом с бинарями и каталог назначения в PATH (решения 6 и 7).

    Путь в обёртке зашит абсолютным, поэтому пишет её генератор (update и
    doctor --fix), а не релиз: у каждой машины он свой, а после переезда чекаута
    обёртка обязана перевыпуститься. Маркер devkit:generated стоит по той же
    причине, что в тонких файлах проектов: по нему видно, что файл наш, и чужой
    скрипт с тем же именем доктор не затрёт.
    """
    findings, fixed = [], []
    dest = bin_dir()
    path = dest / WRAPPER
    want = wrapper_text(devkit)
    whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, класть на машину "
                                   "обёртку с непроверенной ветки нельзя; из основного чекаута: ")
    have = path.read_text(encoding="utf-8", errors="replace") if path.is_file() else None
    if have is not None and MARKER not in have:
        findings.append("в %s лежит чужой %s без пометы devkit:generated, не трогаю его; "
                        "python-часть devkit зовётся длинным путём, пока имя занято" % (dest, WRAPPER))
    elif have != want:
        why = ("нет обёртки %s в %s: python-часть зовётся длинным путём вместо имени наравне "
               "с бинарями" % (WRAPPER, dest) if have is None else
               "обёртка %s показывает не на этот чекаут devkit" % path)
        if fix and from_main:
            dest.mkdir(parents=True, exist_ok=True)
            path.write_text(want, encoding="utf-8")
            os.chmod(str(path), 0o755)
            fixed.append("установлена обёртка %s, она вызывает python-часть devkit из %s"
                         % (path, devkit))
        else:
            findings.append("%s; положить: %sdevkitctl doctor --fix" % (why, whence))
    if not in_path(dest):
        findings.append("каталог %s не в PATH: поставленное туда не зовётся по имени; дописать в "
                        "профиль оболочки export PATH=\"%s:$PATH\" и открыть новую сессию"
                        % (dest, dest))
    return findings, fixed


def no_tags_text(devkit):
    """Тегов нет вовсе: до первого релиза команда обязана быть безвредной и внятной."""
    text = ("релизов devkit ещё не выпускалось: тегов v* в клоне нет, ставить нечего. "
            "Чекаут не тронут, бинари тоже")
    if shutil.which("go"):
        return text + ".\nПока тега нет, бинари собираются на месте: %s build" % (HOW % devkit)
    return (text + ".\nСобрать на месте тоже нечем, go в PATH нет: дождаться релиза либо "
            "поставить Go (brew install go) и позвать %s build" % (HOW % devkit))


def tell(devkit, tag, latest, branch):
    """Рассказ --check: что стоит, что вышло и что сделает обычный update."""
    lines = []
    if branch and tag:
        # Тег ставится на голову main, и сразу после релиза он стоит на том же
        # коммите, что и вершина ветки. Сказать тут «релиза под такой HEAD нет»
        # значило бы соврать: релиз ровно этот, а вот чекаут не отвязан.
        lines.append("чекаут: ветка %s, и на её вершине стоит тег %s; отвязанным он не считается"
                     % (branch, tag))
    elif branch:
        lines.append("чекаут: ветка %s, релиза под такой HEAD нет" % branch)
    elif tag:
        lines.append("чекаут: тег %s (%s)" % (tag, head_commit(devkit)))
    else:
        lines.append("чекаут: отвязан, но ни на одном релизном теге не стоит")
    lines.append("новейший тег: %s" % latest)
    dest = bin_dir()
    for name, (version, commit) in sorted(installed(dest, build.tools(devkit)).items()):
        lines.append("%s: %s (%s)" % (name, version, commit))
    if branch:
        lines.append("обновление тут это git pull и devkitctl doctor --fix; перевести клон на "
                     "тег %s: devkitctl update --pin" % latest)
    elif tag == latest:
        lines.append("новее ничего нет: обновляться не от чего")
    else:
        lines.append("поставит devkitctl update: чекаут и бинари тега %s" % latest)
    return lines


def placed_line(before, after, names, dest):
    """Строка отчёта про пачку утилит: что с ними стало, сколько их и куда легли.

    Слова тут те же, какими про установку скажет человек: он читает про то, что
    у него на машине теперь стоит, а не про раскладку файлов.
    """
    n = len(names)
    how_many = UTILS[0] if n == 1 else say.counted(n, UTILS)
    who = ", ".join(names)
    if after is None:
        verb = "установлена" if n == 1 else "установлено"
        return "%s %s devkit в %s, а версию назвать не смогли: %s" % (verb, how_many, dest, who)
    version = "devkit %s (%s)" % after
    if before is None:
        verb, what = ("установлена" if n == 1 else "установлено"), version
    elif before == after:
        verb, what = ("переложена" if n == 1 else "переложено"), version
    else:
        verb = "обновлена" if n == 1 else "обновлено"
        what = "devkit с %s (%s) до %s (%s)" % (before + after)
    return "%s %s %s в %s: %s" % (verb, how_many, what, dest, who)


def install(devkit, tag, log, err):
    """Вторая половина: тарболл своей платформы, сверка суммы, подмена бинарей.

    Отдаёт (код возврата, имена поставленного).
    """
    asset, bad = asset_name(tag)
    if bad:
        err(bad)
        return 1, []
    dest = bin_dir()
    dest.mkdir(parents=True, exist_ok=True)
    names = build.tools(devkit)
    was = installed(dest, names)
    base = "%s/%s" % (release_base().rstrip("/"), tag)
    # Временный каталог берётся рядом с каталогом назначения, а не в /tmp: на
    # linux /tmp это чаще всего отдельная файловая система, и переименование
    # через её границу дало бы EXDEV ровно на VPS.
    staged = Path(tempfile.mkdtemp(prefix=TMP_PREFIX, dir=str(dest)))
    try:
        archive = staged / asset
        code, why = fetch_url("%s/%s" % (base, asset), archive)
        if code == 404:
            err("тег %s есть, а ассетов у него ещё нет (%s): релиз собирается пару минут после "
                "тега, позвать позже" % (tag, why))
            return 1, []
        if code != 200:
            err("не скачал %s (%s): нужна сеть до гитхаба, бинари остались прежними" % (asset, why))
            return 1, []
        sums = staged / build.SUMS
        code, why = fetch_url("%s/%s" % (base, build.SUMS), sums)
        if code != 200:
            err("не скачал %s (%s): сверять сумму нечем, установка остановлена"
                % (build.SUMS, why))
            return 1, []
        bad = verify(archive, parse_sums(sums.read_text(encoding="utf-8", errors="replace")))
        if bad:
            err(bad)
            return 1, []
        log("скачан %s, сумма сошлась" % asset)
        placed, bad = unpack(archive, staged)
        if bad:
            err(bad)
            return 1, []
        place(staged, placed, dest)
    finally:
        shutil.rmtree(str(staged), ignore_errors=True)
    now = installed(dest, placed)
    # Утилиты ставятся одним движением, и строка на каждую это один и тот же
    # факт, разбитый на шесть строк. Свёртка идёт по переходу: обычно он общий,
    # а если часть утилит пришла с другой версии, таких групп будет две.
    groups = {}
    for name in placed:
        groups.setdefault((was.get(name), now.get(name)), []).append(name)
    for names in sorted(groups.values()):
        log(placed_line(was.get(names[0]), now.get(names[0]), names, dest))
    silent = [n for n in placed if n not in now]
    if silent:
        err("поставленное не отвечает на --version: %s; тарболл релиза %s собран не тем кодом "
            "либо бит исполнения потерян" % (", ".join(silent), tag))
        return 1, placed
    return 0, placed


def run(devkit, from_main, pin=False, check=False, restarted=False,
        machine=None, restart=None, log=print, err=None):
    """Установка и обновление одной командой. Шаги в решении 4 документа DK-149.

    machine это раскладка машинного контура (тем же кодом, что зовёт doctor --fix),
    restart это самоперезапуск: оба вынесены параметрами, чтобы шаги проверялись
    порознь и без подмены системных вызовов.
    """
    err = err or (lambda m: sys.stderr.write(m + "\n"))
    restart = restart or restart_now
    devkit = Path(devkit)
    if not from_main:
        err("devkit тут выложен worktree ветки задачи, а update ставит бинари на всю машину: "
            "с непроверенной ветки они уехали бы во все проекты сразу. Звать из основного "
            "чекаута: %s update" % (HOW % devkit))
        return 2
    if not restarted:
        branch = current_branch(devkit)
        if branch and not pin and not check:
            err("чекаут стоит на ветке %s, а не на релизном теге: это дерево разработчика devkit, "
                "в нём ведётся доска и заводятся ветки задач, и отвязывать его молча нельзя. "
                "Обновление тут это git pull и %s doctor --fix, а первая установка переводит "
                "свежий клон на тег ключом --pin" % (branch, HOW % devkit))
            return 2
        if not check:
            # Грязное дерево видно без сети, и об этом стоит сказать раньше
            # отказа fetch, если сети как раз и нет.
            dirty = dirty_tracked(devkit)
            if dirty:
                err(dirty)
                return 2
        rc, out = git(devkit, "fetch", "--no-tags", "--prune", "origin", TAG_REFSPEC)
        if rc != 0:
            err("git fetch тегов не прошёл (%s): за новыми тегами нужна сеть, и ничего не "
                "тронуто" % (out.splitlines()[0].strip() if out else "git промолчал"))
            return 1
        latest = latest_tag(devkit)
        if not latest:
            log(no_tags_text(devkit))
            return 1
        tag = head_tag(devkit)
        if check:
            for line in tell(devkit, tag, latest, branch):
                log(line)
            return 0
        # После fetch, не до него: якорь unpushed_commits это только что
        # пришедшие --prune тегом v*, а до fetch среди них мог сидеть тег,
        # поставленный человеком руками мимо origin (форвард-фикс DK-164,
        # второй круг).
        risky = unpushed_commits(devkit)
        if risky:
            err(risky)
            return 2
        names = build.tools(devkit)
        commit = head_commit(devkit)
        have = installed(bin_dir(), names)
        if not branch and tag == latest and len(have) == len(names) \
                and all(s[1] == commit for s in have.values()):
            log("обновлять нечего: чекаут стоит на новейшем теге %s, и все бинари отвечают его "
                "коммитом (%s). За ассетами в сеть не ходил" % (latest, commit))
            return 0
        # Отвязывает чекаут режим, а не расхождение коммитов: тег ставится на
        # голову main, и на свежем клоне вершина ветки и новейший тег это
        # обычно один коммит. Судить по коммиту значило бы оставить машину
        # потребителя на ветке, где следующий update откажет как дереву
        # разработчика.
        if branch or tag != latest:
            before = code_stamp(devkit)
            rc, out = git(devkit, "checkout", "--detach", "--quiet", latest)
            if rc != 0:
                err("не перевёл чекаут на %s (%s): чекаут остался где был" % (latest, out))
                return 1
            whence = ("с ветки %s" % branch if branch else
                      "с релиза %s" % tag if tag else "с коммита без тега")
            log("чекаут devkit переведён %s на релиз %s" % (whence, latest))
            if code_stamp(devkit) != before:
                if understands_restart(devkit):
                    restart(devkit / "tools" / "devkitctl" / "devkitctl.py",
                            ["update", RESTART_FLAG])
                    return 0
                log("тег %s собран до появления update: вторую половину делает этот же код, "
                    "другого на теге нет" % latest)
    tag = head_tag(devkit)
    if not tag:
        err("чекаут не стоит на релизном теге, ставить нечего")
        return 1
    rc, placed = install(devkit, tag, log, err)
    if rc != 0:
        return rc
    # Бинари релиза и сборка на машине едут одной дорогой, в bin_dir, и после
    # укладки обе сверяются по победителю PATH: вторая сборка, выигравшая в
    # PATH, пережила бы установку молча, и команда осталась бы старой (DK-599).
    stale = path_divergence(bin_dir(), installed(bin_dir(), placed))
    if stale:
        for m in stale:
            err(m)
        return 1
    # Обёртку кладёт раскладка машинного контура, и второй раз тут её не зовут:
    # иначе установка сказала бы про каталог мимо PATH дважды. Без раскладки
    # (её зовут не все, кто зовёт установку) обёртка всё равно обязана лечь,
    # без неё python-часть не зовётся по имени.
    if machine:
        findings, fixed = machine()
        findings = ["машина: %s" % m for m in findings]
    else:
        findings, fixed = check_wrapper(devkit, True)
    # Сделанное печатается как есть: каждая строка сама начинается с того, что
    # с машиной случилось, и общая помета перед ней только мешала бы читать.
    for m in fixed:
        log(m)
    log("\ndevkit %s на месте: %s в %s" % (tag, say.counted(len(placed), UTILS), bin_dir()))
    if findings:
        log("\nосталось сделать:")
        for i, s in enumerate(findings, 1):
            log("%d. %s" % (i, s))
    else:
        log("\nустановка завершена, дописывать руками нечего.")
    log("проверить devkit можно командой devkitctl doctor")
    return 0
