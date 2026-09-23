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

Потолков параллельности исторически было три независимых: воркеры этого
раннера, воркеры сюиты devkitctl (`suite.py`) и параллельность самого
`go test` (по умолчанию равна числу ядер и внутри пакета, и между пакетами).
Три потолка перемножались: на десяти ядрах прогон `shipctl merge` разгонял
load average до 78 (находка DK-1123), потому что каждый из восьми
одновременных компонентов верхнего уровня внутри себя ещё брал все ядра под
себя. У прогона один бюджет параллельности, равный числу ядер машины
(`cpu_budget`), и он не суммируется, а делится: воркеры верхнего уровня
получают число одновременно работающих компонентов, а `component_share`
делит бюджет на них же, отдавая долю каждому компоненту внутрь (`suite.py`
аргументом `-j`, `go test` флагом `-p` и переменной `GOMAXPROCS`). Целочисленное
деление с полом в единицу держит инвариант `jobs * share <= budget` при любом
числе одновременно активных компонентов, тяжёлых или лёгких.

Доля не статичная на весь прогон, а живая: `run_all` считает её по числу
воркеров, ещё не исчерпавших очередь (`alive`), а не по потолку воркеров
целиком (замечание ревью DK-1123). Пока компонентов хватает на всех
воркеров, доля не отличается от старой статичной, а к хвосту прогона, когда
воркеры один за другим находят очередь пустой, доля новых, позже
стартующих компонентов растёт. У способа есть предел: компонент первого
залпа (когда очередь ещё не короче числа воркеров) получает статичную долю
навсегда, `-p` и `GOMAXPROCS` домешать после старта процесса нельзя - для
`go:dashboard`, известного длинного полюса (по замеру DK-1122 494-556 с в
одиночку), это и есть его случай. Живой замер полного прогона проверяет,
что при этом стена не раздувается выше потолка DK-1122.

Вызов без аргументов гонит все компоненты по раскладке корня devkit,
`--list` печатает их перечень, `--only-go` сужает прогон до го-модулей
`tools/` (этим же флагом их гоняет CI, `.github/workflows/ci.yml`: список
модулей общий с `go_tools`, поэтому новый модуль подхватывается без правки
workflow). Запускается из корня чекаута, как ключ `test` из
`.devkit/deploy.local`.
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
    DK-166: кэш тестового прогона go обязан молчать. -timeout=20m поднят с
    дефолтных 10 минут go test: пакет dashboard изолированно укладывается в
    516с, а рядом с остальными компонентами конкуренция за машину регулярно
    выносит его за исходный потолок.
    """
    comps = [("go:" + tool, "tools/" + tool,
              ["go", "test", "-count=1", "-timeout=20m", "./..."]) for tool in go_tools(root)]
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


def cpu_budget():
    """Общий бюджет параллельности прогона: число ядер машины, минимум один.

    Одна точка, от которой считаются все три потолка (DK-1123): раньше
    верхний раннер, сюита devkitctl и `go test` каждый брали параллельность
    по-своему, и потолки перемножались вместо деления одного бюджета.
    """
    return max(1, os.cpu_count() or 4)


def component_share(jobs, budget=None):
    """Доля бюджета на один одновременно работающий компонент.

    При `jobs` одновременных компонентах и общем бюджете `budget` в худшем
    случае (все места заняты внутренне-параллельными компонентами) обязано
    держаться `jobs * share <= budget`: целочисленное деление с полом в
    единицу даёт это без исключений, включая ручной `-j` больше числа ядер
    (тогда доля падает к одному, а не растёт обратно).
    """
    budget = budget if budget is not None else cpu_budget()
    return max(1, budget // max(1, jobs))


def with_share(name, argv, share):
    """Argv компонента с долей бюджета, вставленной на его место.

    Го-компонент получает `-p <share>` перед списком пакетов (после списка
    флаг ушёл бы как путь), сюита devkitctl берёт готовый `-j <share>`
    (`suite.py` уже принимает этот аргумент). Остальные компоненты не умеют
    делиться внутри и argv не трогают.
    """
    if name.startswith("go:"):
        return argv[:-1] + ["-p", str(share)] + argv[-1:]
    if name == "devkitctl":
        return argv + ["-j", str(share)]
    return argv


def command_env(argv, share=None):
    """Окружение подпроцесса компонента, None оставляет окружение раннера.

    Go-компоненты идут с GOWORK=off: чужой go.work выше по дереву (находка
    DK-115) уводит go test из модуля утилиты, поэтому глушить workspace
    обязан сам раннер, а не только обёртка снаружи. GOMAXPROCS держит ту же
    долю бюджета, что и флаг `-p` в argv: без переменной рантайм go внутри
    одного тестового бинаря по-прежнему брал бы все ядра под горутины.
    """
    if argv[0] != "go":
        return None
    share = share if share is not None else cpu_budget()
    return dict(os.environ, GOWORK="off", GOMAXPROCS=str(share))


class _LiveShare:
    """Доля компонента по числу воркеров, ещё не исчерпавших очередь.

    Статичный `component_share(jobs, budget)` считался бы один раз перед всей
    пачкой и держался бы одним и тем же весь прогон (замечание ревью
    DK-1123): к хвосту, где очередь опустела и воркеры один за другим уходят
    ни с чем, доля новых компонентов обязана расти, а не оставаться такой,
    как будто по-прежнему работают все воркеры разом. Воркер выходит из игры
    только по факту пустой очереди (`exhausted`), а не по числу уже забранных
    компонентов: так `alive` не падает раньше времени, пока компонентов
    хватает на всех, и инвариант `alive_сейчас * доля(alive_при_старте) <=
    budget` держится - `alive` только убывает, поэтому доля уже бегущего
    компонента не может превысить долю, посчитанную по меньшему текущему
    `alive`.
    """

    def __init__(self, workers, budget):
        self._alive = max(1, workers)
        self._budget = budget
        self._lock = threading.Lock()

    def share(self):
        """Доля для компонента, который только что встал в работу."""
        with self._lock:
            return component_share(self._alive, self._budget)

    def exhausted(self):
        """Воркер нашёл очередь пустой и навсегда выходит из игры."""
        with self._lock:
            self._alive -= 1


def run_all(comps, workers, root=ROOT, budget=None):
    """Гонит компоненты параллельно с потолком воркеров.

    Отдаёт итоги (имя, код, секунды, вывод) в порядке завершения и имя первого
    провалившегося компонента либо None. Первый же неуспешный код валит прогон:
    занятые компоненты получают SIGTERM по группе процессов и в итог не
    попадают, а короткий хвост за тяжёлой сюитой успевает догнаться.

    `budget` это общий бюджет параллельности (по умолчанию `cpu_budget()`).
    Доля компонента внутрь (`-p` у go test, `-j` у сюиты devkitctl,
    `GOMAXPROCS`) считает `_LiveShare` по числу воркеров, ещё остающихся в
    игре, а не статичным расчётом на всю пачку разом (замечание ревью
    DK-1123, разбор в докстроке `_LiveShare`). У способа есть предел:
    компонент первого залпа, вставший в очередь до того, как хоть один
    воркер её исчерпал, получает ту же долю, что и при статичном расчёте, и
    она с ним навсегда - `go test` и рантайм go читают `-p` и `GOMAXPROCS`
    только при своём старте, домешать больше нельзя. Живой замер полного
    прогона проверяет, что это не раздувает стену выше потолка DK-1122.
    """
    budget = budget if budget is not None else cpu_budget()
    pending = queue.Queue()
    for comp in comps:
        pending.put(comp)
    outcomes, stop = [], threading.Event()
    first_fail, lock = [None], threading.Lock()
    live = _LiveShare(workers, budget)

    def worker():
        while not stop.is_set():
            try:
                name, rel, argv = pending.get_nowait()
            except queue.Empty:
                live.exhausted()
                return
            share = live.share()
            argv = with_share(name, argv, share)
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
                    argv, cwd=str(root / rel), env=command_env(argv, share),
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
    ap.add_argument("-j", dest="jobs", type=int, default=None,
                    help="потолок воркеров верхнего уровня, по компоненту на "
                         "поток (по умолчанию число ядер машины)")
    ap.add_argument("--list", action="store_true",
                    help="только перечень компонентов")
    ap.add_argument("--only-go", action="store_true",
                    help="только го-модули tools/ (тот же перечень, что у go_tools)")
    args = ap.parse_args(argv)
    budget = cpu_budget()
    jobs = args.jobs if args.jobs is not None else budget
    comps = components()
    if args.only_go:
        comps = [c for c in comps if c[0].startswith("go:")]
    if args.list:
        # Превью на полную пачку: --list не гоняет ни одного подпроцесса,
        # поэтому показывает худший случай (все компоненты стартуют разом),
        # тот же, что при живом прогоне держится на первом же круге.
        # Дальше по ходу прогона доля растёт по мере опустения очереди
        # (run_all, DK-1123) - число в --list это нижняя граница, а не
        # то единственное, что получит каждый компонент.
        share = component_share(jobs, budget)
        preview = [(name, rel, with_share(name, comp_argv, share))
                   for name, rel, comp_argv in comps]
        for name, rel, comp_argv in preview:
            print("%-16s (%s) %s" % (name, rel, " ".join(comp_argv)))
        print("компонентов: %d" % len(preview))
        return 0
    print("бюджет параллельности: ядра=%d jobs=%d доля=%d..%d (живая, растёт по "
          "мере опустения очереди)" % (budget, jobs, component_share(jobs, budget), budget))
    started = time.monotonic()
    outcomes, first_fail = run_all(comps, jobs, budget=budget)
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
