#!/usr/bin/env python3
"""Общая механика launchd-агентов devkit.

Носителей два, дашборд и сторожок цикла цели. Оба кладут plist в
`$HOME/Library/LaunchAgents` и поднимают его в домене `gui/<uid>`. Домен про
дом ничего не знает. Метка `ru.devkit.dashboard` одна на машину. `bootstrap` из
подставного дома снимает живого агента пользователя и ставит на его место
экземпляр с чужим чекаутом. Журнал такого экземпляра уходит во временный
каталог. Так DK-588 уводил обоих на каждом сценарии проверки с временным HOME.

Дом машины тут спрашивается у `getpwuid`. Из подставного дома launchctl не
зовётся. Доводка там кладёт plist и печатает строку.

Взведённого агента сверяет `hijacked`. Путь plist печатает `launchctl print`.
Перехватчик, переживший прошлые прогоны, находится этой сверкой.
"""
import os
import pwd
import subprocess
from pathlib import Path


def machine_home():
    """Дом пользователя машины по учётной записи.

    Раскладку launchd берёт оттуда же. Подставной дом теста или сценария на
    службы машины не влияет."""
    try:
        return Path(pwd.getpwuid(os.getuid()).pw_dir)
    except KeyError:
        return Path(os.path.expanduser("~"))


def own_home(home, machine=None):
    """Тот ли это дом, чьими службами распоряжается процесс."""
    machine = machine_home() if machine is None else Path(machine)
    try:
        return Path(home).resolve() == machine.resolve()
    except OSError:
        return str(home) == str(machine)


def launchctl(args, call=None):
    """(код возврата, вывод) launchctl. Код 127 стоит там, где позвать не
    вышло."""
    call = subprocess.run if call is None else call
    try:
        p = call(["launchctl"] + list(args), stdout=subprocess.PIPE,
                 stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return 127, str(e)
    return p.returncode, (p.stdout or "").strip()


def loaded(label, call=None):
    return launchctl(["list", label], call)[0] == 0


def reload_agent(label, plist, call=None):
    """Перевзвести агента. Прежний снимается, новый поднимается в домене
    gui/<uid>. Это сессия пользователя, в ней агент и живёт."""
    target = "gui/%d" % os.getuid()
    launchctl(["bootout", "%s/%s" % (target, label)], call)
    code, out = launchctl(["bootstrap", target, str(plist)], call)
    if code == 0:
        return ""
    # Старый launchctl bootstrap не знает, а load умеет то же самое.
    code, out = launchctl(["load", "-w", str(plist)], call)
    return "" if code == 0 else out


def agent_source(label, call=None):
    """Путь plist, которым агент метки взведён сейчас.

    Читается строка `path =` из `launchctl print`. Пустая строка приходит на
    отсутствие агента, отказ launchctl и незнакомый вывод."""
    code, out = launchctl(["print", "gui/%d/%s" % (os.getuid(), label)], call)
    if code != 0:
        return ""
    for ln in out.splitlines():
        key, sep, val = ln.strip().partition("=")
        if sep and key.strip() == "path":
            return val.strip()
    return ""


def hijacked(label, plist, call=None):
    """Путь перехватчика, если агент метки поднят не из ожидаемого plist.

    Такой агент выглядит здоровым отовсюду. Файл в доме совпадает с эталоном,
    `launchctl list` метку находит, работает при этом чужой чекаут. Видно
    перехват по пути, из которого агент взведён."""
    got = agent_source(label, call)
    if not got:
        return ""
    try:
        same = Path(got).resolve() == Path(plist).resolve()
    except OSError:
        same = got == str(plist)
    return "" if same else got


def foreign_line(what, home, plist, machine=None):
    """Строка доводки для подставного дома. Называет положенный plist и оба
    дома."""
    machine = machine_home() if machine is None else machine
    return ("%s разложен в подставном доме %s: plist положен в %s, а launchd "
            "машины не тронут, метка агента одна на машину и увела бы живого "
            "(дом машины %s)" % (what, home, plist, machine))
