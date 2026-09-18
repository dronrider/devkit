#!/usr/bin/env python3
"""Носитель дашборда: launchd-агент ru.devkit.dashboard держит `dashboard
serve` с KeepAlive, кладёт и поднимает его `devkitctl doctor --fix` по
образцу сторожка цикла цели (watch.py).

Сам бинарь dashboard собирается и ставится в PATH как остальные утилиты
(devkitctl build / update), агент показывает на него, а не на чекаут: замену
бинаря выкатом демон замечает сам по иноде и переживает без правки plist.
Доктор здесь проверяет три вещи: агент положен и показывает на живой бинарь,
launchd его поднял, и /healthz отвечает без ошибок конфига. Живость меряется
после первого старта сервера: строку root в конфиг кладёт уже сам доктор
(ensure_conf), а секрет входа только serve, и пока его там нет, стучаться
некуда, сервер ещё не поднимался. Молчание различимо: каждая нехватка
называется находкой со своей командой починки.

На платформах без launchd носителя пока нет, и доктор говорит об этом
находкой, когда дашбордом на машине пользуются (есть конфиг или бинарь):
команду ставят в systemd руками.
"""
import json
import os
import shutil
import sys
import time
import urllib.request
from pathlib import Path

import launchd

LABEL = "ru.devkit.dashboard"
PLIST = "~/Library/LaunchAgents/%s.plist" % LABEL
LOG = "~/.devkit/dashboard.log"
CONF = "~/.devkit/dashboard.local"
DEFAULT_PORT = 7112
HEALTHZ_TIMEOUT = 3
# Ждём секрет входа, который кладёт сам `serve` при первом старте: launchd
# поднимает процесс не мгновенно, и без этого ожидания первый прогон
# devkitctl update/doctor --fix чаще всего не застал бы токен готовым.
LOGIN_WAIT = 5.0
LOGIN_POLL = 0.2


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


def default_root(main):
    """Корень поиска проектов по умолчанию: родитель чекаута devkit. Клон по
    CONNECT.md лежит в `~/projects`, и родитель напрашивается сам; чекаут в
    другом месте даёт другой корень, а не пустой список (решение DK-822)."""
    return str(Path(main).resolve().parent)


def ensure_conf(home, main):
    """Заводит `~/.devkit/dashboard.local` со строкой `root`, когда файла ещё
    нет вовсе: без неё сервер стартует с пустым списком корней, и строка из
    CONNECT.md доводит только до «нет ни одной строки root» в /healthz.
    Существующий конфиг не трогается, даже без строки root в нём: человек мог
    убрать её нарочно. Зовётся раньше взвода агента, чтобы первый же старт
    `serve` увидел готовый конфиг, а не пустой."""
    path = home_path(home, CONF)
    if path.exists():
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("root = %s\n" % default_root(main), encoding="utf-8")
    path.chmod(0o600)


def _read_token(home):
    path = home_path(home, CONF)
    if not path.exists():
        return ""
    text = path.read_text(encoding="utf-8", errors="replace")
    for ln in text.splitlines():
        key, sep, val = ln.partition("=")
        if sep and key.strip() == "token":
            return val.strip()
    return ""


def wait_for_token(home, timeout=LOGIN_WAIT, poll=LOGIN_POLL, sleep=None):
    """Ждёт секрет входа, который кладёт сам `serve` при первом старте (тем же
    кодом, что и `dashboard secret`): до этого момента стучаться в /healthz
    некуда, а токен показать нечего. Пустая строка на истёкший срок ожидания,
    вызывающий тогда печатает адрес без токена."""
    sleep = time.sleep if sleep is None else sleep
    deadline = time.monotonic() + timeout
    got = _read_token(home)
    while not got and time.monotonic() < deadline:
        sleep(poll)
        got = _read_token(home)
    return got


def login_line(home, had_token, waiter=None):
    """Строка входа в хвост update/doctor --fix: адрес печатается всегда, а
    секрет только когда родился на этом же прогоне (had_token снят до наших
    действий), потому что машина та же, и звать новичка за `dashboard secret`
    отдельным шагом незачем (решение DK-822)."""
    addr = "http://localhost:%d/login" % conf_port(home)
    if had_token:
        return "дашборд поднят, адрес входа %s" % addr
    got = (waiter or wait_for_token)(home)
    if not got:
        return "дашборд поднят, адрес входа %s (токен ещё не родился, напечатать: dashboard secret)" % addr
    return "дашборд поднят, адрес входа %s, токен %s" % (addr, got)


# PATH launchd-агента собирается из четырёх частей: системное умолчание
# launchd, из-за которого дефект и случился, брю-каталоги обеих архитектур и
# ~/.local/bin, куда пипсовые и подобные установщики кладут символьные ссылки
# (там стоит claude, третье звено того же корня, DK-247).
SYSTEM_PATH = ("/usr/bin", "/bin", "/usr/sbin", "/sbin")
BREW_PATH = ("/opt/homebrew/bin", "/usr/local/bin")
LOCAL_BIN = "~/.local/bin"


def agent_path(binary):
    """PATH для plist: каталог бинаря devkit, системные пути launchd,
    брю-каталоги и ~/.local/bin, которые есть на машине. Считается на
    doctor --fix по самой машине (наличием каталогов), а не срезом живого
    PATH пользователя: срез менялся бы от сессии к сессии, и доктор
    переписывал бы агента на каждом прогоне. Тильда в ~/.local/bin
    разворачивается по HOME процесса doctor. Без этой строки демон живёт с
    системным PATH launchd и не находит tmux (дефект шага 5 сценария
    DK-217) или claude (DK-247)."""
    parts = [str(Path(binary).parent)]
    parts += list(SYSTEM_PATH)
    parts += [p for p in BREW_PATH if os.path.isdir(p)]
    local_bin = os.path.expanduser(LOCAL_BIN)
    if os.path.isdir(local_bin):
        parts.append(local_bin)
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


def loaded(call=None):
    return launchd.loaded(LABEL, call)


def reload_agent(plist, call=None):
    """Перевзвести агента: снять прежнего и поднять заново, как у сторожка."""
    return launchd.reload_agent(LABEL, plist, call)


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
          call=None, which=None, fetch=None, machine=None, waiter=None):
    """Носитель дашборда в машинном контуре доктора.

    Хоть plist и показывает на бинарь из PATH, а не на чекаут, класть его с
    worktree ветки задачи нельзя по общему правилу: на машину едет только
    проверенное, и доводка отсылает в основной чекаут, как у сторожка.

    Под подставным домом launchd не трогается вовсе (DK-588): метка агента
    одна на машину, и доводка из временного дома уводила живой дашборд
    пользователя. Дом машины берётся из учётной записи, ключ `machine` тут для
    тестов. Конфиг с корнем поиска проектов (`ensure_conf`) это простой файл
    заданного дома, а не служба машины, и заводится независимо от own_home:
    подставному дому это не вредит, а настоящему экономит первый шаг."""
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
    had_token = bool(_read_token(home))
    if fix:
        ensure_conf(home, main)
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
        if not launchd.own_home(home, machine):
            return [], [launchd.foreign_line("дашборд", home, plist, machine)]
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента дашборда %s: %s" % (plist, err)], []
        return [], ["дашборд подключён launchd-агентом %s (порт %d, журнал %s); %s"
                    % (LABEL, conf_port(home), log, login_line(home, had_token, waiter))]
    # Дальше речь про службы машины, и под подставным домом судить о них не о
    # чем: поднят там агент пользователя, а не тот, что описан этим plist.
    if not launchd.own_home(home, machine):
        return [], []
    thief = launchd.hijacked(LABEL, plist, call)
    if thief:
        why = ("launchd-агент дашборда %s взведён чужим plist %s: доска с телефона "
               "показывает чужой чекаут, а журнал уходит туда же" % (LABEL, thief))
        if not fix:
            return ["%s; вернуть своего: devkitctl doctor --fix" % why], []
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента дашборда %s: %s" % (plist, err)], []
        return [], ["дашборд отобран у перехватчика %s и поднят из %s; %s"
                    % (thief, plist, login_line(home, had_token, waiter))]
    if not loaded(call):
        if not fix:
            return ["launchd-агент дашборда %s положен, но не поднят: доска с телефона "
                    "не видна, поднять: devkitctl doctor --fix" % LABEL], []
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента дашборда %s: %s" % (plist, err)], []
        return [], ["дашборд поднят launchd-агентом %s; %s"
                    % (LABEL, login_line(home, had_token, waiter))]
    # Живость меряется по /healthz только после первого старта сервера: секрет
    # в конфиг кладёт только сам serve, и пока его там нет, сервер ещё не
    # поднимался (root в конфиге кладёт уже ensure_conf, файл сам по себе
    # больше не доказательство старта), стучаться некуда и не в какой порт.
    if not _read_token(home):
        return [], []
    bad = probe(home, fetch)
    if bad:
        return [bad], []
    if not fix:
        return [], []
    # На обновлениях, когда чинить уже нечего, доктор всё равно печатает
    # адрес входа одной строкой: новичок, вставивший строку из CONNECT.md
    # заново, должен увидеть его и без свежей установки.
    return [], [login_line(home, had_token, waiter)]
