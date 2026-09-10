#!/usr/bin/env python3
"""Стенд подъёма сессии после ответа человека (DK-922).

  python3 tools/devkitctl/testdata/poc_wakeraise.py

Юниты зовут `watch.wake` и `taskWake` по отдельности, а тут обе половины
сходятся на живом запуске. Временный репозиторий с синтетической доской, строка
в Blocked с машинной причиной «вопрос: ...», ответ человека во входе разговора
задачи. Дальше идёт настоящий `devkitctl watch` и настоящий бинарь дашборда,
собранный из этого же чекаута. Настоящий дом, настоящая доска и настоящий tmux
не трогаются: дом временный, а tmux, клиент, вердикт яруса и перечень прав
подменены фикстурами, которые пишут свои вызовы в журнал.

Стенд проверяет всю цепочку разом. Тик находит лежащий ответ, возвращает строку
в In progress, снимает признак ожидания и зовёт `dashboard wake`. Дашборд
поднимает окно `task-<ID>` с заказом продолжения и без признака hidden, то есть
разговор остаётся в списке панели, и продолжение работы человек видит в ленте
без нажатия «Запуска». Печатает одну строку итога и выходит 0, любое
расхождение это ненулевой выход с разбором.
"""
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEVKITCTL = HERE.parent / "devkitctl.py"
ROOT = HERE.parents[2]
GOAL = "DK-900"
TASK = "DK-901"

BOARD = """# Задачи стенда

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| %s | Цель: стенд подъёма | task | P1 | 60 (50+5+3+0+2) | XL | [tasks/%s.md](tasks/%s.md) |

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| %s | Спрашивает [блок: вопрос: режем строку или поднимаем цену] | task | P1 | 55 (50+3+2+0+0) | M | [tasks/%s.md](tasks/%s.md) |
""" % (GOAL, GOAL, GOAL, TASK, TASK, TASK)

# Подставной taskctl: доску он и печатает, и правит. Печатает разбором той же
# разметки, что читает сторожок, поэтому обе половины стенда видят одно
# состояние, а не две выдумки. Правит только тем ходом, каким будят строку.
TASKCTL = r'''#!/usr/bin/env python3
import json, os, re, sys

argv = sys.argv[1:]
root = argv[argv.index("-C") + 1] if "-C" in argv else os.getcwd()
board = os.path.join(root, "docs", "TASKS.md")
log = os.environ["STAND_LOG"]
open(log, "a").write("taskctl " + " ".join(argv) + "\n")


def read():
    return open(board, encoding="utf-8").read()


def rows():
    out, key = [], ""
    for line in read().split("\n"):
        if line.startswith("## "):
            title = line[3:].strip()
            key = title.split(" ")[0].lower().replace("in", "in-progress") \
                if title.startswith("In") else title.split(" ")[0].lower()
            out.append({"key": key, "title": title, "rows": []})
            continue
        if not line.startswith("| ") or line.startswith("| ID ") or set(line) <= set("|- "):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        title, block = cells[1], ""
        got = re.search(r"\[блок: (.+)\]$", title)
        if got:
            block, title = got.group(1), title[:got.start()].strip()
        out[-1]["rows"].append({"id": cells[0], "title": title, "block": block,
                                "type": "task", "p": cells[3], "r": 55,
                                "r_parts": [50, 3, 2, 0, 0], "cost": cells[5],
                                "link": "-", "accept": ""})
    return out


if "list" in argv and "--json" in argv:
    print(json.dumps({"prefix": "DK", "sections": rows()}, ensure_ascii=False))
    sys.exit(0)

if "move" in argv:
    tid = argv[argv.index("move") + 1]
    text, moved = read().split("\n"), None
    keep = []
    for line in text:
        if line.startswith("| " + tid + " "):
            moved = re.sub(r" \[блок: [^\]]+\]", "", line)
            continue
        keep.append(line)
    if moved is None:
        print("строки %s на доске нет" % tid)
        sys.exit(1)
    at = keep.index("## Check (готово, ждёт проверки пользователем)") - 1
    keep.insert(at, moved)
    open(board, "w", encoding="utf-8").write("\n".join(keep))
    print("%s в in-progress" % tid)
    sys.exit(0)

print("подставной taskctl не знает команды: " + " ".join(argv))
sys.exit(1)
'''

TMUX = r'''#!/bin/sh
echo "tmux $@" >> "$STAND_LOG"
case "$1" in
  ls) exit 1;;
  capture-pane) printf '';;
esac
exit 0
'''

AGENTCTL = r'''#!/bin/sh
case "$1" in
  harness) cat <<'JSON'
{"harnesses":[{"name":"claude-code","bin":"claude","default":true,"enabled":true,
 "tiers":{"base":"модель-base","pro":"модель-pro","max":"модель-max"}}]}
JSON
  ;;
  pick) printf 'model: модель-pro\neffort: high\ntier: pro\n';;
  *) exit 0;;
esac
'''


def die(why, out=""):
    print("poc_wakeraise: %s\n%s" % (why, out), file=sys.stderr)
    sys.exit(1)


def script(path, body):
    path.write_text(body, encoding="utf-8")
    path.chmod(0o755)


def stand(root):
    home, projects = root / "home", root / "projects"
    proj, devkit = projects / "demo", projects / "devkit"
    (home / ".devkit" / "goals").mkdir(parents=True)
    (proj / "docs" / "tasks").mkdir(parents=True)
    (proj / ".devkit" / "chat").mkdir(parents=True)
    (proj / "docs" / "TASKS.md").write_text(BOARD, encoding="utf-8")
    for tid in (GOAL, TASK):
        (proj / "docs" / "tasks" / ("%s.md" % tid)).write_text("# %s\n" % tid, encoding="utf-8")
    subprocess.run(["git", "-C", str(proj), "init", "-q"], check=True)
    # Перечень прав и оболочка конвейера ищутся в корнях конфига: фикстуры
    # отвечают «права на месте» и «оболочка есть», а живые тут отвечали бы по
    # машине разработчика.
    (devkit / "tools" / "devkitctl").mkdir(parents=True)
    (devkit / "kit" / "skills" / "board-task").mkdir(parents=True)
    script(devkit / "tools" / "devkitctl" / "perms.py", "#!/usr/bin/env python3\n")
    (devkit / "kit" / "skills" / "board-task" / "task-run.py").write_text(
        "import sys\n", encoding="utf-8")

    # Ответ человека лежит во входе разговора задачи безадресной строкой: такой
    # его кладёт панель, и только такую сторожок считает ответом задаче.
    (proj / ".devkit" / "chat" / ("task-%s.in" % TASK)).write_text(
        "2026-09-10 19:57, из дашборда: режем, две половины по шву\n", encoding="utf-8")
    # Признак ожидания без срока: живого читателя у него нет, и пробуждение
    # обязано снять его само.
    (proj / ".devkit" / "chat" / ("task-%s.ask" % TASK)).write_text(
        "-\nзадача %s\n" % TASK, encoding="utf-8")

    conf = home / ".devkit" / "dashboard.local"
    conf.write_text("root = %s\ntoken = стенд\n" % projects, encoding="utf-8")
    conf.chmod(0o600)
    entry = home / ".devkit" / "goals" / ("%s-стенд.watch" % GOAL)
    entry.write_text("goal = %s\nroot = %s\nfile = %s\n" % (
        GOAL, proj, proj / "docs" / "tasks" / ("%s.md" % GOAL)), encoding="utf-8")
    return home, proj


def bins(root):
    """Каталог подставных утилит и настоящего дашборда, собранного отсюда же."""
    bin = root / "bin"
    bin.mkdir()
    script(bin / "taskctl", TASKCTL)
    script(bin / "tmux", TMUX)
    script(bin / "agentctl", AGENTCTL)
    script(bin / "claude", "#!/bin/sh\nexit 0\n")
    build = subprocess.run(["go", "build", "-o", str(bin / "dashboard"), "."],
                           cwd=str(ROOT / "tools" / "dashboard"),
                           env=dict(os.environ, GOWORK="off"),
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    if build.returncode != 0:
        die("дашборд не собрался", build.stdout or "")
    return bin


def main():
    root = Path(tempfile.mkdtemp(prefix="poc-wakeraise-"))
    try:
        home, proj = stand(root)
        bin = bins(root)
        log = root / "stand.log"
        env = dict(os.environ, HOME=str(home), STAND_LOG=str(log),
                   PATH=str(bin) + os.pathsep + os.environ["PATH"])
        env.pop("DEVKIT_HOME", None)
        p = subprocess.run([sys.executable, str(DEVKITCTL), "watch"], env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        out = p.stdout or ""
        if "разбужена" not in out:
            die("тик не разбудил припаркованную строку (код %d)" % p.returncode, out)
        if "сессия задачи поднята в tmux-сессии task-%s" % TASK not in out:
            die("тик не поднял сессию разбуженной задачи (код %d)" % p.returncode, out)
        board = (proj / "docs" / "TASKS.md").read_text(encoding="utf-8")
        head, tail = board.split("## Blocked", 1)
        if TASK not in head or TASK in tail:
            die("строка не вернулась из Blocked в работу", board)
        if (proj / ".devkit" / "chat" / ("task-%s.ask" % TASK)).exists():
            die("признак ожидания пережил пробуждение", out)
        said = log.read_text(encoding="utf-8")
        if "tmux new-session -d -s task-%s" % TASK not in said:
            die("окно задачи не поднято", said)
        if "--order 'Продолжай выполнение %s'" % TASK not in said:
            die("поднятое окно получило не тот заказ", said)
        if "DEVKIT_HIDDEN=1" in said:
            die("окно поднято скрытым от списка панели: продолжения в ленте не видно", said)
        print("poc_wakeraise: ok, тик разбудил %s ответом, строка вернулась в работу, "
              "признак ожидания снят, дашборд поднял окно task-%s с заказом продолжения "
              "и без признака hidden" % (TASK, TASK))
    finally:
        shutil.rmtree(str(root), ignore_errors=True)


if __name__ == "__main__":
    main()
