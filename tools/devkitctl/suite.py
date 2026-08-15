"""Раннер самопроверки devkitctl: классы тестов отдельными процессами.

Один discover на всю сюиту держит минуты, и тяжёлый хвост не режется путём
прогонов по модулям: один devkitctl_test из девятнадцати классов занимает
больше, чем вся остальная сюита. Поэтому единица параллели это тестовый
класс: у класса свой Sandbox, и классы одного модуля расходятся по воркерам,
не мешая ни друг другу, ни классам из чужих модулей.

Счёт тестов обязан совпадать с последовательным discover, поэтому перечень
классов раннер выводит тем же загрузчиком, а сводный счёт сверяет с его
прогоном: расход счёта это находка, а не молчание.
"""
import argparse
import os
import re
import subprocess
import sys
import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent

# Потолок воркеров от числа ядер: классы идут в своих процессах, но за каждым
# стоят подпроцессы доктора и git, и «ядра плюс два» задушает машину обменом
# вместо ускорения. На десяти ядрах стена выходит на плато между восемью и
# шестнадцатью воркерами и откатывается к двадцати четырём: дальше сюита
# упирается не в процессор, а в файловую систему и её демонов.
MAX_WORKERS = min(12, os.cpu_count() or 4)

# Итог прогона класса, по которому раннер берёт счёт его тестов. Строка итога
# надёжнее подсчёта строк прогона: unittest -v выносит докстринг теста на
# отдельную строку, и «... ok» уезжает с той, где имя теста.
SUMMARY_RE = re.compile(r"^Ran (\d+) tests? in ", re.M)


def _walk_cases(node):
    """Тесты из дерева suite в глубину, как их гоняет последовательный прогон."""
    for item in node:
        if isinstance(item, unittest.TestSuite):
            yield from _walk_cases(item)
        else:
            yield item


def enumerate_classes(start_dir=HERE, pattern="*_test.py"):
    """Перечень классов сюиты: «модуль.Класс» в порядке загрузчика.

    discover грузит модули прямо в процессе раннера, со всеми их побочными
    эффектами (стенд testenv подменяет HOME), и у последовательного прогона
    эффекты те же, поэтому чётность прогонов не ломается.
    """
    suite = unittest.TestLoader().discover(str(start_dir), pattern=pattern)
    classes = []
    for case in _walk_cases(suite):
        name = "%s.%s" % (type(case).__module__, type(case).__qualname__)
        if name not in classes:
            classes.append(name)
    return classes


def count_discover_tests(start_dir=HERE, pattern="*_test.py"):
    """Счёт тестов последовательного discover: с ним сверяется параллельный."""
    suite = unittest.TestLoader().discover(str(start_dir), pattern=pattern)
    return sum(1 for _ in _walk_cases(suite))


def run_class(name, cwd=HERE):
    """Прогон одного класса в своём процессе. Отдаёт код, вывод и счёт тестов."""
    proc = subprocess.run(
        [sys.executable, "-m", "unittest", "-v", name],
        cwd=str(cwd), stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        env={**os.environ, "PYTHONIOENCODING": "utf-8"})
    out = proc.stdout.decode("utf-8", "replace")
    m = SUMMARY_RE.search(out)
    return proc.returncode, out, int(m.group(1)) if m else 0


def run_suite(classes=None, workers=MAX_WORKERS, verbose=False, cwd=HERE):
    """Вся сюита по классам с потолком воркеров. Отдаёт счёт, удачу и время.

    Классы лежат в общей очереди, и освободившийся воркер берёт следующий:
    длительность класса раннеру заранее неизвестна, и жёсткая разметка по
    дорожкам собрала бы два тяжёлых класса в одну. Тяжёлые уходят в очередь
    первыми по числу тестов: число тестов грубая оценка длительности, но
    класс в 22 теста и впрямь дольше класса в два. Общая очередь выравнивает
    остаток сама: пока один воркер сидит в классе на минуту, остальные
    разбирают хвост.
    """
    classes = classes if classes is not None else enumerate_classes(cwd)
    weights = {}
    for name in classes:
        try:
            weights[name] = unittest.TestLoader().loadTestsFromName(name).countTestCases()
        except Exception:
            weights[name] = 0
    order = sorted(classes, key=lambda n: -weights[n])
    results, started = {}, time.monotonic()
    with ThreadPoolExecutor(max_workers=max(1, workers)) as pool:
        for name, res in zip(order, pool.map(lambda n: run_class(n, cwd), order)):
            results[name] = res
    if verbose:
        for name in order:
            rc, _, _ = results[name]
            print("%-52s %s" % (name, "ok" if rc == 0 else "FAIL"))
    passed = all(rc == 0 for rc, _, _ in results.values())
    ran = sum(count for _, _, count in results.values())
    return ran, passed, time.monotonic() - started, results


def fail_counts(results):
    """Сколько в провалах отказов и сколько ошибок: строка итога как у unittest."""
    failures = errors = 0
    for _, out, _ in results.values():
        failures += len(re.findall(r"^FAIL: ", out, re.M))
        errors += len(re.findall(r"^ERROR: ", out, re.M))
    return failures, errors


def main(argv=None, start_dir=HERE):
    ap = argparse.ArgumentParser(prog="suite", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-j", dest="jobs", type=int, default=MAX_WORKERS,
                    help="потолок воркеров, по классу на процесс (по умолчанию %d)" % MAX_WORKERS)
    ap.add_argument("-v", dest="verbose", action="store_true",
                    help="строка на каждый класс: имя и вердикт")
    ap.add_argument("--list", action="store_true",
                    help="только перечень классов сюиты")
    args = ap.parse_args(argv)
    classes = enumerate_classes(start_dir)
    if args.list:
        for name in classes:
            print(name)
        print("классов: %d" % len(classes))
        return 0
    expected = count_discover_tests(start_dir)
    ran, passed, secs, results = run_suite(classes, args.jobs, args.verbose, start_dir)
    for name in classes:
        rc, out, _ = results[name]
        if rc != 0:
            print(out)
    mins, secs = divmod(int(secs + 0.5), 60)
    print("Ran %d tests in %dm%02ds across %d classes" % (ran, mins, secs, len(classes)))
    if ran != expected:
        print("FAIL: counted %d tests, discover says %d: раннер потерял или "
              "придумал тесты, счёт обязан совпадать" % (ran, expected))
        return 1
    if not passed:
        print("FAILED (failures=%d, errors=%d)" % fail_counts(results))
        return 1
    print("OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
