#!/usr/bin/env python3
"""devkitctl: обвязка devkit в проекте одной командой.

  devkitctl new --prefix XX [--name "..."] [--no-board] [-C dir]
      подключить проект: CLAUDE.md из шаблона (импорты пересчитываются под
      реальный путь до devkit), git-хуки, доска через taskctl init, болванка
      .devkit/deploy.local для shipctl; --no-board для внешнего трекера

  devkitctl doctor [--fix] [-C dir]
      проверить обвязку проекта: импорты CLAUDE.md разворачиваются, git-хуки
      подключены, инварианты доски (taskctl lint), обвязка выката
      (.devkit/deploy.local есть, с командой и гитигнорнута), локальные
      markdown-ссылки не битые; и машинный контур: PostToolUse-хуки,
      SessionStart-хук освежения квоты, хуки уведомлений вместе с бэкендом,
      которым их слать, бинари утилит devkit в PATH и не старее
      исходников, определения агентов в ~/.claude/agents, tmux и сам снимок
      квоты ~/.devkit/quota.local;
      --fix additive доводит обвязку (хуки, болванка deploy.local, .gitignore,
      сборка бинарей, копия определений агентов), заполненное не трогает,
      неоднозначное оставляет находкой

  devkitctl stats [-C dir]
      сводка по журналу запусков .devkit/log: частота команд (утилита, команда),
      доля ошибок, отсортировано по частоте убыванием, в конце итоговая строка
      по всему журналу; битые строки молча пропускаются

Выход 0 всё в порядке, 1 есть находки, 2 ошибка запуска.
"""
import argparse
import importlib.util
import json
import os
import re
import shutil
import subprocess
import sys
from datetime import datetime
from pathlib import Path

DEVKIT = Path(__file__).resolve().parent.parent
HOOK_SCRIPTS = ("check-symbols.py", "check-memory.py", "check-sensitive.py")
SESSION_HOOK = "quota-refresh.sh"
NOTIFY_HOOK = "notify.py"
NOTIFY_EVENTS = ("Notification", "SubagentStop")
BINARIES = ("taskctl", "shipctl", "agentctl", "regcheck")
AGENTS_DIR = "~/.claude/agents"
QUOTA_FILE = "~/.devkit/quota.local"
# Порог свежести снимка держит agentctl (snapshotMaxAge в agentctl/quota.go), тут
# его копия: доктор про снимок говорит то же самое, что pick, иначе одна утилита
# звала бы переснимать, а вторая молчала.
QUOTA_MAX_AGE = 45 * 60
QUOTA_TIME_FORMATS = ("%Y-%m-%dT%H:%M", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%d %H:%M")
DEPLOY_CONFIG = ".devkit/deploy.local"
DEPLOY_IGNORE = ".devkit/*.local"
RUN_LOG = ".devkit/log"
DEPLOY_TEMPLATE = (
    "# Обвязка выката для shipctl (гитигнорнут: в команде выката обычно адрес\n"
    "# или роль машины, её место в локальном, а не в коммитимом). shipctl merge\n"
    "# берёт команду отсюда, --deploy на каждый вызов передавать не нужно.\n"
    "# Объект выката бывает не только серверным: для приложения сюда идёт\n"
    "# сборка, подпись и заливка в канал обновлений, а не серверный рестарт.\n"
    "# autonomous=true отдаёт агенту весь конвейер: готовую задачу (тесты\n"
    "# зелёные, ревью чистое) он сам сливает, пушит в origin и катит на прод.\n"
    "# false оставляет слияние, пуш и выкат за пользователем.\n"
    "deploy =\n"
    "autonomous = false\n"
)
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
    findings = []
    for dirpath, dirnames, filenames in os.walk(root):
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
    # Пара (deploy, autonomous). None вместо deploy это «файла нет», иначе
    # значение deploy= (может быть пустым) и флаг autonomous.
    f = root / DEPLOY_CONFIG
    if not f.exists():
        return None, None
    deploy = ""
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
        elif key_stripped == "autonomous":
            autonomous = val_stripped.lower() in ("1", "true", "t", "yes", "y", "on")
    return deploy, autonomous


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


def log_run(root, cmd, code):
    # Журнал запусков, общий с taskctl/shipctl/regcheck: статистика, какие
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


def go_bin_dir():
    gobin = os.environ.get("GOBIN")
    if gobin:
        return Path(gobin)
    gopath = os.environ.get("GOPATH")
    root = Path(gopath) if gopath else Path(os.path.expanduser("~/go"))
    return root / "bin"


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
    # Основной чекаут devkit и признак, что скрипт запущен из него. В worktree
    # ветки задачи mtime исходников это момент выкладки, а не правки, поэтому
    # свежесть бинарей там не меряется и сборка не запускается: машинный бинарь
    # с непроверенной ветки уехал бы во все проекты сразу.
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


def check_binaries(fix):
    findings, fixed = [], []
    gobin = go_bin_dir()
    main, from_main = devkit_checkout()
    for name in BINARIES:
        src = max((p.stat().st_mtime for p in (DEVKIT / name).glob("*.go")), default=0)
        path = shutil.which(name)
        if path and (os.path.getmtime(path) >= src or not from_main):
            continue
        why = "не в PATH" if not path else "старее исходников devkit"
        build = "cd %s/%s && go build -o %s/%s ." % (main, name, gobin, name)
        target = gobin / name
        if target.exists() and os.path.getmtime(target) >= src:
            # Свежая сборка уже лежит на месте, дело за PATH.
            conflict = path_winner(name, target, gobin)
            if conflict:
                findings.append(conflict)
            continue
        if not from_main:
            findings.append("%s %s, а devkit тут выложен worktree ветки задачи: mtime исходников "
                            "это момент выкладки, и машинный бинарь с непроверенной ветки собирать "
                            "нельзя; пересобрать из основного чекаута: %s" % (name, why, build))
            continue
        if not shutil.which("go"):
            findings.append("%s %s, а go в PATH нет: собирать нечем, Go ставится пакетным менеджером "
                            "(brew install go), потом %s" % (name, why, build))
            continue
        if not fix:
            findings.append("%s %s: %s" % (name, why, build))
            continue
        gobin.mkdir(parents=True, exist_ok=True)
        rc, out = run(["go", "build", "-o", str(target), "."], cwd=str(DEVKIT / name))
        if rc != 0:
            findings.append("%s %s, сборка не прошла: %s" % (name, why, out))
            continue
        fixed.append("%s собран в %s" % (name, target))
        conflict = path_winner(name, target, gobin)
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
    src_dir = main / "agents" if (main / "agents").is_dir() else DEVKIT / "agents"
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


def check_notify_hook(text, settings):
    findings = []
    events = hook_events(text, NOTIFY_HOOK)
    if events is None:
        missing = [] if NOTIFY_HOOK in text else list(NOTIFY_EVENTS)
    else:
        missing = [e for e in NOTIFY_EVENTS if e not in events]
    if missing:
        findings.append("хук %s не подключён на события %s в %s: сессия молча стоит, когда ждёт "
                        "разрешения, и не говорит, что субагент отработал (hooks/README.md)"
                        % (NOTIFY_HOOK, ", ".join(missing), settings))
    # Выбор бэкенда живёт в самом уведомителе, второй его копии тут нет.
    src = DEVKIT / "hooks" / NOTIFY_HOOK
    spec = importlib.util.spec_from_file_location("devkit_notify", src)
    if not src.exists() or spec is None:
        return findings
    mod = importlib.util.module_from_spec(spec)
    try:
        spec.loader.exec_module(mod)
    except (OSError, SyntaxError):
        return findings
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
    # Машинный контур, общий для всех проектов: хуки Claude Code, бинари devkit,
    # определения агентов, tmux и снимок квоты.
    findings, fixed = [], []
    settings = Path(os.path.expanduser("~/.claude/settings.json"))
    text = settings.read_text(encoding="utf-8") if settings.exists() else ""
    for script in HOOK_SCRIPTS:
        if script not in text:
            findings.append("PostToolUse-хук %s не подключён в %s (hooks/README.md)" % (script, settings))
    if SESSION_HOOK not in text:
        findings.append("SessionStart-хук %s не подключён в %s: снимок квоты сам не освежается, "
                        "и корректор pick рано или поздно останется с протухшим (hooks/README.md)"
                        % (SESSION_HOOK, settings))
    findings += check_notify_hook(text, settings)
    for check in (check_binaries, check_agent_defs):
        f, d = check(fix)
        findings += f
        fixed += d
    if not shutil.which("tmux"):
        findings.append("tmux не в PATH: agentctl quota refresh не снимет панель /usage, "
                        "корректор останется без снимка; ставится пакетным менеджером (brew install tmux)")
    quota = Path(os.path.expanduser(QUOTA_FILE))
    if not quota.exists():
        findings.append("нет снимка квоты %s: корректор pick двигать вердикт не будет; "
                        "снять: agentctl quota refresh" % quota)
    else:
        taken = quota_taken(quota)
        if taken is None:
            findings.append("в снимке квоты %s не разобран момент снятия (строка taken =), "
                            "возраст не проверить; переснять: agentctl quota refresh" % quota)
        else:
            age = (datetime.now() - taken).total_seconds()
            if age > QUOTA_MAX_AGE:
                findings.append("снимок квоты %s протух (возраст %s при пороге %s): профицит по нему уже не считается, "
                                "сдвиг вверх потерян; переснять: agentctl quota refresh"
                                % (quota, human_age(age), human_age(QUOTA_MAX_AGE)))
    return findings, fixed


def doctor(start, fix=False):
    findings, fixed = [], []
    root, in_git = project_root(start)
    if not in_git:
        findings.append("не git-репозиторий: %s" % root)
    claude = root / "CLAUDE.md"
    if not claude.exists():
        findings.append("нет CLAUDE.md в корне проекта; подключение: devkitctl new --prefix XX")
    else:
        for i, ln in enumerate(claude.read_text(encoding="utf-8").splitlines(), 1):
            ln = ln.strip()
            if not ln.startswith("@"):
                continue
            target = Path(os.path.expanduser(ln[1:]))
            if not target.is_absolute():
                target = root / target
            if not target.exists():
                findings.append("CLAUDE.md:%d: импорт %s не разворачивается (devkit склонирован рядом?)" % (i, ln))
    if in_git and check_git_hooks(root):
        if fix:
            done, residual = connect_git_hooks(root)
            (fixed if done else findings).append(done or residual)
        else:
            findings.append(check_git_hooks(root))
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
        deploy, autonomous = read_deploy(root)
        if deploy is None and fix:
            fixed += scaffold_deploy(root)  # заводит файл и строку в .gitignore
            deploy, autonomous = read_deploy(root)  # теперь файл есть, команда пустая
        if deploy is None:
            findings.append("нет %s: команда выката не задана, shipctl merge оставит "
                            "выкат пользователю (болванку заводит devkitctl new или doctor --fix)" % DEPLOY_CONFIG)
        else:
            if deploy == "" and not autonomous:
                findings.append("%s: пустой deploy=, shipctl нечего выкатывать; "
                                "вписать команду выката" % DEPLOY_CONFIG)
            elif deploy == "" and autonomous:
                findings.append("%s: autonomous = true при пустом deploy= (агенту доверен конвейер, "
                                "а катить нечего); вписать команду выката либо снять autonomous" % DEPLOY_CONFIG)
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


def stats(start):
    root, _ = project_root(start)
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
    root, in_git = project_root(start)
    claude = root / "CLAUDE.md"
    if claude.exists():
        sys.stderr.write("CLAUDE.md уже есть, проект подключён; проверка: devkitctl doctor\n")
        return 2
    if not no_board and not prefix:
        sys.stderr.write("нужен --prefix для доски либо --no-board, когда задачи во внешнем трекере\n")
        return 2
    text = (DEVKIT / "templates" / "CLAUDE.project.md").read_text(encoding="utf-8")
    if text.startswith("<!--"):
        text = text[text.index("-->") + 3:].lstrip("\n")
    name = name or root.name
    text = text.replace("<название проекта>", name).replace("<XX>", prefix or "XX")
    rel = Path(os.path.relpath(DEVKIT, root)).as_posix()
    if rel != "../devkit":
        # Шаблон рассчитан на devkit в соседней директории; когда он лежит
        # иначе, импорты без пересчёта молча не развернутся.
        text = text.replace("@../devkit/", "@%s/" % rel)
    claude.write_text(text, encoding="utf-8")
    done = ["CLAUDE.md создан из шаблона"]
    if in_git:
        applied, residual = connect_git_hooks(root)
        done.append(applied or residual or "git-хуки уже подключены")
    else:
        done.append("не git-репозиторий, git-хуки не подключались")
    if no_board:
        done.append("доска не заводилась: убрать импорт RULES.board.md из CLAUDE.md и вписать внешний трекер")
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
                        "cd %s/taskctl && go build -o ~/go/bin/taskctl . && taskctl -C %s init --prefix %s"
                        % (DEVKIT, root, prefix))
        done += scaffold_deploy(root)
    print("\n".join(done))
    return 0


def main(argv):
    ap = argparse.ArgumentParser(prog="devkitctl", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    d = sub.add_parser("doctor", help="проверить обвязку проекта")
    d.add_argument("-C", dest="dir", default=".", help="директория проекта")
    d.add_argument("--fix", action="store_true",
                   help="доводить обвязку до актуальной (additive, заполненное не трогает)")
    n = sub.add_parser("new", help="подключить новый проект")
    n.add_argument("-C", dest="dir", default=".", help="директория проекта")
    n.add_argument("--prefix", default="", help="префикс ID задач доски, заглавными (XR)")
    n.add_argument("--name", default="", help="название проекта, по умолчанию имя директории")
    n.add_argument("--no-board", action="store_true", help="без доски, задачи во внешнем трекере")
    s = sub.add_parser("stats", help="сводка по журналу запусков")
    s.add_argument("-C", dest="dir", default=".", help="директория проекта")
    a = ap.parse_args(argv)
    if a.cmd == "doctor":
        rc = doctor(a.dir, a.fix)
    elif a.cmd == "new":
        rc = new(a.dir, a.prefix.upper(), a.name, a.no_board)
    else:
        rc = stats(a.dir)
    log_run(project_root(a.dir)[0], a.cmd, rc)
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
