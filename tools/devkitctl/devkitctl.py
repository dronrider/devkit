#!/usr/bin/env python3
"""devkitctl: обвязка devkit в проекте одной командой.

  devkitctl new --prefix XX [--name "..."] [--no-board] [-C dir]
      подключить проект: AGENTS.md из шаблона, тонкие файлы правил под
      включённые харнесы, git-хуки, доска через taskctl init, болванка
      .devkit/deploy.local для shipctl; --no-board для внешнего трекера

  devkitctl corp [--prefix XX] [--name "..."] [-C dir]
      подключить корп-проект: боковая директория ../<проект>-local со скелетом
      доски и своим git-репозиторием, редирект git config devkit.local в клоне,
      тонкий файл контекста харнеса с импортом на AGENTS.md боковой директории и
      строкой в .git/info/exclude, обёртки хуков на pre-commit и commit-msg.
      Прогон повторяемый: боковая директория не трогается, доводится обвязка
      клона, поэтому он же восстанавливает обвязку после переклонирования

  devkitctl doctor [--fix] [-C dir]
      проверить обвязку проекта: AGENTS.md на месте, генерённые файлы правил
      свежи и не правлены руками, их импорты разворачиваются, git-хуки
      подключены, инварианты доски (taskctl lint), обвязка выката
      (.devkit/deploy.local есть, с командой и гитигнорнута), локальные
      markdown-ссылки не битые; в корп-контуре (задан devkit.local) рабочие
      файлы берутся из боковой директории, а сверх обычных проверок идут
      корп-: чистота корп-индекса, exclude-строка, цепочки на обоих хуках,
      remote боковой директории и свежесть sync, и прогнанный в боковой
      директории доктор идёт по ключу repo привязки и проверяет обвязку
      названного клона; и машинный контур: PostToolUse-хуки,
      SessionStart-хук освежения квоты, хуки уведомлений вместе с бэкендом,
      которым их слать, права машинного контура в permissions.allow (без них
      сессия без человека встаёт на первом отказе), бинари утилит devkit в
      PATH и не старее исходников, обёртка devkitctl рядом с ними и сам
      каталог назначения в PATH, определения агентов в ~/.claude/agents,
      скиллы devkit в ~/.claude/skills, глобальная точка правил ([rules]
      global_file, обычно ~/.claude/CLAUDE.md) свежа и не правлена руками,
      tmux и снимки квоты в ~/.devkit/quota; профили харнесов devkit/kit/harness
      прогоняются через тот же валидатор, каким их читает agentctl; в чекауте
      devkit печатается вес резидента по карманам и находка при превышении
      порога (ядро, ядро доски, общий потолок токенов) плюс находка на тело
      скилла тяжелее своего порога, с предложением резать его надвое (DK-029);
      --fix additive доводит обвязку (хуки, болванка deploy.local либо
      недостающие в ней ключи, .gitignore, сборка бинарей, копия определений
      агентов и скиллов, права машинного контура, глобальная точка правил,
      переезд одиночного снимка квоты в директорию, генерация файлов правил),
      заполненное не трогает, неоднозначное оставляет находкой

  devkitctl update [--pin] [--check]
      поставить или обновить devkit готовыми бинарями релиза, без тулчейна:
      перевести чекаут на новейший тег, скачать тарболл своей платформы,
      сверить SHA256, подменить бинари в каталоге назначения, положить рядом
      обёртку devkitctl и разложить машинный контур. Зовётся из основного
      чекаута; на ветке отказывает и называет обычный путь (git pull и
      doctor --fix), а первая установка разрешает перевод свежего клона на тег
      ключом --pin. --check делает git fetch --tags и только рассказывает: на
      каком теге машина, какой вышел, что поставит обычный update

  devkitctl build [--release] [--out dir]
      собрать бинари devkit с зашитой версией: что собирать, выводится из
      дерева (каталоги tools/*/ с go.mod), версия и коммит из git этого
      чекаута. Без ключей сборка под текущую машину в каталог назначения
      (DEVKIT_BIN, иначе первый из ~/go/bin и ~/.local/bin, который уже стоит
      в PATH, иначе ~/.local/bin) и самопроверка запуском: каждый свежий
      бинарь обязан напечатать по --version ту строку, которую в него
      зашивали. --release собирает четыре пары GOOS/GOARCH, пакует тарболл на
      пару (devkit-<версия>-<os>-<arch>.tar.gz) и кладёт рядом SHA256SUMS;
      живьём там проверяется пара своей машины, остальные три байтовым
      поиском зашитой строки. Каталог назначения по умолчанию dist

  devkitctl weigh [-C dir] [--runs N] [--limit T] [--model M] [--prompt "..."]
      живой замер веса резидента: два headless-прогона claude -p с одинаковым
      запросом (базовый без раскладки devkit, целевой с ней), разница это цена
      текста devkit в токенах. Рядом расчёт по карманам в символах и токенах и
      расхождение расчёта с замером; код 1, когда замер выше потолка. Прогон
      стоит денег и требует сети, поэтому команда отдельная, а не проверка
      доктора; несвежая раскладка на машине это отказ мерить

  devkitctl stats [--context] [-C dir]
      сводка по журналу запусков .devkit/log: частота команд (утилита, команда),
      доля ошибок, отсортировано по частоте убыванием, в конце итоговая строка
      по всему журналу; битые строки молча пропускаются.
      --context берёт второй источник, журналы сессий харнеса
      (~/.claude/projects/<слепок пути проекта>/*.jsonl), и печатает, куда ушёл
      объём: старт против истории, перезаписи префикса с их ценой, топ тулов по
      объёму результатов; правило подписи перезаписи в context.py

Выход 0 всё в порядке, 1 есть находки, 2 ошибка запуска.
"""
import argparse
import build
import context
import corp
import harness
import importlib.util
import json
import layout
import os
import perms
import re
import rules
import shutil
import subprocess
import sys
import update
import weigh
from datetime import datetime
from pathlib import Path

DEVKIT = Path(__file__).resolve().parent.parent.parent
HOOK_SCRIPTS = ("check-symbols.py", "check-memory.py", "check-sensitive.py")
SESSION_HOOK = "quota-refresh.sh"
NOTIFY_HOOK = "notify.py"
NOTIFY_EVENTS = ("Notification", "Stop", "SubagentStop", "UserPromptSubmit")
NOTIFY_MATCHER = "permission_prompt|agent_needs_input|elicitation_dialog|idle_prompt"
POST_MATCHER = "Edit|Write|NotebookEdit"
# Раскладка хуков харнеса: событие, матчер, команда с местом под чекаут devkit.
# Тот же перечень нарисован в hooks/README.md, но раскладывает его отсюда
# доктор: список ручных шагов в README это перекладывание раскладки на человека.
HOOK_LAYOUT = (
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-symbols.py --hook"),
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-memory.py --hook"),
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-sensitive.py --hook"),
    ("SessionStart", "", "sh %s/hooks/quota-refresh.sh"),
    ("Notification", NOTIFY_MATCHER, "python3 %s/hooks/notify.py --hook claude-code"),
    ("Stop", "", "python3 %s/hooks/notify.py --hook claude-code"),
    ("SubagentStop", "", "python3 %s/hooks/notify.py --hook claude-code"),
    ("UserPromptSubmit", "", "python3 %s/hooks/notify.py --hook claude-code"),
)
AGENTS_DIR = "~/.claude/agents"
SKILLS_DIR = "~/.claude/skills"
# Снимок остатка лимитов лежит по файлу на харнес. Одиночный quota.local это
# как было до директории: его переезд делает --fix, читатель до тех пор смотрит
# старый путь (tools/agentctl/quota.go).
QUOTA_DIR = "~/.devkit/quota"
QUOTA_LEGACY = "~/.devkit/quota.local"
# В какой файл директории переезжает старый снимок: снять его было нечем, кроме
# панели Claude Code.
QUOTA_LEGACY_HARNESS = "claude-code"
# Порог свежести снимка держит agentctl (snapshotMaxAge в tools/agentctl/quota.go), тут
# его копия: доктор про снимок говорит то же самое, что pick, иначе одна утилита
# звала бы переснимать, а вторая молчала.
QUOTA_MAX_AGE = 45 * 60
QUOTA_TIME_FORMATS = ("%Y-%m-%dT%H:%M", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%d %H:%M")
DEPLOY_CONFIG = ".devkit/deploy.local"
DEPLOY_IGNORE = ".devkit/*.local"
RUN_LOG = ".devkit/log"
# Ключи болванки выката: имя, комментарий (со своим завершающим \n) и значение
# для файла, заводимого с нуля. Один источник для DEPLOY_TEMPLATE (новый файл)
# и для дописывания недостающего в уже существующий: комментарий у ключа один,
# держать его в двух местах значило бы дать им разойтись.
DEPLOY_FIELDS = (
    ("deploy",
     "# Обвязка выката для shipctl (гитигнорнут: в команде выката обычно адрес\n"
     "# или роль машины, её место в локальном, а не в коммитимом). shipctl merge\n"
     "# берёт команду отсюда, --deploy на каждый вызов передавать не нужно.\n"
     "# Объект выката бывает не только серверным: для приложения сюда идёт\n"
     "# сборка, подпись и заливка в канал обновлений, а не серверный рестарт.\n",
     ""),
    ("test",
     "# test это команда тестов проекта, её берёт shipctl merge, когда не передан\n"
     "# --test: сочинять её под каждый репозиторий процедуре пачки нечем.\n",
     ""),
    ("autonomous",
     "# autonomous=true отдаёт агенту весь конвейер: готовую задачу (тесты\n"
     "# зелёные, ревью чистое) он сам сливает, пушит в origin и катит на прод.\n"
     "# false оставляет слияние, пуш и выкат за пользователем.\n",
     "false"),
)
DEPLOY_TEMPLATE = "".join("%s%s =%s\n" % (comment, key, " %s" % default if default else "")
                          for key, comment, default in DEPLOY_FIELDS)
SKIP_DIRS = {".git", "node_modules", "vendor", "target", "local-docs",
             ".venv", "venv", "__pycache__", ".idea", ".vscode"}
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
CODE_SPAN_RE = re.compile(r"`+[^`]*`+")


def run(args, cwd=None):
    p = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    return p.returncode, (p.stdout + p.stderr).strip()


def project_root(start):
    rc, out = run(["git", "-C", start, "rev-parse", "--show-toplevel"])
    if rc == 0:
        return Path(out), True
    return Path(start).resolve(), False


def check_links(root):
    # Проверяется живая дока: корневые тексты, README инструментов, kit/ и
    # hooks/. docs/ стареет по правилу и не проверяется: файл задачи описывает
    # состояние на момент решения и после закрытия не правится, так что ссылка
    # на уехавший путь там законна, а находка по ней зовёт чинить то, что чинить
    # запрещено (DK-140, DK-144).
    findings = []
    for dirpath, dirnames, filenames in os.walk(root):
        if Path(dirpath) == Path(root):
            dirnames[:] = [d for d in dirnames if d != "docs"]
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if not fn.endswith(".md"):
                continue
            md = Path(dirpath) / fn
            rel = os.path.relpath(md, root)
            try:
                lines = md.read_text(encoding="utf-8", errors="replace").splitlines()
            except OSError:
                continue
            fence = ""
            for i, ln in enumerate(lines, 1):
                fm = FENCE_RE.match(ln)
                if fm:
                    # Закрывает только забор из того же символа и не короче
                    # открывающего, иначе вложенный ``` оборвал бы внешний ````.
                    if not fence:
                        fence = fm.group(1)
                    elif fm.group(1)[0] == fence[0] and len(fm.group(1)) >= len(fence) \
                            and not ln[fm.end():].strip():
                        fence = ""
                    continue
                if fence:
                    continue
                # Инлайн-код это текст: [текст](путь) в примере команды не ссылка.
                for m in LINK_RE.finditer(CODE_SPAN_RE.sub(lambda c: " " * len(c.group(0)), ln)):
                    target = m.group(1).split("#")[0]
                    if not target or "://" in target or target.startswith("mailto:"):
                        continue
                    if not (md.parent / target).exists():
                        findings.append("%s:%d: битая ссылка (%s)" % (rel, i, m.group(1)))
    return findings


def read_deploy(root):
    # Тройка (deploy, test, autonomous). None вместо deploy это «файла нет»,
    # иначе значения ключей (могут быть пустыми) и флаг autonomous. Пустая
    # строка тут значит и «ключа нет», и «ключ есть, но пустой» одинаково: за
    # различением этих двух случаев (нужно для дописывания недостающего) идти
    # в deploy_present_keys, не сюда.
    f = root / DEPLOY_CONFIG
    if not f.exists():
        return None, None, None
    deploy = ""
    test = ""
    autonomous = False
    for ln in f.read_text(encoding="utf-8", errors="replace").splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#") or "=" not in ln:
            continue
        key, _, val = ln.partition("=")
        key_stripped = key.strip()
        val_stripped = val.strip().strip("\"'")
        if key_stripped == "deploy":
            deploy = val_stripped
        elif key_stripped == "test":
            test = val_stripped
        elif key_stripped == "autonomous":
            autonomous = val_stripped.lower() in ("1", "true", "t", "yes", "y", "on")
    return deploy, test, autonomous


def deploy_present_keys(text):
    # Ключи, реально написанные в файле, вне зависимости от значения (в том
    # числе пустого). read_deploy для отсутствующего и пустого ключа отдаёт
    # одно и то же "", а дописыванию нужно отличать одно от другого.
    keys = set()
    for ln in text.splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#") or "=" not in ln:
            continue
        key, _, _ = ln.partition("=")
        keys.add(key.strip())
    return keys


def ensure_gitignore(root, pattern, comment="# Локальная обвязка выката, живёт только на машине."):
    gi = root / ".gitignore"
    lines = gi.read_text(encoding="utf-8").splitlines() if gi.exists() else []
    if pattern in (ln.strip() for ln in lines):
        return False
    sep = "\n" if lines and lines[-1].strip() else ""
    with gi.open("a", encoding="utf-8") as f:
        f.write("%s%s\n%s\n" % (sep, comment, pattern))
    return True


LOG_IGNORE_COMMENT = "# Журнал запусков инструментов devkit, живёт только на машине."


def scaffold_deploy(root):
    dep = root / DEPLOY_CONFIG
    done = []
    if dep.exists():
        done.append("%s уже есть, не трогаю" % DEPLOY_CONFIG)
    else:
        dep.parent.mkdir(parents=True, exist_ok=True)
        dep.write_text(DEPLOY_TEMPLATE, encoding="utf-8")
        done.append("%s создан: вписать команду выката, autonomous при готовности" % DEPLOY_CONFIG)
    if ensure_gitignore(root, DEPLOY_IGNORE):
        done.append(".gitignore: добавлен %s" % DEPLOY_IGNORE)
    if ensure_gitignore(root, RUN_LOG, LOG_IGNORE_COMMENT):
        done.append(".gitignore: добавлен %s" % RUN_LOG)
    return done


def patch_deploy(root):
    # Дописывает в конец уже существующего deploy.local ключи болванки,
    # которых в файле нет, каждый со своим комментарием. Чужой текст (значения
    # имеющихся ключей, шапка, их порядок) не трогается, только добавление в
    # хвост. Дописанный ключ остаётся пустым (не берётся значение по
    # умолчанию из DEPLOY_FIELDS): находка про пустой test= после починки
    # обязана остаться, а пустой autonomous и так штатно читается как false.
    dep = root / DEPLOY_CONFIG
    text = dep.read_text(encoding="utf-8", errors="replace")
    present = deploy_present_keys(text)
    missing = [(key, comment) for key, comment, _ in DEPLOY_FIELDS if key not in present]
    if not missing:
        return []
    sep = "\n" if text and not text.endswith("\n") else ""
    addition = "".join("%s%s =\n" % (comment, key) for key, comment in missing)
    with dep.open("a", encoding="utf-8") as f:
        f.write(sep + addition)
    return ["%s: дописан недостающий ключ %s" % (DEPLOY_CONFIG, key) for key, _ in missing]


def log_run(root, cmd, code):
    # Журнал запусков, общий с tools/taskctl/shipctl/regcheck: статистика, какие
    # команды реально гоняются и как часто падают. Только там, где есть
    # .devkit, провал записи работу не ломает.
    d = root / ".devkit"
    if not d.is_dir():
        return
    try:
        with (d / "log").open("a", encoding="utf-8") as f:
            f.write("%s\tdevkitctl\t%s\t%d\n"
                    % (datetime.now().strftime("%Y-%m-%dT%H:%M:%S"), cmd, code))
    except OSError:
        pass


def check_git_hooks(root):
    hooks_dir = (DEVKIT / "hooks").resolve()
    rc, hp = run(["git", "config", "core.hooksPath"], cwd=root)
    if rc == 0 and hp:
        cand = Path(os.path.expanduser(hp))
        if not cand.is_absolute():
            cand = root / cand
        if cand.exists() and cand.resolve() == hooks_dir:
            return None
    else:
        rc, gp = run(["git", "rev-parse", "--git-path", "hooks"], cwd=root)
        pre = (Path(gp) if os.path.isabs(gp) else root / gp) / "pre-commit"
        if pre.exists() and Path(os.path.realpath(pre)).parent == hooks_dir:
            return None
    rel = os.path.relpath(hooks_dir, root)
    return "git-хуки devkit не подключены; из корня проекта: git config core.hooksPath %s" % rel


def connect_git_hooks(root):
    # Подключить хуки devkit. Возврат (сделано, находка): либо одно непустое,
    # либо оба None, когда хуки уже на месте. Свои хуки проекта не трогаются,
    # это остаётся находкой (симлинки ставит человек). Мутирует.
    if check_git_hooks(root) is None:
        return None, None
    rc, gp = run(["git", "rev-parse", "--git-path", "hooks"], cwd=root)
    hdir = Path(gp) if os.path.isabs(gp) else root / gp
    custom = [p.name for p in hdir.glob("*") if p.is_file() and not p.name.endswith(".sample")]
    if custom:
        return None, ("в хуках проекта уже есть свои (%s), core.hooksPath не трогается; "
                      "симлинки на хуки devkit по hooks/README.md" % ", ".join(custom))
    hooks_rel = os.path.relpath((DEVKIT / "hooks").resolve(), root)
    run(["git", "config", "core.hooksPath", hooks_rel], cwd=root)
    return "git-хуки: core.hooksPath = %s" % hooks_rel, None


def human_age(seconds):
    minutes = int(seconds // 60)
    if minutes < 60:
        return "%dм" % minutes
    hours = minutes // 60
    if hours < 48:
        return "%dч" % hours
    return "%dд" % (hours // 24)


def quota_taken(path):
    # Момент снятия из снимка. None и когда строки taken нет, и когда она не
    # разобрана: для доктора это один случай, снимку нельзя верить по возрасту.
    for ln in path.read_text(encoding="utf-8", errors="replace").splitlines():
        key, sep, val = ln.partition("=")
        if not sep or key.strip() != "taken":
            continue
        val = val.strip()
        for fmt in QUOTA_TIME_FORMATS:
            try:
                return datetime.strptime(val, fmt)
            except ValueError:
                pass
        return None
    return None


def devkit_checkout():
    # Основной чекаут devkit и признак, что скрипт запущен из него. Версия
    # бинарей сверяется с HEAD основного чекаута, а сборка из worktree ветки
    # задачи не запускается вовсе: машинный бинарь с непроверенной ветки уехал
    # бы во все проекты сразу.
    rc, out = run(["git", "-C", str(DEVKIT), "rev-parse", "--git-common-dir"])
    if rc != 0:
        return DEVKIT, True
    common = Path(out)
    if not common.is_absolute():
        common = DEVKIT / common
    main = common.resolve().parent
    return main, main == DEVKIT.resolve()


def path_winner(name, target, gobin):
    # Собранный бинарь может проиграть в PATH чужой копии, и тогда doctor
    # молча чинил бы то, чем никто не пользуется.
    found = shutil.which(name)
    if not found:
        return "свежий %s лежит в %s, но %s не в PATH: добавить директорию в PATH" % (name, target, gobin)
    if os.path.realpath(found) != os.path.realpath(str(target)):
        return ("свежий %s лежит в %s, а в PATH выигрывает %s: убрать старую копию либо "
                "поднять %s выше в PATH" % (name, target, found, gobin))
    return None


def unsaved_go(main):
    """Утилиты с несохранёнными правками go-кода: имя -> mtime самого свежего файла.

    Это единственный хвост, где сверка версий остаётся без опоры: коммит бинаря
    равен HEAD, а исходники уже другие, и сравнивать нечего. Только тут доктор
    досравнивает mtime, как делал до сверки по коммиту.
    """
    rc, out = run(["git", "-C", str(main), "status", "--porcelain", "--", "*.go"])
    dirty = set()
    if rc != 0:
        return {}
    for line in out.splitlines():
        parts = Path(line[3:].strip().strip('"')).parts
        if len(parts) > 2 and parts[0] == "tools":
            dirty.add(parts[1])
    return {name: max((p.stat().st_mtime for p in (Path(main) / "tools" / name).glob("*.go")),
                      default=0) for name in dirty}


def binary_trouble(path, head, mode, stale_after):
    """Что не так с бинарём: текст причины либо None, если всё сходится.

    Судится по коммиту, зашитому в бинарь: правило сходимости одно, коммит
    бинаря равен коммиту HEAD основного чекаута devkit, и «немного разошлись»
    тут не бывает.
    """
    if not path or not os.path.exists(str(path)):
        return "не в PATH"
    stamp = update.binary_stamp(path)
    if stamp is None:
        return "не отвечает на --version: собран до релизной схемы версий"
    version, commit = stamp
    if head and commit != head:
        return "собран из коммита %s (версия %s), а чекаут devkit %s" % (commit, version, mode)
    if stale_after and os.path.getmtime(str(path)) < stale_after:
        return "старее несохранённых правок go-кода в devkit"
    return None


def check_binaries(fix):
    """Бинари devkit в PATH и на одной версии с чекаутом.

    Список проверяемых выводится из дерева (tools/*/go.mod), а не из перечня в
    коде: по правилу языка это ровно то, что ставится на машину, и новая утилита
    попадает под проверку тем, что она есть.
    """
    findings, fixed = [], []
    gobin = update.bin_dir()
    main, from_main = devkit_checkout()
    # Точка сборки одна на машину и на CI, поэтому доктор и советует, и собирает
    # тем же devkitctl build: своя копия go build разошлась бы с ним ключами
    # (версия зашивается линковкой) в первый же раз.
    cmd = "python3 %s/tools/devkitctl/devkitctl.py build" % main
    src = main if (main / "tools").is_dir() else DEVKIT
    head, mode = update.head_commit(main), update.checkout_mode(main)
    stale = unsaved_go(main)
    broken = []
    for name in build.tools(src):
        target = gobin / name
        # Судится то, что выигрывает в PATH: пользоваться человек будет им, а не
        # тем, что лежит в каталоге назначения.
        why = binary_trouble(shutil.which(name), head, mode, stale.get(name, 0))
        if why is None:
            continue
        if binary_trouble(target, head, mode, stale.get(name, 0)) is None:
            # Годная сборка уже лежит на месте, дело за PATH.
            conflict = path_winner(name, target, gobin)
            if conflict:
                findings.append(conflict)
            continue
        broken.append((name, why))
    if not broken:
        return findings, fixed
    if not from_main:
        # Машинный бинарь с непроверенной ветки уехал бы во все проекты сразу,
        # поэтому чинится расхождение только из основного чекаута.
        findings += ["%s %s, а devkit тут выложен worktree ветки задачи: собирать машинный "
                     "бинарь с непроверенной ветки нельзя; из основного чекаута: %s"
                     % (name, why, cmd) for name, why in broken]
        return findings, fixed
    tag = update.head_tag(main)
    go = shutil.which("go")
    if tag:
        # Чекаут стоит на релизе: бинари этого тега выложены готовыми, и тулчейна
        # на машине потребителя может не быть вовсе.
        how = "поставить бинари релиза %s: devkitctl update" % tag
    elif go:
        how = cmd
    else:
        how = ("собирать нечем, go в PATH нет: Go ставится пакетным менеджером "
               "(brew install go), потом %s" % cmd)
    if not fix or not (tag or go):
        findings += ["%s %s; %s" % (name, why, how) for name, why in broken]
        return findings, fixed
    if tag:
        # Отчёт установки идёт в свой список: доктор говорит про машинный контур
        # одной строкой на находку, а «сумма сошлась» это подробность команды.
        said = []
        update.install(main, tag, said.append, lambda m: findings.append(str(m)))
    else:
        version, commit = build.stamp(main)
        gobin.mkdir(parents=True, exist_ok=True)
        for name, _ in broken:
            err = (build.compile_one(main, name, gobin / name, version, commit)
                   or build.check_run(name, gobin / name, version, commit))
            if err:
                findings.append(err)
    for name, why in broken:
        if binary_trouble(gobin / name, head, mode, stale.get(name, 0)) is not None:
            findings.append("%s %s; %s" % (name, why, how))
            continue
        fixed.append("%s в %s: %s (%s)" % ((name, gobin) + update.binary_stamp(gobin / name)))
        conflict = path_winner(name, gobin / name, gobin)
        if conflict:
            findings.append(conflict)
    return findings, fixed


def is_agent_def(path):
    # Определение субагента опознаётся по frontmatter с effort: именно оттуда
    # харнес берёт глубину размышления, и файл без него агентом не работает.
    head = path.read_text(encoding="utf-8", errors="replace").split("\n", 40)
    if not head or head[0].strip() != "---":
        return False
    for line in head[1:]:
        if line.strip() == "---":
            return False
        if line.startswith("effort:"):
            return True
    return False


def check_agent_defs(fix):
    # Эталон берётся из основного чекаута, а не из того, откуда запущен doctor:
    # определение с ветки задачи уехало бы на машину во все проекты сразу.
    findings, fixed = [], []
    main, from_main = devkit_checkout()
    src_dir = main / "kit" / "agents" if (main / "kit" / "agents").is_dir() else DEVKIT / "kit" / "agents"
    dst_dir = Path(os.path.expanduser(AGENTS_DIR))
    # Директория перебирается целиком, а не по префиксу: набор растёт ролями
    # (exec-* для исполнения, review-* для ревью), и новая роль должна
    # раскладываться сама, без правки доктора. Отбор идёт по frontmatter, иначе
    # на машину как агент уехал бы любой соседний markdown вроде README.
    for src in sorted(src_dir.glob("*.md")):
        if not is_agent_def(src):
            continue
        dst = dst_dir / src.name
        whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, класть на машину "
                                       "определение с непроверенной ветки нельзя; из основного чекаута: ")
        if dst.exists():
            if dst.read_text(encoding="utf-8", errors="replace") != src.read_text(encoding="utf-8"):
                # devkit источник правды для промптов агентов: правка, сделанная в
                # репозитории, обязана доехать на машину сама, а не остаться
                # находкой навсегда. Ручную правку на машине --fix затирает, но не
                # молча: отчёт называет, что именно переложил.
                if fix and from_main:
                    shutil.copyfile(src, dst)
                    fixed.append("определение агента %s разошлось с devkit, переложено из %s"
                                % (dst, src))
                else:
                    findings.append("определение агента %s разошлось с devkit; обновить: %scp %s %s"
                                    % (dst, whence, src, dst))
            continue
        if fix and from_main:
            dst_dir.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(src, dst)
            fixed.append("определение агента %s положено в %s" % (src.name, dst_dir))
            continue
        findings.append("нет определения агента %s: effort из вердикта pick применять нечем, "
                        "спавн уйдёт на дефолтного агента; %scp %s %s"
                        % (dst, whence, src, dst))
    return findings, fixed


def check_skills(fix):
    # Скиллы едут на машину тем же каналом, что определения субагентов: эталон
    # это основной чекаут, из worktree ветки задачи идёт только сверка. Скилл
    # это директория с SKILL.md, по нему он и опознаётся, соседняя проза в
    # kit/skills/ на машину не уезжает.
    findings, fixed = [], []
    main, from_main = devkit_checkout()
    src_dir = main / "kit" / "skills" if (main / "kit" / "skills").is_dir() else DEVKIT / "kit" / "skills"
    dst_dir = Path(os.path.expanduser(SKILLS_DIR))
    for src in sorted(src_dir.glob("*/SKILL.md")):
        name = src.parent.name
        dst = dst_dir / name / "SKILL.md"
        whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, класть на машину "
                                       "скилл с непроверенной ветки нельзя; из основного чекаута: ")
        how = "%smkdir -p %s && cp %s %s" % (whence, dst.parent, src, dst)
        if dst.exists():
            if dst.read_text(encoding="utf-8", errors="replace") != src.read_text(encoding="utf-8"):
                if fix and from_main:
                    shutil.copyfile(src, dst)
                    fixed.append("скилл %s разошёлся с devkit, переложен из %s" % (dst, src))
                else:
                    findings.append("скилл %s разошёлся с devkit; обновить: %s" % (dst, how))
            continue
        if fix and from_main:
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(src, dst)
            fixed.append("скилл %s положен в %s" % (name, dst.parent))
            continue
        findings.append("нет скилла %s: процедуру сессия по нему не подхватит и соберёт "
                        "на глаз; %s" % (dst, how))
    return findings, fixed


def hook_events(text, script):
    # События, на которые в настройках повешен скрипт. None значит настройки не
    # разобрались, и судить остаётся по подстроке.
    try:
        data = json.loads(text or "{}")
    except ValueError:
        return None
    found = set()
    for event, groups in (data.get("hooks") or {}).items():
        for group in groups or []:
            for h in (group or {}).get("hooks") or []:
                if script in (h.get("command") or ""):
                    found.add(event)
    return found


def hook_gaps(text, settings):
    """Чего не хватает в хуках харнеса. Возврат (пробелы, находки): пробел это
    строка HOOK_LAYOUT, которую кладёт --fix, находка это тот же пробел словами
    для человека."""
    gaps, findings = [], []
    notify_events = hook_events(text, NOTIFY_HOOK)
    if notify_events is None:
        # Настройки не разобрались, судить остаётся по подстроке: тогда либо
        # уведомитель там есть на всех событиях, либо нет ни на одном.
        notify_events = set(NOTIFY_EVENTS) if NOTIFY_HOOK in text else set()
    missing_notify = []
    for event, matcher, cmd in HOOK_LAYOUT:
        script = os.path.basename(cmd.split()[1])
        if script == NOTIFY_HOOK:
            if event in notify_events:
                continue
            missing_notify.append(event)
        elif script in text:
            continue
        gaps.append((event, matcher, cmd))
        if script in HOOK_SCRIPTS:
            findings.append("PostToolUse-хук %s не подключён в %s (hooks/README.md)"
                            % (script, settings))
        elif script == SESSION_HOOK:
            findings.append("SessionStart-хук %s не подключён в %s: снимок квоты сам не освежается, "
                            "и корректор pick рано или поздно останется с протухшим "
                            "(hooks/README.md)" % (SESSION_HOOK, settings))
    if missing_notify:
        findings.append("хук %s не подключён на события %s в %s: сессия молча стоит, когда ждёт "
                        "разрешения, и не говорит, что закончила ход или что субагент "
                        "отработал (hooks/README.md)"
                        % (NOTIFY_HOOK, ", ".join(missing_notify), settings))
    return gaps, findings


def install_hooks(settings, gaps, devkit):
    """Дописать недостающие хуки в настройки харнеса. Правка additive, как у
    прав: чужие группы и порядок остаются, команда встаёт в группу со своим
    матчером либо заводит её. Отдаёт строки о сделанном."""
    data, bad = perms.load(settings)
    if bad is not None:
        return []
    home = os.path.expanduser("~")
    path = str(devkit)
    if path.startswith(home + os.sep):
        path = "~" + path[len(home):]
    hooks = data.setdefault("hooks", {})
    done = []
    for event, matcher, tpl in gaps:
        cmd = tpl % path
        groups = hooks.setdefault(event, [])
        group = None
        for g in groups:
            if isinstance(g, dict) and (g.get("matcher") or "") == matcher:
                group = g
                break
        if group is None:
            group = {"matcher": matcher} if matcher else {}
            group["hooks"] = []
            groups.append(group)
        group.setdefault("hooks", []).append({"type": "command", "command": cmd})
        done.append("хук харнеса на %s: %s" % (event, cmd))
    settings.parent.mkdir(parents=True, exist_ok=True)
    tmp = settings.with_name(settings.name + ".devkit-tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(str(tmp), str(settings))
    return done


def check_notify_hook(text, settings):
    findings = []
    # Выбор бэкенда живёт в самом уведомителе, второй его копии тут нет.
    src = DEVKIT / "hooks" / NOTIFY_HOOK
    spec = importlib.util.spec_from_file_location("devkit_notify", src)
    if not src.exists() or spec is None:
        return findings
    mod = importlib.util.module_from_spec(spec)
    # Уведомитель берёт разбор входа из соседнего hookio, а загрузка по пути его
    # директорию в sys.path не кладёт.
    sys.path.insert(0, str(src.parent))
    try:
        spec.loader.exec_module(mod)
    except (OSError, SyntaxError, ImportError):
        return findings
    finally:
        sys.path.pop(0)
    backend = mod.pick_backend()
    if backend:
        # Слать есть чем, но клик по баннеру уводит в Finder: это не поломка, а
        # предложение доставить недостающее, поэтому и находка мягкая.
        if sys.platform == "darwin" and os.path.basename(backend) == "osascript":
            findings.append("уведомления идут, но клик по баннеру ведёт не в окно сессии: "
                            "osascript постит от имени Script Editor, и клик открывает Finder; "
                            "переход даёт terminal-notifier (brew install terminal-notifier)")
        return findings
    if sys.platform == "darwin":
        how = "на macOS шлём terminal-notifier или osascript, а их нет в PATH"
    elif sys.platform.startswith("linux"):
        how = "notify-send не в PATH, ставится пакетным менеджером (apt install libnotify-bin)"
    else:
        how = "бэкенда под платформу %s в %s пока нет" % (sys.platform, NOTIFY_HOOK)
    findings.append("уведомлять нечем: %s; проверка канала: python3 %s --self-test" % (how, src))
    return findings


def check_machine(fix):
    # Машинный контур, общий для всех проектов: хуки Claude Code, права сессии
    # без человека, бинари devkit, определения агентов, скиллы, глобальная точка
    # правил, tmux и снимок квоты.
    findings, fixed = [], []
    settings = Path(os.path.expanduser("~/.claude/settings.json"))
    text = settings.read_text(encoding="utf-8") if settings.exists() else ""
    main, from_main = devkit_checkout()
    # Хуки харнеса раскладываются тем же рубежом, что права и скиллы: с ветки
    # задачи на машину они не едут, там их сверяют и чинят из основного чекаута.
    gaps, gap_findings = hook_gaps(text, settings)
    if gaps and fix and from_main:
        fixed += install_hooks(settings, gaps, main)
        text = settings.read_text(encoding="utf-8") if settings.exists() else ""
    else:
        findings += gap_findings
    findings += check_notify_hook(text, settings)
    pf, pd = perms.check(settings, fix, None if from_main else main)
    findings += pf
    fixed += pd
    for check in (check_binaries, check_agent_defs, check_skills):
        f, d = check(fix)
        findings += f
        fixed += d
    # Обёртка devkitctl рядом с бинарями: без неё python-часть зовётся длинным
    # путём, а кладут её одинаково update и доктор.
    wf, wd = update.check_wrapper(main, fix, from_main)
    findings += wf
    fixed += wd
    # Про вышедший релиз машине потребителя говорит только доктор: git pull в
    # отвязанном чекауте не работает, и другого канала у неё нет.
    findings += update.check_release(main)
    devkit_src = main if (main / "kit" / "harness").is_dir() else DEVKIT
    whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, класть на машину "
                                   "правила с непроверенной ветки нельзя; из основного чекаута %s: "
                                   % main)
    gf, gd = rules.check_global(devkit_src, fix and from_main, whence=whence)
    findings += gf
    fixed += gd
    if not shutil.which("tmux"):
        findings.append("tmux не в PATH: agentctl quota refresh не снимет панель /usage, "
                        "корректор останется без снимка; ставится пакетным менеджером (brew install tmux)")
    f, d = check_quota(fix)
    findings += f
    fixed += d
    return findings, fixed


def check_quota(fix):
    # Снимки квоты: переезд одиночного файла в директорию и возраст каждого
    # снимка. Директория заведена по харнесу, потому что лимиты у инструментов
    # свои, и одним файлом их не описать.
    findings, fixed = [], []
    quota_dir = Path(os.path.expanduser(QUOTA_DIR))
    legacy = Path(os.path.expanduser(QUOTA_LEGACY))
    moved = quota_dir / ("%s.local" % QUOTA_LEGACY_HARNESS)
    if legacy.exists():
        if moved.exists():
            # Переезд уже сделан, а старый файл остался: удалять его --fix не
            # вправе (правки строго additive), но молчать про два снимка нельзя.
            findings.append("старый снимок квоты %s лежит рядом с новым %s: читается новый, "
                            "старый убрать руками (rm %s)" % (legacy, moved, legacy))
        elif fix:
            quota_dir.mkdir(parents=True, exist_ok=True)
            legacy.replace(moved)
            fixed.append("снимок квоты переехал: %s -> %s" % (legacy, moved))
        else:
            findings.append("снимок квоты лежит по старому пути %s: снимок стал директорией по файлу "
                            "на харнес; переложить: devkitctl doctor --fix (в %s)" % (legacy, moved))
    snaps = sorted(quota_dir.glob("*.local")) if quota_dir.is_dir() else []
    if not snaps and not legacy.exists():
        findings.append("нет снимка квоты в %s: корректор pick двигать вердикт не будет; "
                        "снять: agentctl quota refresh" % quota_dir)
    for quota in snaps:
        taken = quota_taken(quota)
        if taken is None:
            findings.append("в снимке квоты %s не разобран момент снятия (строка taken =), "
                            "возраст не проверить; переснять: agentctl quota refresh" % quota)
            continue
        age = (datetime.now() - taken).total_seconds()
        if age > QUOTA_MAX_AGE:
            findings.append("снимок квоты %s протух (возраст %s при пороге %s): профицит по нему уже не считается, "
                            "сдвиг вверх потерян; переснять: agentctl quota refresh"
                            % (quota, human_age(age), human_age(QUOTA_MAX_AGE)))
    return findings, fixed


def corp_profiles(local):
    # Тонкие файлы харнесов корп-клона считаются по конфигу боковой директории:
    # там лежит .devkit и там же решается, какие харнесы включены.
    profiles, findings = rules.enabled_harnesses(local, DEVKIT / "kit" / "harness")
    imports = [(n, p) for n, p in profiles if rules.mode_of(p) == "import"]
    return imports, findings


def corp_thin_names(imports):
    return [p.str_of("rules", "file") for _, p in imports]


def corp_thin(clone, local, imports, fix):
    # Тонкий файл харнеса обязан лежать в корне клона, иначе харнес его не
    # прочитает, а весь текст живёт в боковой директории: проверка та же, что у
    # домашнего проекта, только AGENTS.md берётся оттуда.
    findings, fixed = [], []
    board = (Path(local) / "docs" / "TASKS.md").exists()
    for name, profile in imports:
        depth = rules.actual_depth(DEVKIT, local, board, rules.declared_depth(profile)[0])
        tf, td = rules.check_thin(name, profile, Path(clone), DEVKIT, board, False, depth, fix,
                                  agents_root=Path(local))
        findings += tf
        fixed += td
    return findings, fixed


def doctor(start, fix=False):
    findings, fixed = [], []
    root, in_git = project_root(start)
    if not in_git:
        findings.append("не git-репозиторий: %s" % root)
    clone, local = corp.pair(root, DEVKIT)
    if local:
        # Рабочие файлы devkit в корп-контуре лежат не в дереве проекта, а
        # рядом, и обычные проверки идут по ним: в самом клоне ни доски, ни
        # AGENTS.md нет и быть не должно.
        root = Path(local)
        imports, hf = corp_profiles(local)
        findings += hf
        cf, cd = corp.check(clone, local, DEVKIT, corp_thin_names(imports), fix)
        findings += cf
        fixed += cd
        if corp.local_dir(clone, DEVKIT):
            tf, td = corp_thin(clone, local, imports, fix)
            findings += tf
            fixed += td
    rfindings, rfixed = rules.check(root, DEVKIT, fix, SKIP_DIRS)
    findings += rfindings
    fixed += rfixed
    if in_git and check_git_hooks(root):
        if fix:
            done, residual = connect_git_hooks(root)
            (fixed if done else findings).append(done or residual)
        else:
            findings.append(check_git_hooks(root))
    # Профили харнесов проверяются все, а не только активный: битый профиль
    # находится до того, как кто-то на него переключится, а починить его
    # автоматике нечем, это правка в devkit.
    findings += harness.check_profiles(str(DEVKIT / "kit" / "harness"))
    # Вес резидента и тело скилла (DK-029): карманы общие для всех проектов
    # devkit, находка не проектная, поэтому считается только для самого
    # чекаута devkit, а не для каждого подключённого проекта.
    if Path(root).resolve() == DEVKIT.resolve():
        wlines, wfindings = weigh.pockets_report(root, DEVKIT)
        for ln in wlines:
            print(ln)
        findings += wfindings
        findings += weigh.skill_findings(DEVKIT)
    # Раскладка проверяется только на самом devkit: в чужом проекте она своя.
    findings += ["раскладка: %s" % m for m in layout.check(root)]
    # Режим чекаута называется всегда, а не только рядом с находкой: на машине
    # разработчика бинарь впереди последнего релиза это норма, на машине
    # потребителя поломка, и по одному номеру версии одно от другого не отличить.
    print("чекаут devkit: %s" % update.checkout_mode(devkit_checkout()[0]))
    mfindings, mfixed = check_machine(fix)
    findings += ["машина: %s" % m for m in mfindings]
    fixed += mfixed
    if (root / "docs" / "TASKS.md").exists():
        tc = shutil.which("taskctl")
        if tc:
            # Про отсутствие бинаря уже сказал машинный раздел, тут только lint.
            rc, out = run([tc, "-C", str(root), "lint"])
            if rc != 0:
                findings.append("taskctl lint: %s" % out)
        deploy, test, autonomous = read_deploy(root)
        if deploy is None and fix:
            fixed += scaffold_deploy(root)  # заводит файл и строку в .gitignore
            deploy, test, autonomous = read_deploy(root)  # теперь файл есть, команды пустые
        elif deploy is not None and fix:
            patched = patch_deploy(root)  # дописывает недостающие ключи болванки
            if patched:
                fixed += patched
                deploy, test, autonomous = read_deploy(root)
        if deploy is None:
            findings.append("нет %s: команда выката не задана, shipctl merge оставит "
                            "выкат пользователю (болванку заводит devkitctl new или doctor --fix)" % DEPLOY_CONFIG)
        else:
            # В корп-контуре слияние и выкат ведёт процесс компании, shipctl там
            # отказывает честной строкой, и пустой deploy= это норма, а не
            # находка: требовать команду выката значило бы звать чинить исправное.
            if deploy == "" and local:
                pass
            elif deploy == "" and not autonomous:
                findings.append("%s: пустой deploy=, shipctl нечего выкатывать; "
                                "вписать команду выката" % DEPLOY_CONFIG)
            elif deploy == "" and autonomous:
                findings.append("%s: autonomous = true при пустом deploy= (агенту доверен конвейер, "
                                "а катить нечего); вписать команду выката либо снять autonomous" % DEPLOY_CONFIG)
            if test == "":
                findings.append("%s: пустой test=, shipctl merge будет требовать --test на каждый "
                                "вызов, а процедура пачки сочинять его не умеет; вписать команду "
                                "тестов проекта" % DEPLOY_CONFIG)
            rc, _ = run(["git", "-C", str(root), "check-ignore", "-q", DEPLOY_CONFIG])
            if rc != 0:
                if fix and ensure_gitignore(root, DEPLOY_IGNORE):
                    fixed.append(".gitignore: добавлен %s" % DEPLOY_IGNORE)
                else:
                    findings.append("%s не гитигнорнут: адрес и доступы из команды выката "
                                    "утекут в git, добавить %s в .gitignore" % (DEPLOY_CONFIG, DEPLOY_IGNORE))
        if (root / ".devkit").is_dir() and in_git:
            rc, _ = run(["git", "-C", str(root), "check-ignore", "-q", RUN_LOG])
            if rc != 0:
                if fix and ensure_gitignore(root, RUN_LOG, LOG_IGNORE_COMMENT):
                    fixed.append(".gitignore: добавлен %s" % RUN_LOG)
                else:
                    findings.append("%s не гитигнорнут: журнал запусков замусорит status, "
                                    "добавить %s в .gitignore" % (RUN_LOG, RUN_LOG))
    findings += check_links(root)
    for m in fixed:
        print("починено: %s" % m)
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("обвязка в порядке")
    return 0


def layout_only(start):
    """Одна проверка раскладки, без машинной обвязки.

    Полный doctor смотрит на ~/.claude, собранные бинари, tmux и снимок квоты, и
    в CI такой прогон дал бы десяток находок про ненастроенную машину. Тут
    печатаются только находки раскладки, и код выхода стоит по ним.
    """
    root, in_git = project_root(start)
    if not in_git:
        sys.stderr.write("не git-репозиторий: %s\n" % root)
        return 2
    if not layout.is_devkit(root):
        print("раскладка: правило действует на самом devkit, тут проверять нечего")
        return 0
    findings = layout.check(root)
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("раскладка в порядке")
    return 0


def build_binaries(release, out):
    """Сборка бинарей devkit, та же командой с машины и с раннера.

    Собирается тот чекаут devkit, из которого запущен сам devkitctl: версия
    зашивается из его git, и собирать чужое дерево этой команде незачем.
    """
    if not shutil.which("go"):
        sys.stderr.write("go в PATH нет: собирать нечем, Go ставится пакетным менеджером "
                         "(brew install go)\n")
        return 2
    if release:
        findings = build.release(DEVKIT, out or "dist")
    else:
        target = Path(out) if out else update.bin_dir()
        findings = build.local(DEVKIT, target)
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("сборка в порядке")
    return 0


def update_devkit(pin, check, restarted):
    """Установка и обновление devkit готовыми бинарями релиза.

    Раскладку машинного контура делает тот же код, что зовёт doctor --fix:
    второй копии этой работы у установки нет, иначе она разошлась бы с
    доктором в первый же раз.
    """
    main, from_main = devkit_checkout()
    return update.run(main, from_main, pin=pin, check=check, restarted=restarted,
                      machine=lambda: check_machine(True))


def weigh_resident(start, runs, limit, model, prompt):
    # Гейт свежести: замер меряет раскладку devkit на машине, а кладёт её
    # doctor --fix. По вчерашней раскладке замер соврал бы молча, поэтому сперва
    # прогоняются те же проверки доктора, что за раскладку и отвечают. Остальной
    # машинный контур (снимок квоты, tmux) на вес резидента не влияет и мерить
    # не мешает.
    root, _ = project_root(start)
    findings = []
    for check in (check_agent_defs, check_skills):
        f, _ = check(False)
        findings += ["машина: %s" % m for m in f]
    rfindings, _ = rules.check(root, DEVKIT, False, SKIP_DIRS)
    findings += rfindings
    return weigh.measure(root, DEVKIT, findings, runs, limit, prompt, model)


def stats_context(start):
    root, _ = project_root(start)
    directory = context.logs_dir(root)
    if not directory.is_dir():
        sys.stderr.write("журналы сессий не найдены: %s\n"
                         "харнес пишет их по слепку пути проекта, и для %s такой директории нет: "
                         "сессий отсюда не было либо проект открывали из другой директории\n"
                         % (directory, root))
        return 2
    if not context.report(directory, sys.stdout):
        sys.stderr.write("в журналах сессий нет запросов с расходом: %s\n" % directory)
        return 2
    return 0


def stats(start, ctx=False):
    if ctx:
        # Журналы сессий харнес кладёт по слепку пути проекта, а сессия живёт в
        # клоне, поэтому тут корень остаётся клоном и в корп-контуре.
        return stats_context(start)
    root, _ = project_root(start)
    root = Path(corp.pair(root, DEVKIT)[1] or root)
    log_file = root / RUN_LOG
    if not log_file.exists() or log_file.stat().st_size == 0:
        sys.stderr.write("журнал запусков не найден: %s\n" % RUN_LOG)
        return 2

    runs = {}
    total_runs, total_errors = 0, 0

    for ln in log_file.read_text(encoding="utf-8", errors="replace").splitlines():
        parts = ln.split('\t')
        if len(parts) != 4:
            continue
        try:
            code = int(parts[3])
        except ValueError:
            continue

        tool = parts[1]
        cmd = parts[2]
        key = (tool, cmd)

        if key not in runs:
            runs[key] = [0, 0]
        runs[key][0] += 1
        if code != 0:
            runs[key][1] += 1
        total_runs += 1
        if code != 0:
            total_errors += 1

    if total_runs == 0:
        sys.stderr.write("журнал пуст: %s\n" % RUN_LOG)
        return 2

    sorted_runs = sorted(runs.items(), key=lambda x: x[1][0], reverse=True)

    max_len = max(len(f"{t} {c}") for (t, c), _ in sorted_runs)
    for (tool, cmd), (count, errors) in sorted_runs:
        key_str = f"{tool} {cmd}"
        error_pct = round(100 * errors / count)
        print(f"{key_str:<{max_len}}  {count:>3}   ошибок {errors} ({error_pct}%)")

    total_pct = round(100 * total_errors / total_runs)
    print(f"{'итого':<{max_len}}  {total_runs:>3}   ошибок {total_errors} ({total_pct}%)")

    return 0


def new(start, prefix, name, no_board):
    if not no_board and not prefix:
        # Аргументы разбираются до раскладки: отказ не должен оставлять за собой
        # заведённую наполовину директорию.
        sys.stderr.write("нужен --prefix для доски либо --no-board, когда задачи во внешнем трекере\n")
        return 2
    root, in_git = project_root(start)
    # Подключение доводит проект до рабочего состояния само, как corp заводит
    # боковую директорию: пустого места и не-репозитория оно не боится. Иначе
    # первый же шаг инструкции падал бы на записи AGENTS.md, а до shipctl дело
    # доходило бы без git.
    started = []
    if not root.is_dir():
        try:
            root.mkdir(parents=True)
        except OSError as e:
            sys.stderr.write("не завёл директорию %s: %s\n" % (root, e))
            return 1
        started.append("директория %s заведена" % root)
    if not in_git:
        rc, out = run(["git", "init", "-q", str(root)])
        if rc != 0:
            sys.stderr.write("git init %s: %s\n" % (root, out))
            return 1
        in_git = True
        started.append("git-репозиторий заведён: ветку и дерево задачи shipctl берёт отсюда")
    agents = root / rules.AGENTS_FILE
    if agents.exists():
        sys.stderr.write("%s уже есть, проект подключён; проверка: devkitctl doctor\n"
                         % rules.AGENTS_FILE)
        return 2
    text = (DEVKIT / "kit" / "templates" / "AGENTS.project.md").read_text(encoding="utf-8")
    if text.startswith("<!--"):
        text = text[text.index("-->") + 3:].lstrip("\n")
    name = name or root.name
    text = text.replace("<название проекта>", name).replace("<XX>", prefix or "XX")
    agents.write_text(text, encoding="utf-8")
    done = started + ["%s создан из шаблона" % rules.AGENTS_FILE]
    applied, residual = connect_git_hooks(root)
    done.append(applied or residual or "git-хуки уже подключены")
    if no_board:
        done.append("доска не заводилась: вписать в %s, какой это трекер" % rules.AGENTS_FILE)
    else:
        tc = shutil.which("taskctl")
        if tc:
            rc, out = run([tc, "-C", str(root), "init", "--prefix", prefix, "--name", name])
            if rc != 0:
                sys.stderr.write("taskctl init: %s\n" % out)
                return 1
            done.append(out)
        else:
            done.append("taskctl не в PATH; доска заводится после сборки: "
                        "python3 %s/tools/devkitctl/devkitctl.py build && taskctl -C %s init --prefix %s"
                        % (DEVKIT, root, prefix))
        done += scaffold_deploy(root)
    # Тонкие файлы генерятся последними: доска к этому моменту уже заведена, и в
    # импорты попадает RULES.board.md.
    _, generated = rules.check(root, DEVKIT, fix=True, skip_dirs=SKIP_DIRS)
    done += generated
    steps = []
    # Свежий репозиторий стоит на неродившейся ветке, и ветку задачи от неё
    # shipctl не заводит («не нашёл ветку main или master»). Первый коммит
    # поэтому делает подключение, и делает пустым: чужие файлы проекта в него не
    # едут, а ветка рождается. Спрашивается тут HEAD, а не то, кто завёл
    # репозиторий: пустым он бывает и заведённым заранее руками.
    if run(["git", "-C", str(root), "rev-parse", "--verify", "HEAD"])[0] != 0:
        rc, out = run(["git", "-C", str(root), "commit", "--allow-empty", "-q",
                       "-m", "chore: подключение devkit"])
        if rc == 0:
            done.append("первый коммит проекта сделан: без него shipctl start ветку задачи "
                        "не заведёт")
        else:
            steps.append("первый коммит проекта: git -C %s commit --allow-empty "
                         "-m \"chore: подключение devkit\"; сейчас он не прошёл (%s), а без "
                         "коммита ветка не рождается и shipctl start её не найдёт"
                         % (root, out.replace("\n", " ")[:120]))
    print("\n".join(done))
    steps += deploy_steps(root)
    if no_board:
        steps.append("%s: вписать, какой это трекер и как в нём ведутся задачи"
                     % (root / rules.AGENTS_FILE))
    print_remaining(steps, "devkitctl doctor -C %s" % root)
    return 0


def deploy_steps(root, corp_contour=False):
    """Незаполненное в обвязке выката словами шага, а не находкой доктора:
    подключение печатает это в одном списке с остальным недоделанным. В
    корп-контуре команду выката не спрашивают: выкат там ведёт процесс
    компании."""
    deploy, test, _ = read_deploy(root)
    if deploy is None:
        return []
    keys = (("test", test),) if corp_contour else (("test", test), ("deploy", deploy))
    miss = [k for k, v in keys if not v]
    if not miss:
        return []
    return ["обвязка выката %s: вписать %s"
            % (root / DEPLOY_CONFIG, ", ".join("%s =" % k for k in miss))]


def print_remaining(steps, check):
    """Хвост подключения: что осталось человеку и чем это проверяется. Пустой
    список тоже печатается: «шагов не осталось» и молчание команды снаружи
    выглядят одинаково, а значат разное."""
    if steps:
        print("\nосталось сделать:")
        for i, s in enumerate(steps, 1):
            print("%d. %s" % (i, s))
    else:
        print("\nручных шагов не осталось.")
    print("проверка: %s" % check)


def corp_connect(start, prefix, name, contour="", key="", branch="", remote=""):
    # Подключение корп-проекта и оно же восстановление обвязки: заведённое не
    # переписывается, доводится только недостающее, поэтому повторный прогон
    # после переклонирования возвращает клон в рабочее состояние, а доску в
    # боковой директории не трогает.
    root, in_git = project_root(start)
    if not in_git:
        sys.stderr.write("%s не git-репозиторий: corp подключает клон корп-репозитория, "
                         "обычный проект подключает devkitctl new\n" % root)
        return 2
    clone = Path(corp.checkout(root) or root)
    local = Path(corp.local_dir(clone, DEVKIT) or (clone.parent / (clone.name + "-local")))
    board = local / "docs" / "TASKS.md"
    if not board.exists() and not prefix:
        sys.stderr.write("нужен --prefix для доски боковой директории (%s)\n" % local)
        return 2
    # Префикс доски, совпавший с ключом проекта в трекере, снимает правило про
    # локальный ID: рубеж следов не отличает ID строки доски от ключа тикета
    # (DK-124). На заведённой доске это уже находка доктора, а на незаведённой
    # префикс ещё выбирается, и дешевле остановиться здесь.
    bound_key = key or corp.tracker_value(local, "key", DEVKIT)
    if prefix and bound_key and prefix == bound_key.upper():
        if not board.exists():
            sys.stderr.write("префикс доски %s совпадает с ключом проекта в трекере: рубеж следов "
                             "не отличит локальный ID доски от ключа тикета и правило про ID на "
                             "этом проекте работать не будет, взять другой --prefix\n" % prefix)
            return 2
        sys.stderr.write("предупреждение: префикс доски %s совпадает с ключом проекта в трекере, "
                         "рубеж следов на этой паре правило про локальный ID снимает\n" % prefix)
    done = []
    if not local.is_dir():
        local.mkdir(parents=True)
        done.append("боковая директория %s заведена" % local)
    if not (local / ".git").exists():
        rc, out = run(["git", "init", "-q", str(local)])
        if rc != 0:
            sys.stderr.write("git init %s: %s\n" % (local, out))
            return 1
        done.append("git-репозиторий боковой директории заведён: доска пушится в личный "
                    "приватный remote, а не в корп-origin")
    (local / "docs" / "tasks").mkdir(parents=True, exist_ok=True)
    (local / ".devkit").mkdir(exist_ok=True)
    if not (local / rules.AGENTS_FILE).exists():
        text = (DEVKIT / "kit" / "templates" / "AGENTS.project.md").read_text(encoding="utf-8")
        if text.startswith("<!--"):
            text = text[text.index("-->") + 3:].lstrip("\n")
        text = text.replace("<название проекта>", name or clone.name).replace("<XX>", prefix or "XX")
        (local / rules.AGENTS_FILE).write_text(text, encoding="utf-8")
        done.append("%s создан из шаблона в боковой директории" % rules.AGENTS_FILE)
    if not board.exists():
        tc = shutil.which("taskctl")
        if not tc:
            sys.stderr.write("taskctl не в PATH, доску заводить нечем: собрать утилиты "
                             "(python3 %s/tools/devkitctl/devkitctl.py build) и повторить\n" % DEVKIT)
            return 1
        rc, out = run([tc, "-C", str(local), "init", "--prefix", prefix, "--name", name or clone.name])
        if rc != 0:
            sys.stderr.write("taskctl init: %s\n" % out)
            return 1
        done.append(out)
    done += scaffold_deploy(local)
    applied, residual = connect_git_hooks(local)
    if applied or residual:
        done.append(applied or residual)
    _, generated = rules.check(local, DEVKIT, fix=True, skip_dirs=SKIP_DIRS)
    done += ["боковая директория: %s" % g for g in generated]
    rel = corp.ensure_redirect(clone, local)
    done.append("редирект корня: git config devkit.local %s" % rel)
    for bkey, bval in corp.ensure_binding(local, clone,
                                          {"contour": contour, "key": key, "branch": branch}):
        why = (" (по нему обвязка клона находится после переклонирования)"
               if bkey == "repo" else "")
        done.append("%s: вписан %s = %s%s" % (corp.TRACKER, bkey, bval, why))
    if contour:
        cpath = corp.ensure_contour(contour)
        if cpath:
            done.append("контур компании %s заведён болванкой: адрес и пользователь вписать, "
                        "таблицу [status] сверить с трекером" % cpath)
    if remote and not corp.git(local, "remote"):
        rc, out = run(["git", "-C", str(local), "remote", "add", "origin", remote])
        if rc != 0:
            sys.stderr.write("git remote add: %s\n" % out)
            return 1
        done.append("remote доски: origin %s" % remote)
    imports, hfindings = corp_profiles(local)
    _, thin_fixed = corp_thin(clone, local, imports, fix=True)
    done += thin_fixed
    for n in corp.ensure_exclude(clone, corp_thin_names(imports)):
        done.append(".git/info/exclude: спрятан %s" % n)
    for hook, state, chained in corp.ensure_hooks(clone, DEVKIT):
        done.append("хук %s: цепочка %s%s"
                    % (hook, state, " (чужой хук переехал в %s.chained)" % hook if chained else ""))
    print("\n".join(done + hfindings))
    print_remaining(corp.remaining(clone, local, DEVKIT) + deploy_steps(local, corp_contour=True),
                    "devkitctl doctor -C %s, дальше trackctl status -C %s" % (clone, local))
    return 0


def main(argv):
    ap = argparse.ArgumentParser(prog="devkitctl", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    d = sub.add_parser("doctor", help="проверить обвязку проекта")
    d.add_argument("-C", dest="dir", default=".", help="директория проекта")
    d.add_argument("--fix", action="store_true",
                   help="доводить обвязку до актуальной (additive, заполненное не трогает)")
    d.add_argument("--layout", action="store_true",
                   help="только раскладка devkit, без машинной обвязки (шаг CI)")
    n = sub.add_parser("new", help="подключить новый проект")
    n.add_argument("-C", dest="dir", default=".", help="директория проекта")
    n.add_argument("--prefix", default="", help="префикс ID задач доски, заглавными (XR)")
    n.add_argument("--name", default="", help="название проекта, по умолчанию имя директории")
    n.add_argument("--no-board", action="store_true", help="без доски, задачи во внешнем трекере")
    c = sub.add_parser("corp", help="подключить корп-проект (боковая директория и обвязка клона)")
    c.add_argument("-C", dest="dir", default=".", help="директория корп-клона")
    c.add_argument("--prefix", default="", help="префикс ID задач доски, заглавными (XR)")
    c.add_argument("--name", default="", help="название проекта, по умолчанию имя директории клона")
    c.add_argument("--contour", default="", help="имя контура компании ~/.devkit/tracker/<имя>.local")
    c.add_argument("--key", default="", help="ключ проекта в трекере (ABC), отличный от --prefix")
    c.add_argument("--branch", default="", help="шаблон ветки, по умолчанию решает shipctl")
    c.add_argument("--remote", default="", help="личный приватный remote доски боковой директории")
    b = sub.add_parser("build", help="собрать бинари devkit с зашитой версией")
    b.add_argument("--release", action="store_true",
                   help="четыре пары GOOS/GOARCH, тарболлы и SHA256SUMS")
    b.add_argument("--out", default="",
                   help="каталог назначения: по умолчанию тот же, куда ставит update, "
                        "а под --release dist")
    u = sub.add_parser("update", help="поставить или обновить devkit бинарями релиза")
    u.add_argument("--pin", action="store_true",
                   help="перевести чекаут с ветки на новейший тег (первая установка)")
    u.add_argument("--check", action="store_true",
                   help="только рассказать про версии, ничего не двигая")
    u.add_argument(update.RESTART_FLAG, dest="restarted", action="store_true",
                   help=argparse.SUPPRESS)
    s = sub.add_parser("stats", help="сводка по журналу запусков")
    s.add_argument("-C", dest="dir", default=".", help="директория проекта")
    s.add_argument("--context", action="store_true",
                   help="разбивка объёма по журналам сессий вместо журнала запусков")
    w = sub.add_parser("weigh", help="живой замер веса резидента")
    w.add_argument("-C", dest="dir", default=".", help="директория проекта")
    w.add_argument("--runs", type=int, default=weigh.RUNS,
                   help="сколько пар прогонов, по ним считается разброс (по умолчанию %d)" % weigh.RUNS)
    w.add_argument("--limit", type=int, default=weigh.LIMIT,
                   help="потолок резидента в токенах, выше него код 1 (по умолчанию %d)" % weigh.LIMIT)
    w.add_argument("--model", default="", help="модель прогона, по умолчанию модель клиента")
    w.add_argument("--prompt", default=weigh.PROMPT, help="запрос прогона, у обоих он один")
    a = ap.parse_args(argv)
    if a.cmd == "doctor":
        rc = layout_only(a.dir) if a.layout else doctor(a.dir, a.fix)
    elif a.cmd == "new":
        rc = new(a.dir, a.prefix.upper(), a.name, a.no_board)
    elif a.cmd == "corp":
        rc = corp_connect(a.dir, a.prefix.upper(), a.name, a.contour, a.key.upper(),
                          a.branch, a.remote)
    elif a.cmd == "weigh":
        rc = weigh_resident(a.dir, a.runs, a.limit, a.model, a.prompt)
    elif a.cmd == "build":
        rc = build_binaries(a.release, a.out)
    elif a.cmd == "update":
        rc = update_devkit(a.pin, a.check, a.restarted)
    else:
        rc = stats(a.dir, a.context)
    # Журнал запусков в корп-контуре лежит там же, где остальные рабочие файлы,
    # то есть в боковой директории: в дереве клона .devkit нет. У build своего
    # -C нет, он собирает чекаут devkit, и запуск ложится в его же журнал.
    root = project_root(getattr(a, "dir", str(DEVKIT)))[0]
    log_run(Path(corp.pair(root, DEVKIT)[1] or root), a.cmd, rc)
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
