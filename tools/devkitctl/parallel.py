#!/usr/bin/env python3
"""Параллельный раннер компонентов локальной самопроверки devkit.

Последовательная цепочка в ключе `test` держала сумму компонентов: go-инструменты
по одному, затем hooks, сюита devkitctl, скиллы и doctor, и по замеру DK-166
сумма вчетверо длиннее самого тяжёлого звена. Компоненты независимы и друг
друга не читают, поэтому раннер гонит их одновременно с потолком воркеров
(задача DK-347 цели DK-166).

Воркер это поток с процессом компонента целиком: у каждого своя команда со
своим cwd (go test, питоновые сюиты, проверочные скрипты). Первый провал валит
прогон: занятые компоненты получают SIGTERM по группе процессов и в итог
не попадают, а вывод провалившегося печатается целиком. Очередь общая,
а не дорожка на компонент: два go-инструмента рядом прогоняют общую сборку
пакетов, а освободившийся воркер забирает следующий компонент, не простаивая.

Потолок по умолчанию ограничен восемью воркерами при пятнадцати компонентах:
за сюитой devkitctl стоят её собственные воркеры с подпроцессами, и общее
число процессов обязано оставаться в плато, за которым сюита упирается в
файловую систему, а не в CPU. Слишком малый потолок складывает go-инструменты
обратно в очередь.

Вызов без аргументов гонит все компоненты по раскладке корня devkit,
`--list` печатает их перечень. Запускается из корня чекаута, как ключ `test`
из `.devkit/deploy.local`.
"""
import argparse
import contextlib
import os
import queue
import signal
import subprocess
import sys
import threading
import time
import traceback
from pathlib import Path

# Корень чекаута: раннер лежит в tools/devkitctl на две ступени глубже.
ROOT = Path(__file__).resolve().parents[2]


def go_tools(root=ROOT):
    """Имена go-модулей под tools/, отсортированные по имени.

    Список ищется по факту наличия go.mod, а не хранится руками: хранёный
    перечень уже расходился с деревом (находка DK-367, модуль cmdout выпал из
    прежнего списка) и молчал об этом, пока кто-то не заметил глазами.
    """
    return sorted(p.parent.name for p in (root / "tools").glob("*/go.mod"))


def skill_suites(root=ROOT):
    """Имена скиллов со своими тестами рядом с SKILL.md, по алфавиту.

    Ищутся по факту наличия `*_test.py`, а не хранятся руками: с go-модулями
    хранёный перечень уже разошёлся с деревом молча (находка DK-367), а у
    скилла цена расхождения та же. Тесты нового скилла просто не гонялись бы, и
    заметить это было бы некому."""
    return sorted({p.parent.name for p in (root / "kit" / "skills").glob("*/*_test.py")})


def components(root=ROOT):
    """Перечень компонентов: (имя, cwd от корня, argv) в порядке запуска.

    Go-компоненты идут с GOWORK=off: чужой go.work выше по дереву (находка
    DK-115) уводил бы go test из модуля утилиты. -count=1 держит DoD цели
    DK-166: кэш тестового прогона go обязан молчать.
    """
    comps = [("go:" + tool, "tools/" + tool,
              ["go", "test", "-count=1", "./..."]) for tool in go_tools(root)]
    comps += [
        ("hooks", "hooks",
         [sys.executable, "-m", "unittest", "discover", "-p", "*_test.py"]),
        ("devkitctl", "tools/devkitctl", [sys.executable, "suite.py"]),
        ("skills", "kit/skills",
         [sys.executable, "-m", "unittest", "discover", "-p", "*_test.py"]),
        ("check-skills", "kit/skills", [sys.executable, "check-skills.py"]),
        ("check-exec-bit", "hooks",
         [sys.executable, "check-exec-bit.py"]),
    ]
    comps += [(name, "kit/skills/" + name,
               [sys.executable, "-m", "unittest", "discover", "-p", "*_test.py"])
              for name in skill_suites(root)]
    comps.append(("doctor", ".", [sys.executable, "tools/devkitctl/devkitctl.py",
                                  "doctor", "--layout"]))
    return comps


def command_env(argv):
    """Окружение подпроцесса компонента, None оставляет окружение раннера.

    Go-компоненты идут с GOWORK=off: чужой go.work выше по дереву (находка
    DK-115) уводит go test из модуля утилиты, поэтому глушить workspace
    обязан сам раннер, а не только обёртка снаружи.
    """
    if argv[0] != "go":
        return None
    return dict(os.environ, GOWORK="off")


def run_all(comps, workers, root=ROOT):
    """Гонит компоненты параллельно с потолком воркеров.

    Отдаёт итоги (имя, код, секунды, вывод) в порядке завершения и имя первого
    провалившегося компонента либо None. Первый же неуспешный код валит прогон:
    занятые компоненты получают SIGTERM по группе процессов и в итог не
    попадают, а короткий хвост за тяжёлой сюитой успевает догнаться.
    """
    pending = queue.Queue()
    for comp in comps:
        pending.put(comp)
    outcomes, stop = [], threading.Event()
    first_fail, lock = [None], threading.Lock()

    def worker():
        while not stop.is_set():
            try:
                name, rel, argv = pending.get_nowait()
            except queue.Empty:
                return
            started = time.monotonic()
            # Старт и провал одного правила: компонент, который не смог
            # запуститься (несуществующий cwd или бинарник), обязан красить
            # прогон, а не молча выпадать из итога зелёным числом меньше.
            # Поэтому исключение старта ловится здесь и гонится через тот же
            # путь, что и неуспешный код возврата, с трейсбеком в выводе.
            try:
                # Группа процессов нужна ради стопа: у компонента свои
                # подпроцессы (раннер сюиты, подпроцессы доктора), и
                # terminate самого питона оставил бы их сиротами докручивать
                # уже решённый прогон.
                proc = subprocess.Popen(
                    argv, cwd=str(root / rel), env=command_env(argv),
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                    start_new_session=True)
            except Exception as exc:
                out = "старт компонента не удался: %r\n%s" % (
                    exc, traceback.format_exc())
                with lock:
                    if first_fail[0] is None:
                        first_fail[0] = name
                        stop.set()
                    outcomes.append((name, None,
                                     time.monotonic() - started, out))
                continue
            kills = 0
            while True:
                try:
                    out, _ = proc.communicate(timeout=0.2)
                    break
                except subprocess.TimeoutExpired:
                    if stop.is_set():
                        # TERM уходит на каждом круге, а через пару секунд
                        # сменяется KILL: упрямый подпроцесс, игнорирующий
                        # TERM, иначе повесил бы стоп на себе.
                        kills += 1
                        sig = (signal.SIGTERM if kills <= 10
                               else signal.SIGKILL)
                        with contextlib.suppress(ProcessLookupError):
                            os.killpg(proc.pid, sig)
            with lock:
                if proc.returncode == 0:
                    outcomes.append((name, 0, time.monotonic() - started,
                                     out.decode("utf-8", "replace")))
                elif first_fail[0] is None:
                    first_fail[0] = name
                    stop.set()
                    outcomes.append((name, proc.returncode,
                                     time.monotonic() - started,
                                     out.decode("utf-8", "replace")))

    threads = [threading.Thread(target=worker) for _ in range(max(1, workers))]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    return outcomes, first_fail[0]


def main(argv=None):
    ap = argparse.ArgumentParser(
        prog="parallel", description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-j", dest="jobs", type=int, default=8,
                    help="потолок воркеров, по компоненту на поток (по умолчанию 8)")
    ap.add_argument("--list", action="store_true",
                    help="только перечень компонентов")
    args = ap.parse_args(argv)
    comps = components()
    if args.list:
        for name, rel, argv in comps:
            print("%-16s (%s) %s" % (name, rel, " ".join(argv)))
        print("компонентов: %d" % len(comps))
        return 0
    started = time.monotonic()
    outcomes, first_fail = run_all(comps, args.jobs)
    secs = time.monotonic() - started
    for name, rc, took, out in sorted(outcomes, key=lambda o: -o[2]):
        print("%-16s %6.1fs %s" % (name, took, "ok" if rc == 0 else "FAIL"))
    for name, rc, _, out in outcomes:
        if rc != 0:
            print("FAIL %s\n%s" % (name, out))
    mins, whole = divmod(int(secs + 0.5), 60)
    print("Ran %d of %d components in %dm%02ds" % (len(outcomes), len(comps),
                                                    mins, whole))
    if first_fail is not None:
        print("FAILED (first=%s), стоп по первому провалу" % first_fail)
        return 1
    print("OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
