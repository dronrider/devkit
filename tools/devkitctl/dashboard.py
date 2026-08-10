#!/usr/bin/env python3
"""Носитель дашборда: launchd-агент ru.devkit.dashboard держит `dashboard
serve` с KeepAlive, кладёт и поднимает его `devkitctl doctor --fix` по
образцу сторожка цикла цели (watch.py).

Сам бинарь dashboard собирается и ставится в PATH как остальные утилиты
(devkitctl build / update), агент показывает на него, а не на чекаут: замену
бинаря выкатом демон замечает сам по иноде и переживает без правки plist.
Доктор здесь проверяет три вещи: агент положен и показывает на живой бинарь,
launchd его поднял, и /healthz отвечает без ошибок конфига. Живость меряется
после первого старта сервера: конфиг с секретом и портом кладёт сам serve, и
пока файла нет, стучаться некуда, агент только что взведён. Молчание
различимо: каждая нехватка называется находкой со своей командой починки.

На платформах без launchd носителя пока нет, и доктор говорит об этом
находкой, когда дашбордом на машине пользуются (есть конфиг или бинарь):
команду ставят в systemd руками.
"""
import json
import os
import shutil
import subprocess
import sys
import urllib.request
from pathlib import Path

LABEL = "ru.devkit.dashboard"
PLIST = "~/Library/LaunchAgents/%s.plist" % LABEL
LOG = "~/.devkit/dashboard.log"
CONF = "~/.devkit/dashboard.local"
DEFAULT_PORT = 7112
HEALTHZ_TIMEOUT = 3


def home_path(home, path):
    """Путь из шапки модуля с подставленным домом: тесты гоняют проверку на
    своём доме, живой прогон на настоящем."""
    tail = path[2:] if path.startswith("~/") else path
    return Path(home) / tail


def default_home():
    return Path(os.path.expanduser("~"))


def conf_port(home):
    """Порт из ~/.devkit/dashboard.local (строка `port = N`), без файла или с
    непонятной строкой умолчание 7112. Разбор нарочно терпимый: строгий держит
    сам сервер, доктору порт нужен только чтобы постучаться в /healthz."""
    try:
        text = home_path(home, CONF).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return DEFAULT_PORT
    for ln in text.splitlines():
        key, sep, val = ln.partition("=")
        if sep and key.strip() == "port":
            try:
                port = int(val.strip())
            except ValueError:
                return DEFAULT_PORT
            return port if 0 < port < 65536 else DEFAULT_PORT
    return DEFAULT_PORT


# PATH launchd-агента собирается из трёх частей: системное умолчание launchd,
# из-за которого дефект и случился, и брю-каталоги обеих архитектур.
SYSTEM_PATH = ("/usr/bin", "/bin", "/usr/sbin", "/sbin")
BREW_PATH = ("/opt/homebrew/bin", "/usr/local/bin")


def agent_path(binary):
    """PATH для plist: каталог бинаря devkit, системные пути launchd и
    брю-каталоги, которые есть на машине. Считается на doctor --fix по самой
    машине (наличием каталогов), а не срезом живого PATH пользователя: срез
    менялся бы от сессии к сессии, и доктор переписывал бы агента на каждом
    прогоне. Без этой строки демон живёт с системным PATH launchd и не
    находит tmux (дефект шага 5 сценария DK-217)."""
    parts = [str(Path(binary).parent)]
    parts += list(SYSTEM_PATH)
    parts += [p for p in BREW_PATH if os.path.isdir(p)]
    out = []
    for p in parts:
        if p not in out:
            out.append(p)
    return ":".join(out)


def plist_text(binary, log):
    """Тело launchd-агента: KeepAlive держит демон живым, журнал процесса
    совмещён с журналом сервера (обе стороны пишут дозаписью), а PATH задан
    явно, потому что умолчание launchd не знает ни брю, ни каталога бинарей."""
    return "\n".join([
        '<?xml version="1.0" encoding="UTF-8"?>',
        '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" '
        '"http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
        '<plist version="1.0">',
        '<dict>',
        '  <key>Label</key><string>%s</string>' % LABEL,
        '  <key>ProgramArguments</key>',
        '  <array>',
        '    <string>%s</string>' % binary,
        '    <string>serve</string>',
        '  </array>',
        '  <key>EnvironmentVariables</key>',
        '  <dict>',
        '    <key>PATH</key><string>%s</string>' % agent_path(binary),
        '  </dict>',
        '  <key>KeepAlive</key><true/>',
        '  <key>RunAtLoad</key><true/>',
        '  <key>StandardOutPath</key><string>%s</string>' % log,
        '  <key>StandardErrorPath</key><string>%s</string>' % log,
        '</dict>',
        '</plist>',
        '',
    ])


def binary_of(text):
    """Путь бинаря, прописанный в готовом plist: первый <string> внутри
    ProgramArguments."""
    for ln in text.splitlines():
        ln = ln.strip()
        if ln.startswith("<string>") and ln.endswith("</string>"):
            val = ln[len("<string>"):-len("</string>")]
            if val != LABEL:
                return val
    return ""


def launchctl(args, call=None):
    call = subprocess.run if call is None else call
    try:
        p = call(["launchctl"] + list(args), stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return 127, str(e)
    return p.returncode, (p.stdout or "").strip()


def loaded(call=None):
    return launchctl(["list", LABEL], call)[0] == 0


def reload_agent(plist, call=None):
    """Перевзвести агента: снять прежнего и поднять заново, как у сторожка."""
    target = "gui/%d" % os.getuid()
    launchctl(["bootout", "%s/%s" % (target, LABEL)], call)
    code, out = launchctl(["bootstrap", target, str(plist)], call)
    if code == 0:
        return ""
    code, out = launchctl(["load", "-w", str(plist)], call)
    return "" if code == 0 else out


def fetch_healthz(port):
    """Ответ /healthz строкой JSON; OSError и таймаут отдаются исключением."""
    with urllib.request.urlopen("http://127.0.0.1:%d/healthz" % port,
                                timeout=HEALTHZ_TIMEOUT) as resp:
        return resp.read().decode("utf-8", "replace")


def probe(home, fetch=None):
    """Находка по живости поднятого дашборда, пустая строка если всё отвечает.

    Ошибки конфига из /healthz тоже находка: сервер с пустым списком корней
    честно работает, но доску не покажет, и молчать об этом нельзя."""
    fetch = fetch_healthz if fetch is None else fetch
    port = conf_port(home)
    log = home_path(home, LOG)
    try:
        body = fetch(port)
    except Exception as e:
        return ("дашборд поднят, но /healthz на порту %d не отвечает (%s): "
                "смотреть журнал %s" % (port, e, log))
    try:
        errors = json.loads(body).get("errors") or []
    except ValueError:
        return "дашборд отвечает на порту %d не своим /healthz: %.80s" % (port, body)
    if errors:
        return "в /healthz дашборда ошибки конфига: %s" % "; ".join(str(e) for e in errors)
    return ""


def check(fix=False, main=None, from_main=True, home=None, platform=None,
          call=None, which=None, fetch=None):
    """Носитель дашборда в машинном контуре доктора.

    Хоть plist и показывает на бинарь из PATH, а не на чекаут, класть его с
    worktree ветки задачи нельзя по общему правилу: на машину едет только
    проверенное, и доводка отсылает в основной чекаут, как у сторожка."""
    home = default_home() if home is None else home
    platform = sys.platform if platform is None else platform
    which = shutil.which if which is None else which
    binary = which("dashboard")
    if platform != "darwin":
        if binary or home_path(home, CONF).exists():
            return ["носителя дашборда на платформе %s пока нет: держать dashboard serve "
                    "своим расписанием (systemd) и смотреть /healthz на порту %d"
                    % (platform, conf_port(home))], []
        return [], []
    if not binary:
        return ["бинаря dashboard нет в PATH: дашборд не поднять; поставить бинари "
                "(devkitctl update или build) и повторить doctor --fix"], []
    plist = home_path(home, PLIST)
    log = home_path(home, LOG)
    want = plist_text(binary, log)
    have = plist.read_text(encoding="utf-8", errors="replace") if plist.exists() else ""
    if have != want:
        why = ("дашборд не подключён" if not have else
               "launchd-агент дашборда показывает на %s" % (binary_of(have) or "невесть что"))
        if not fix:
            return ["%s: доска с телефона не видна, поднять: devkitctl doctor --fix" % why], []
        if not from_main:
            return ["%s, а devkit тут выложен worktree ветки задачи: чинить из основного "
                    "чекаута %s" % (why, main)], []
        plist.parent.mkdir(parents=True, exist_ok=True)
        plist.write_text(want, encoding="utf-8")
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента дашборда %s: %s" % (plist, err)], []
        return [], ["дашборд подключён launchd-агентом %s (порт %d, журнал %s)"
                    % (LABEL, conf_port(home), log)]
    if not loaded(call):
        if not fix:
            return ["launchd-агент дашборда %s положен, но не поднят: доска с телефона "
                    "не видна, поднять: devkitctl doctor --fix" % LABEL], []
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента дашборда %s: %s" % (plist, err)], []
        return [], ["дашборд поднят launchd-агентом %s" % LABEL]
    # Живость меряется по /healthz только после первого старта сервера: конфиг
    # с секретом кладёт сам serve, и пока файла нет, сервер ещё не поднимался
    # (агента только что взвели), стучаться некуда и не в какой порт.
    if not home_path(home, CONF).exists():
        return [], []
    bad = probe(home, fetch)
    if bad:
        return [bad], []
    return [], []
