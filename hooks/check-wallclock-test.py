#!/usr/bin/env python3
"""Сторож стенного порога в новых тестах (DK-1126): рубеж на коммите, который
ловит тест, сравнивающий измеренное время с числовым порогом или с другим
измеренным временем. Семейство таких строк набралось за цель DK-1084 (DK-764,
DK-746, DK-908, DK-1038 и другие), каждый раз потому что новый тест писался по
старому образцу. Приём «мерить факт вместо времени» выбран человеком
2026-09-12 в DK-764 и стоит образцом в kit/skills/test-standard/SKILL.md,
«Факт вместо стенного времени».

Смотрятся добавленные строки коммита, тем же порядком, что у check-tests.py и
check-symbols.py, и ловятся две формы одного приёма:

  - Go: тестовый файл (`*_test.go`), где строка сравнивает (<, >, <=, >=)
    измеренное значение с константой time.Duration (`took > 5*time.Millisecond`)
    либо, когда файл вообще меряет время (`time.Since(` или `time.Now()` уже
    встречались), сравнивает между собой два значения, у одного из которых имя
    похоже на измерение (`warm > cold`, `el1 < el2`).
  - Python: тестовый файл (`*_test.py` либо `test_*.py`), где среди
    добавленных строк есть часы (`time.time()`, `time.perf_counter()`,
    `time.monotonic()`, `timeit.timeit(...)` и родственники) и порог: вызов
    assertLess, assertGreater, assertLessEqual, assertGreaterEqual из unittest
    (образец до правки DK-1038), либо сравнение с числом или с другим
    измерением рядом со словом из семьи «elapsed», «took», «cold», «warm»,
    «idle», «age», «time» и похожих (простой `assert`, без unittest).

Живой калибровочный прогон (obeycheck, сценарий 55-fact-not-wallclock) нашёл
третью форму помимо порога и отношения из «Факт вместо стенного времени»:
голое сравнение двух измерений без всякого порога («второй вызов должен быть
быстрее первого»). Она того же рода: под нагрузкой оба замера плывут, и
порядок между ними ничем не гарантирован. Отсюда «сравнивает с другим
измеренным временем» выше, а не только «с константой».

Не всякое обращение к `time.*` это порог. Срок ожидания внешнего события
(`context.WithTimeout`, `time.After` в select, `.Add(N).Before(...)`,
`while time.time() < deadline:` опроса) не сравнивает измеренное время с
числом, а просто открывает время на подождать, и сюда не попадает: различие
того же рода, что в разборе замка стенда DK-764 и развилке «рычаг» DK-1084
(потолок ожидания это боевое поведение, а не измерение, которое пора выразить
фактом).

Ложная находка гасится пометкой на той же строке: `стенной порог: <причина>`.
Мимо строки с такой пометкой сторож проходит молча.

Режимы:
  check-wallclock-test.py <файл>...   находки вида файл:строка:текст, весь
                                       файл читается целиком
  ... | check-wallclock-test.py --diff  строки вида файл:строка:текст
                                       (staged-дифф из pre-commit, добавленные
                                       строки коммита)

Выход 0 чисто, 1 находка. Обход осознанный: пометка `стенной порог: <причина>`
в самом тесте либо `git commit --no-verify`.
"""
import re
import sys

MARK = "стенной порог:"

# Окно строк вокруг живого замера, в котором голое сравнение или assert ещё
# считаются про это время. DK-1038 до правки держал часы в одном методе
# (`clock()`), порог в другом, класс тот же: между последней строкой часов и
# assertLess было 10 строк. Без окна (вся файл целиком) шум выше пользы: в
# крупном тестовом файле `time.time()` часто стоит ради штампа фикстуры за
# сотни строк от несвязанного assertLess на размере файла или списка (живой
# прогон сторожа по семейству нашёл ровно такой случай).
WINDOW = 20

# Слова, которыми в этой ветке кода называют измеренное время. Список не
# исчерпывающий: это гейт против шума на голых сравнениях (см. scan_go и
# scan_py ниже), не список всех допустимых имён. У «age» суффикса `\w*` нет:
# с ним слово матчит «agentctl» целиком (agent начинается с age), находка
# из живого прогона по quota_refresh_test.py. Вся альтернатива в одной
# группе: `\b` снаружи и `|` внутри не смешиваются сами, врозь «age» без
# ведущего `\b` матчил середину «usage» (тот же прогон, соседняя строка).
DURATION_WORD = (
    r"(?:(?:elapsed|took|duration|latency|cost|wait(?:ed)?|idle|bare|cold|"
    r"warm|plain|spent|time)\w*|age)"
)

GO_UNIT = r"time\.(?:Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)"
GO_THRESHOLD_RE = re.compile(
    r"[<>]=?\s*(?:\d[\d_]*\s*\*\s*)?" + GO_UNIT + r"\b"
    r"|(?:\d[\d_]*\s*\*\s*)?" + GO_UNIT + r"\s*[<>]="
)
# Только time.Since, не time.Now: голое «сейчас» ставит штамп много где
# (аргумент фикстуры, дедлайн), а вычитает интервал только Since. Взятое
# файлом целиком «время где-то есть» красило feed_test.go и main_test.go
# просто по совпадению слова «cold»/«age» в несвязанной строке.
GO_CLOCK_RE = re.compile(r"\btime\.Since\s*\(")
GO_DURATION_WORD_RE = re.compile(r"\b" + DURATION_WORD + r"\b", re.IGNORECASE)
# «<» не через «<-»: канал-приёмник Go начинается с тех же двух символов
# (`case <-time.After(...)`), и без исключения голое сравнение путало срок
# ожидания с порогом (askpass_test.go).
GO_COMPARE_RE = re.compile(r"<(?!-)=?|>=?")

PY_ASSERT_RE = re.compile(r"\bassert(?:Less|Greater)(?:Equal)?\s*\(")
PY_CLOCK_RE = re.compile(
    r"\btime\.(?:time|perf_counter|monotonic)\s*\(\s*\)|\btimeit\b"
)
PY_DURATION_WORD_RE = re.compile(r"\b" + DURATION_WORD + r"\b", re.IGNORECASE)
PY_COMPARE_RE = re.compile(r"[<>]=?")
# `while time.time() < deadline:` это срок ожидания, тот же приём, что Go
# `time.Now().Before(deadline)` из докстринга. Условие цикла не измерение,
# а опрос до предела, и в находки бы шло каждое поле «until»/«deadline» в
# опросе (`quota_refresh_test.py`, `task_run_test.py` в живом прогоне).
PY_POLL_LOOP_RE = re.compile(r"\bwhile\b")


def nearby(clock_lines, target, window=WINDOW):
    return any(abs(c - target) <= window for c in clock_lines)


def is_go_test(path):
    return path.endswith("_test.go")


def is_py_test(path):
    name = path.rsplit("/", 1)[-1]
    return name.endswith("_test.py") or (name.startswith("test_") and name.endswith(".py"))


def scan_go(numbered_lines, where):
    """Порог по литералу ловится построчно, без оглядки на остальной файл:
    `took > 5*time.Millisecond` выдаёт себя сам. Голое сравнение двух измерений
    (`warm > cold`) такого якоря не несёт, и его ловит только рядом со строкой,
    где стоит живой замер (`time.Since`, в окне WINDOW строк): иначе
    `coldRead > whole/10` в файле, который время меряет за сотню строк отсюда
    в несвязанном тесте, попадало бы в находки просто по имени переменной
    (`feed_test.go`, `main_test.go` в живом прогоне сторожа)."""
    clock_lines = [i for i, line in numbered_lines if GO_CLOCK_RE.search(line)]
    findings = []
    for i, line in numbered_lines:
        if MARK in line:
            continue
        if GO_THRESHOLD_RE.search(line):
            findings.append("%s:%d:%s" % (where, i, line.rstrip("\n")))
            continue
        if (nearby(clock_lines, i) and GO_COMPARE_RE.search(line)
                and GO_DURATION_WORD_RE.search(line)):
            findings.append("%s:%d:%s" % (where, i, line.rstrip("\n")))
    return findings


def scan_py(numbered_lines, where):
    """Часы и порог часто живут в разных строках: часы в хелпере измерения,
    порог в самой проверке (образец до правки DK-1038, `clock()` и
    `assertLess` разнесены по методам, но в одном классе, строк десять между
    ними). Окно то же WINDOW, что у Go: без него `time.time()` ради штампа
    фикстуры за сотни строк красил бы любой assertLess дальше по файлу
    (`chat_in_test.py` в живом прогоне сторожа)."""
    clock_lines = [i for i, line in numbered_lines if PY_CLOCK_RE.search(line)]
    if not clock_lines:
        return []
    findings = []
    for i, line in numbered_lines:
        if MARK in line:
            continue
        if not nearby(clock_lines, i):
            continue
        if PY_POLL_LOOP_RE.search(line):
            continue
        hit = PY_ASSERT_RE.search(line) or (
            PY_COMPARE_RE.search(line) and PY_DURATION_WORD_RE.search(line)
        )
        if hit:
            findings.append("%s:%d:%s" % (where, i, line.rstrip("\n")))
    return findings


def check_lines(path, numbered_lines):
    if is_go_test(path):
        return scan_go(numbered_lines, path)
    if is_py_test(path):
        return scan_py(numbered_lines, path)
    return []


def scan_file(path):
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            lines = list(enumerate(f, 1))
    except OSError:
        return []
    return check_lines(path, lines)


def run_diff():
    """Добавленные строки коммита: файл, номер, текст через двоеточие, тем же
    форматом, что у check-symbols.py --diff. Строки группируются по файлу,
    иначе ни питоновский приём (часы отдельно от порога), ни голое сравнение
    в Go (часы в одной строке файла, сравнение в другой) не собрать."""
    by_file = {}
    order = []
    for raw in sys.stdin:
        parts = raw.rstrip("\n").split(":", 2)
        if len(parts) < 3:
            continue
        path, lineno, text = parts
        try:
            lineno = int(lineno)
        except ValueError:
            continue
        if path not in by_file:
            by_file[path] = []
            order.append(path)
        by_file[path].append((lineno, text))
    findings = []
    for path in order:
        findings += check_lines(path, by_file[path])
    return findings


def main(argv):
    if argv[:1] == ["--diff"]:
        findings = run_diff()
    else:
        if not argv:
            sys.stderr.write(__doc__)
            return 2
        findings = []
        for path in argv:
            findings += scan_file(path)
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
