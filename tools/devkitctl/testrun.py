#!/usr/bin/env python3
"""Частичный прогон тестов под тем же потолком, что и полный (DK-1219).

Потолок числа одновременных прогонов на машине живёт замком
`~/.devkit/parallel-slots` (DK-1162), и берёт его `parallel.py`. Исполнитель,
проверяющий свою правку, гонит не полный прогон, а пакет одного компонента:
`go test` по дереву утилиты либо `python3 -m unittest` по каталогу со своими
тестами. Такой прогон шёл мимо замка, и рядом с занятым слотом машина
получала вторую заявку на все ядра: 2026-09-28 в 19:45 load average держался
на 76-89 при десяти ядрах, пока под замком шло слияние, а третья сессия
гоняла `go test` по дереву соседней задачи.

Обёртка это тот же вход, что у полного прогона, только область уже. Слот
берётся тем же `full_run_slot`, бюджет параллельности и приоритет считаются
теми же функциями `parallel.py`, а итог уезжает в `.devkit/test-runs.log`
записью с полем `scope`: по нему частичный прогон отличим от полного, и
счётчик чужой красноты (`shipctl foreign-fails`) его не считает.

Команда выбирается по каталогу, а не ключом: `go.mod` рядом значит go-модуль,
свой `suite.py` значит собственный раннер каталога, файлы `*_test.py` значит
перебор `unittest discover`. Каталог без всех трёх признаков это отказ с их
перечнем, а не молчаливый ноль.
"""
import argparse
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

import parallel

# Подпись слота в строке ожидания. Потолок один на машину и делится с полным
# прогоном, а слово в строке называет то, что встало в очередь: агент читает
# эту строку у себя в выводе и по ней понимает, чей прогон ждёт.
SLOT_LABEL = "прогонов тестов"

# Журнал прогонов проекта, тот же файл, куда пишет слияние (DK-1125,
# tools/shipctl/testlog.go). Частичный прогон кладёт туда запись со своим
# `scope`, и читатели журнала отличают её от полной по этому полю.
LOG_REL = os.path.join(".devkit", "test-runs.log")

# Потолок стенного времени go-прогона, тот же, что у компонента полного
# прогона (parallel.components): пакет dashboard изолированно укладывается в
# 516 с, а под чужой нагрузкой регулярно выходит за дефолтные десять минут
# самого `go test`.
GO_TIMEOUT = "20m"


def tree_root(path):
    """Корень чекаута над path: ближайший предок с `.git`, иначе сам path."""
    path = Path(path).resolve()
    for candidate in [path] + list(path.parents):
        if (candidate / ".git").exists():
            return candidate
    return path


def kind(path):
    """Вид прогона для каталога: "go", "suite", "unittest" либо None.

    Признак берётся у самого каталога, а не у дерева выше: `go test` гонится
    из модуля, а сюита `unittest discover` из каталога с тестами, и оба
    признака лежат рядом с прогоном.

    Свой раннер каталога старше общего перебора. Каталог с `suite.py` гонит
    его: полный прогон зовёт там ровно его же (`parallel.components`), а
    `unittest discover` по тому же каталогу свёл бы сто пятьдесят семь
    классов к одному процессу и растянул бы прогон на десятки минут.
    """
    path = Path(path)
    if (path / "go.mod").exists():
        return "go"
    if (path / "suite.py").exists():
        return "suite"
    if any(path.glob("*_test.py")):
        return "unittest"
    return None


def component_name(root, path, how):
    """Имя компонента в строке итога и в журнале.

    Го-модули названы так же, как их зовёт полный прогон (`go:taskctl`), чтобы
    одно и то же имя в журнале значило одно и то же. Питоновая сюита названа
    путём от корня: своего имени у неё в полном прогоне нет ни у одной, кроме
    перечисленных бакетов, а путь честен для любого каталога.
    """
    rel = os.path.relpath(str(path), str(root))
    if how == "go":
        return "go:" + Path(path).name
    if how == "suite":
        return Path(path).name
    return rel


def named(extra):
    """Хвост называет тесты по имени, а не ключами.

    Первое слово без дефиса это модуль, класс или метод (`board_test`,
    `board_test.MoveTest.test_x`). Такой хвост меняет саму команду: перебором
    каталога отдельный класс не выбрать, его гонит `unittest` по имени.
    """
    return bool(extra) and not str(extra[0]).startswith("-")


def run_filter(extra):
    """Ключ `-run` из хвоста go test и его имя отбора, либо (None, extra).

    Питоновой сюите `-run` незнаком: её `suite.py` и `unittest discover` имени
    теста не принимают. Хвост сценария проверки пишется по образцу go
    (`-run DutyAgent`), и здесь он переводится в отбор `unittest -k`, которому
    имя класса или метода подходит как есть. Го-модулю ключ уезжает как есть.
    """
    extra = list(extra)
    if len(extra) >= 2 and str(extra[0]) == "-run":
        return str(extra[1]), extra[2:]
    if extra and str(extra[0]).startswith("-run="):
        return str(extra[0])[5:], extra[1:]
    return None, extra


def build(path, how, share, extra):
    """Команда прогона: (argv, env) с долей бюджета и пониженным приоритетом.

    Доп. аргументы агента (`-run TestX`, `-v`) уезжают в хвост: у `go test`
    флаги после списка пакетов разбираются наравне с флагами до него, а у
    `unittest discover` хвост это его собственные ключи. Названный по имени
    тест питоновой сюиты гонится `unittest` напрямую, без перебора каталога и
    без своего раннера: ни перебор, ни `suite.py` имени не принимают.
    """
    extra = list(extra)
    filt, extra = run_filter(extra)
    if how == "go":
        # Свой `-timeout` агента старше умолчания: пакет, которому двадцати
        # минут мало, иначе нечем было бы прогнать вовсе (замечание ревью
        # круга 1). Остальные ключи расчёта обёртка держит сама. `-run` тут
        # родной, он уже вернулся в хвост через run_filter.
        argv = ["go", "test", "-count=1"]
        if not any(str(t).split("=", 1)[0] == "-timeout" for t in extra):
            argv.append("-timeout=" + GO_TIMEOUT)
        argv.append("./...")
        if filt is not None:
            extra = ["-run", filt] + extra
        argv = parallel.with_share("go:x", argv, share) + extra
    elif filt is not None:
        # `unittest -k` сравнивает с именем целиком (fnmatch), а go `-run`
        # берёт подстроку: звёзды с обеих сторон держат ту же ширину отбора.
        # Отбор идёт через discover: одних ключей `-k` без имени теста
        # загрузчику недостаточно, и ноль прогонов выглядит как зелень.
        argv = [sys.executable, "-m", "unittest", "discover", "-p", "*_test.py",
                "-k", "*" + filt + "*"] + extra
    elif named(extra):
        argv = [sys.executable, "-m", "unittest"] + extra
    elif how == "suite":
        argv = parallel.with_share(parallel.RUNNER, [sys.executable, "suite.py"],
                                   share) + extra
    else:
        argv = [sys.executable, "-m", "unittest", "discover", "-p", "*_test.py"]
        argv += extra
    return parallel.with_priority(argv), parallel.command_env(argv)


def record(root, scope, name, ok, secs):
    """Запись частичного прогона в журнал проекта, либо None без `.devkit`.

    Поля те же, что у записи слияния (`tools/shipctl/testlog.go`), плюс
    `scope`: он называет область прогона и отличает частичную запись от
    полной. `diff` тут пуст, а принадлежность компонента диффу не считается
    вовсе: частичный прогон гоняет автор своей правки, и вопрос «чья
    краснота» у него не стоит.

    Провал записи прогон не роняет тем же порядком, каким его не роняет
    слияние: журнал это наблюдение, а не предусловие.
    """
    directory = os.path.join(str(root), ".devkit")
    if not os.path.isdir(directory):
        return None
    rec = {
        "time": datetime.now(timezone.utc).astimezone().isoformat(),
        "id": task_id(root),
        "ok": ok,
        "scope": scope,
        "diff": [],
        "components": [{"name": name, "ok": ok, "secs": round(secs, 1),
                        "root": scope, "resolved": False, "own": False}],
    }
    try:
        with open(os.path.join(str(root), LOG_REL), "a", encoding="utf-8") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
    except OSError:
        return rec
    return rec


def task_id(root):
    """ID задачи из имени ветки чекаута, пустая строка вне ветки задачи.

    Ветку задачи заводит `shipctl start` именем вида `dk-1219`, и запись
    журнала опознаётся по тому же ID, каким её опознаёт слияние.
    """
    try:
        out = subprocess.run(["git", "-C", str(root), "rev-parse",
                              "--abbrev-ref", "HEAD"],
                             capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return ""
    name = out.stdout.strip()
    if out.returncode != 0 or not name:
        return ""
    head, sep, tail = name.partition("-")
    if not sep or not tail.split("-")[0].isdigit():
        return ""
    return head.upper() + "-" + tail


def run(target, extra=(), out=None):
    """Гонит тесты каталога под слотом потолка и пишет итог в журнал."""
    out = out if out is not None else sys.stdout
    path = Path(target).resolve()
    if path.is_file():
        path = path.parent
    if not path.is_dir():
        print("нет каталога %s" % path, file=out)
        return 2
    how = kind(path)
    if how is None:
        print("в %s нет ни go.mod, ни suite.py, ни файлов *_test.py: "
              "прогонять нечего, назови каталог go-модуля либо каталог с "
              "тестами" % path, file=out)
        return 2
    root = tree_root(path)
    budget = parallel.cpu_budget()
    # Прогон один, лайн у него один, и доля ему достаётся вся: тот же расчёт,
    # каким полный прогон наделяет единственный оставшийся компонент. Второй
    # такой заявки на машине не бывает, её держит слот.
    share = parallel.component_share(1, budget)
    argv, env = build(path, how, share, extra)
    scope = os.path.relpath(str(path), str(root))
    name = component_name(root, path, how)
    with parallel.full_run_slot(label=SLOT_LABEL, out=out):
        print("бюджет параллельности: ядра=%d доля прогона=%d приоритет=nice %d"
              % (budget, share, parallel.NICE_LEVEL), file=out, flush=True)
        print("прогон %s (%s): %s" % (name, scope, " ".join(argv)),
              file=out, flush=True)
        started = time.monotonic()
        # Свой stdout прогон отдаёт живьём: агент смотрит вывод тестов по ходу,
        # а не после. Отдельный поток вывода (стенд, вызов из другого кода)
        # получает то же самое собранным куском: подпроцессу файловый
        # дескриптор такого потока не передать.
        if out is sys.stdout:
            proc = subprocess.run(argv, cwd=str(path), env=env)
            child = ""
        else:
            proc = subprocess.run(argv, cwd=str(path), env=env,
                                  stdout=subprocess.PIPE,
                                  stderr=subprocess.STDOUT)
            child = proc.stdout.decode("utf-8", "replace")
        secs = time.monotonic() - started
    if child:
        print(child, file=out, end="" if child.endswith("\n") else "\n")
    ok = proc.returncode == 0
    # Строка итога в том же виде, каким её печатает полный прогон
    # (parallel.py): её разбирает componentLineRe в tools/shipctl/testlog.go, и
    # второй формы у строки итога быть не должно.
    print("%-16s (%s) %6.1fs %s" % (name, scope, secs, "ok" if ok else "FAIL"),
          file=out, flush=True)
    record(root, scope, name, ok, secs)
    return 0 if ok else 1


def main(argv=None):
    ap = argparse.ArgumentParser(
        prog="devkitctl test", description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("path", help="каталог go-модуля либо каталог с тестами *_test.py")
    ap.add_argument("rest", nargs=argparse.REMAINDER,
                    help="хвост ключей самому прогону (-run TestX, -v)")
    args = ap.parse_args(argv)
    return run(args.path, args.rest)


if __name__ == "__main__":
    sys.exit(main())
