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
(`cpu_budget`), и он не суммируется, а делится: доля уезжает компоненту
внутрь (`suite.py` аргументом `-j`, `go test` флагом `-p`), а сумма долей
одновременно работающих компонентов держится в бюджете. Переменной
`GOMAXPROCS` раннер долю не ставит, разбор в докстроке `command_env`.

Делится бюджет не поровну, а по весу компонента. Компонентов восемнадцать, и
семнадцать из них это один процесс со своей внутренней параллелью, а сюита
devkitctl сама раздаёт работу воркерам по классу на процесс, и она же полюс
прогона: замер 2026-09-23 дал ей 1161 с при десяти своих воркерах и 1205 с
при пяти, тогда как следующий по длине компонент укладывается в 526-586 с.
Равная доля по числу лайнов свела бы сюиту к одному воркеру, то есть к
последовательному прогону ста пятидесяти семи классов, и стена ушла бы за
потолок 1560 с из замера DK-1122. Поэтому половина бюджета уходит раннеру
(`runner_share`), а вторая половина лайнам верхнего уровня (`lane_count`):
доля раннера плюс по единице каждому остальному лайну и складываются в
бюджет. Лайнов теперь меньше, чем компонентов, поэтому порядок очереди решает
стену, и раннер уходит в неё первым (`queue_order`).

Доля обычного компонента не статичная на весь прогон, а живая: её считает
`_Budget` по числу компонентов, работающих прямо сейчас, и по длине хвоста
очереди. К концу прогона, когда очередь опустела и соседи разошлись, доля
новых компонентов растёт. Предел у способа один и честный: доля едет
компоненту его стартом, и уже бегущему её не домешать, `go test` читает `-p`
только при своём запуске.

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


# Компонент, который внутри себя сам раздаёт работу воркерам: сюита devkitctl
# гонит сто пятьдесят семь классов по процессу на класс. Остальные компоненты
# это один процесс, и делить им между собой нечего.
RUNNER = "devkitctl"


def runner_share(budget=None):
    """Доля бюджета компоненту-раннеру: половина машины.

    Сюита devkitctl это полюс прогона, а не рядовой компонент. Замер
    2026-09-23 дал ей 1161 с при десяти своих воркерах внутри полного прогона
    и 1205 с при пяти в одиночку, а следующий по длине компонент
    (`go:dashboard`) укладывается в 526-586 с. Один воркер свёл бы её к
    последовательному прогону классов, и полюсом стала бы она сама, далеко за
    потолком 1560 с из DK-1122. Половина бюджета держит сюиту рядом с плато
    её собственного раннера (восемь-шестнадцать воркеров, разбор в `suite.py`)
    и оставляет вторую половину лайнам верхнего уровня.
    """
    budget = budget if budget is not None else cpu_budget()
    return max(1, budget // 2)


def lane_count(budget=None, reserved=0):
    """Потолок воркеров верхнего уровня при этой раздаче.

    Лайнов столько, чтобы доля раннера и доли остальных лайнов сложились в
    бюджет: по единице на лайн и остаток раннеру. На десяти ядрах с раннером
    это шесть лайнов при доле раннера пять. Срез без раннера (`--only-go`,
    тот же прогон в CI) делит бюджет только между лайнами, и лайнов там
    столько же, сколько ядер.
    """
    budget = budget if budget is not None else cpu_budget()
    return max(1, budget - reserved + (1 if reserved else 0))


def component_share(jobs, budget=None, reserved=0):
    """Доля бюджета обычному компоненту при `jobs` занятых лайнах.

    `reserved` это доля, отданная раннеру: она вычитается из бюджета вместе
    со своим лайном, а остаток делится между остальными. В худшем случае (все
    лайны заняты внутренне-параллельными компонентами) держится
    `reserved + (jobs - 1) * share <= budget`. Целочисленное деление даёт это
    до `lane_count` лайнов включительно, дальше вступает пол в единицу: ручной
    `-j` больше числа лайнов долю не поднимает и ниже единицы не опускает.
    """
    budget = budget if budget is not None else cpu_budget()
    lanes = max(1, jobs - (1 if reserved else 0))
    return max(1, (budget - reserved) // lanes)


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


def command_env(argv):
    """Окружение подпроцесса компонента, None оставляет окружение раннера.

    Go-компоненты идут с GOWORK=off: чужой go.work выше по дереву (находка
    DK-115) уводит go test из модуля утилиты, поэтому глушить workspace
    обязан сам раннер, а не только обёртка снаружи.

    Доля бюджета уезжает го-компоненту флагом `-p`, а переменной GOMAXPROCS
    раннер её не ставит, хотя постановка DK-1123 звала и её. Замер
    2026-09-23 показал, чем это кончается. GOMAXPROCS это не потолок работы,
    а смена режима рантайма: с одним-двумя полюсами горутина, дождавшаяся
    своего подпроцесса, ждёт освободившегося полюса вместо того, чтобы
    бежать рядом. Под полным прогоном такая задержка красит гонку уборки
    TempDir в пакете dashboard, где фоновая горутина дописывает файл в дом
    стенда. С GOMAXPROCS=1 провалились пять полных прогонов из пяти, с
    GOMAXPROCS=2 шестой, а без переменной прогон той же раздачи прошёл
    зелёным. Машине переменная при этом ничего не сберегла: тесты го-модулей
    `t.Parallel` не зовут, процессор жгут их подпроцессы, и load average
    прогона с ней и без неё один. Гонка живёт черновиком DK-1136, и с её
    починкой переменную можно вернуть.
    """
    if argv[0] != "go":
        return None
    return dict(os.environ, GOWORK="off")


def queue_order(comps):
    """Порядок очереди: раннер первым, остальные как были.

    Лайнов меньше, чем компонентов, поэтому очередь решает стену прогона.
    Сюита devkitctl длиннее следующего компонента вдвое (1161-1205 с против
    526-586 с по замеру 2026-09-23), и взятая из очереди последней она
    удлинила бы стену на всю свою длину. Порядок остальных не трогается:
    длительности их раннер не знает, а выдумывать вес по имени значит держать
    список, который молча разойдётся с деревом (находка DK-367).
    """
    return ([c for c in comps if c[0] == RUNNER]
            + [c for c in comps if c[0] != RUNNER])


class _Budget:
    """Живая раздача бюджета: доля считается на старте каждого компонента.

    Статичный расчёт на всю пачку разом держал бы долю одной и той же весь
    прогон (замечание ревью DK-1123). Первая доработка считала живость по
    воркерам, ещё не нашедшим очередь пустой, и не меняла ничего: воркер
    объявляет очередь пустой ровно тогда, когда брать больше нечего, так что
    ни один компонент не успевал стартовать с уменьшившимся счётом. Счёт
    поэтому идёт по компонентам, работающим прямо сейчас, и убывает на конце
    каждого.

    Доля раннера (`RUNNER`) снята с бюджета заранее и от счёта соседей не
    зависит: он полюс прогона и делится внутри себя сам. Как только раннер
    кончился, его доля возвращается в бюджет остальным.

    Обычная доля считается по худшему из ближайшего будущего: к числу уже
    работающих компонентов прибавляется хвост очереди, потому что свободные
    лайны займутся тут же. Без этой поправки первый же компонент забрал бы
    весь бюджет, а стартовавшие следом сложились бы с ним в перебор.
    """

    def __init__(self, budget, jobs, reserved=0):
        self._budget = budget
        self._jobs = max(1, jobs)
        self._reserved = reserved
        self._running = {}
        self._lock = threading.Lock()

    def take(self, name, waiting=0):
        """Доля компоненту, который сейчас встаёт в работу.

        `waiting` это длина хвоста очереди на этот момент.
        """
        with self._lock:
            if name == RUNNER and self._reserved:
                self._running[name] = self._reserved
                return self._reserved
            lanes = max(1, self._jobs - (1 if self._reserved else 0))
            busy = 1 + sum(1 for n in self._running if n != RUNNER)
            ordinary = min(lanes, busy + waiting)
            jobs = ordinary + (1 if self._reserved else 0)
            share = component_share(jobs, self._budget, self._reserved)
            self._running[name] = share
            return share

    def drop(self, name):
        """Компонент кончился: его доля возвращается в бюджет."""
        with self._lock:
            self._running.pop(name, None)
            if name == RUNNER:
                self._reserved = 0


def run_all(comps, workers, root=ROOT, budget=None):
    """Гонит компоненты параллельно с потолком воркеров.

    Отдаёт итоги (имя, код, секунды, вывод) в порядке завершения и имя первого
    провалившегося компонента либо None. Первый же неуспешный код валит прогон:
    занятые компоненты получают SIGTERM по группе процессов и в итог не
    попадают, а короткий хвост за тяжёлой сюитой успевает догнаться.

    `budget` это общий бюджет параллельности (по умолчанию `cpu_budget()`).
    Доля компонента внутрь (`-p` у go test, `-j` у сюиты devkitctl)
    считается на старте каждого компонента по факту идущих соседей
    (`_Budget`, разбор в его докстроке), а раннер берёт свою половину бюджета
    и уходит в очередь первым (`queue_order`). Предел способа честный: уже
    бегущему компоненту долю не домешать, `go test` читает `-p` только при
    своём запуске.
    """
    budget = budget if budget is not None else cpu_budget()
    comps = queue_order(comps)
    pending = queue.Queue()
    for comp in comps:
        pending.put(comp)
    reserved = (runner_share(budget)
                if any(name == RUNNER for name, _, _ in comps) else 0)
    outcomes, stop = [], threading.Event()
    first_fail, lock = [None], threading.Lock()
    live = _Budget(budget, workers, reserved)

    def worker():
        while not stop.is_set():
            try:
                name, rel, argv = pending.get_nowait()
            except queue.Empty:
                return
            share = live.take(name, pending.qsize())
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
                    argv, cwd=str(root / rel), env=command_env(argv),
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                    start_new_session=True)
            except Exception as exc:
                live.drop(name)
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
            # Доля возвращается в бюджет концом компонента, а не концом
            # очереди: следующий за ним компонент считает свою долю уже по
            # поредевшему прогону.
            live.drop(name)
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
                         "поток (по умолчанию остаток бюджета после доли "
                         "раннера, а без раннера число ядер машины)")
    ap.add_argument("--list", action="store_true",
                    help="только перечень компонентов")
    ap.add_argument("--only-go", action="store_true",
                    help="только го-модули tools/ (тот же перечень, что у go_tools)")
    args = ap.parse_args(argv)
    budget = cpu_budget()
    comps = components()
    if args.only_go:
        comps = [c for c in comps if c[0].startswith("go:")]
    has_runner = any(name == RUNNER for name, _, _ in comps)
    reserved = runner_share(budget) if has_runner else 0
    jobs = args.jobs if args.jobs is not None else lane_count(budget, reserved)
    if args.list:
        # Превью на полную пачку: --list не гоняет ни одного подпроцесса,
        # поэтому показывает худший случай (все лайны заняты разом), тот же,
        # что при живом прогоне держится на первом круге. Дальше по ходу
        # прогона доля обычного компонента растёт по мере опустения очереди
        # (run_all, DK-1123) - число в --list это нижняя граница, а не то
        # единственное, что получит каждый компонент.
        share = component_share(jobs, budget, reserved)
        preview = [(name, rel,
                    with_share(name, comp_argv,
                               reserved if name == RUNNER and reserved else share))
                   for name, rel, comp_argv in queue_order(comps)]
        for name, rel, comp_argv in preview:
            print("%-16s (%s) %s" % (name, rel, " ".join(comp_argv)))
        print("компонентов: %d" % len(preview))
        return 0
    print("бюджет параллельности: ядра=%d лайнов=%d доля раннера=%d "
          "доля компонента=%d..%d (живая, растёт по мере опустения очереди)"
          % (budget, jobs, reserved, component_share(jobs, budget, reserved),
             budget))
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
