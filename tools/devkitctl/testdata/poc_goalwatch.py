#!/usr/bin/env python3
"""Стенд сторожка цикла цели на живом запуске (DK-740).

  python3 tools/devkitctl/testdata/poc_goalwatch.py

Юниты зовут `watch.look` напрямую, а тут через настоящий запуск
`devkitctl.py watch` на синтетическом доме: реестр целей, доска, журнал
запусков и журнал цикла кладутся во временный каталог, время старится
подстановкой строк, а зов уведомителя ловится по `~/.devkit/notify.log` того же
дома. Настоящий дом и настоящая доска не трогаются.

Стенд гоняет четыре тика подряд. Цикл встал (зов), тот же стоп следом (молчание
до порога), стоп дожил до второго порога (повторный зов с номером), в журнал лёг
боевой выкат вне окна тика (движение, тишина). Печатает одну строку итога и
выходит 0, любое расхождение это ненулевой выход с разбором.
"""
import os
import re
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timedelta
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEVKITCTL = HERE.parent / "devkitctl.py"
GOAL = "DK-900"
STAMP = "%Y-%m-%dT%H:%M:%S"
IDLE = 45

BOARD = """# Задачи стенда

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| %s | Цель: стенд сторожка | task | P1 | 60 (50+5+3+0+2) | XL | [tasks/%s.md](tasks/%s.md) |
""" % (GOAL, GOAL, GOAL)


def ago(minutes):
    return (datetime.now() - timedelta(minutes=minutes)).strftime(STAMP)


def stand(root):
    home, proj = root / "home", root / "proj"
    (home / ".devkit" / "goals").mkdir(parents=True)
    (proj / ".devkit").mkdir(parents=True)
    (proj / "docs" / "tasks").mkdir(parents=True)
    (proj / "docs" / "TASKS.md").write_text(BOARD, encoding="utf-8")
    (proj / "docs" / "tasks" / ("%s.md" % GOAL)).write_text("# %s\n" % GOAL, encoding="utf-8")
    # Журнал цикла стоит с ночи, а журнал запусков полон свежих строк
    # расписания: ровно та картина, при которой сторожок молчал четыре с
    # половиной часа.
    (proj / ".devkit" / ("goal-%s.log" % GOAL)).write_text(
        "%s виток стенда\n" % ago(270), encoding="utf-8")
    lines = ["%s\ttaskctl\tmove\t0" % ago(270)]
    ticks = []
    for i in range(265, 0, -5):
        lines += ["%s\tshipctl\tship\t1" % ago(i), "%s\tagentctl\tquota\t0" % ago(i)]
        ticks.append("%s\t%s" % (ago(i), ago(i - 0.05)))
    (proj / ".devkit" / "log").write_text("\n".join(lines) + "\n", encoding="utf-8")
    # Окна тиков рядом с этими строками: по ним сторожок и узнаёт свой
    # служебный вызов. Боевой `shipctl ship <ID>` в журнале выглядит так же, и
    # без окна стенд не отличил бы одно от другого.
    (home / ".devkit" / "watch.ticks").write_text("\n".join(ticks) + "\n", encoding="utf-8")
    entry = home / ".devkit" / "goals" / ("%s-стенд.watch" % GOAL)
    entry.write_text("goal = %s\nroot = %s\nfile = %s\nseen = %s\n" % (
        GOAL, proj, proj / "docs" / "tasks" / ("%s.md" % GOAL), ago(270)), encoding="utf-8")
    return home, proj, entry


def tick(home):
    p = subprocess.run([sys.executable, str(DEVKITCTL), "watch", "--idle", str(IDLE)],
                       env=dict(os.environ, HOME=str(home)),
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    return p.returncode, p.stdout or ""


def die(why, out=""):
    print("poc_goalwatch: %s\n%s" % (why, out), file=sys.stderr)
    sys.exit(1)


def main():
    root = Path(tempfile.mkdtemp(prefix="poc-goalwatch-"))
    try:
        home, proj, entry = stand(root)
        code, out = tick(home)
        if code != 1 or "зову" not in out:
            die("первый тик не позвал по вставшему циклу (код %d)" % code, out)
        code, out = tick(home)
        if code != 0 or "повтор через" not in out:
            die("второй тик позвал раньше порога (код %d)" % code, out)
        text = entry.read_text(encoding="utf-8")
        entry.write_text(re.sub(r"stopped = .*", "stopped = %s" % ago(60), text),
                         encoding="utf-8")
        code, out = tick(home)
        if code != 1 or "зову 2-й раз" not in out:
            die("стоп дожил до второго порога, а повторного зова нет (код %d)" % code, out)
        # Боевой вызов той же утилиты в окно тика не попадает, и цикл живой.
        with open(proj / ".devkit" / "log", "a", encoding="utf-8") as f:
            f.write("%s\tshipctl\tship\t0\n" % ago(2))
        code, out = tick(home)
        if code != 0 or "тихо" not in out:
            die("боевой выкат не сошёл за движение цикла (код %d)" % code, out)
        log = home / ".devkit" / "notify.log"
        said = [ln for ln in log.read_text(encoding="utf-8").splitlines()
                if GOAL in ln] if log.is_file() else []
        if len(said) < 2:
            die("уведомитель позвал %d раз вместо двух" % len(said), "\n".join(said))
        print("poc_goalwatch: ok, тик 1 позвал, тик 2 смолчал до порога, "
              "тик 3 позвал 2-й раз, тик 4 принял боевой выкат за движение; "
              "уведомитель отработал %d раза" % len(said))
    finally:
        shutil.rmtree(str(root), ignore_errors=True)


if __name__ == "__main__":
    main()
