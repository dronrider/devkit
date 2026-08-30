#!/usr/bin/env python3
"""Рубеж следов devkit в корп-репозитории: локальный ID доски и путь боковой
директории не уезжают ни в дифф, ни в сообщение коммита. Внутренняя кухня
разработчика чужому репозиторию не нужна, а ссылка на локальный ID из истории
компании ещё и никуда не ведёт.

Проверка молчит везде, кроме корп-клона: контур опознаётся редиректом
devkit.local (hooks/corp.py), и домашние проекты рубежа не замечают.

Режимы:
  check-traces.py --staged        добавленные строки staged-диффа и пути
                                  добавленных файлов
  check-traces.py --msg <файл>    сообщение коммита (комментарии git и хвост
                                  после scissors-строки не смотрятся)
"""
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import corp  # noqa: E402

SCISSORS = re.compile(r"^# -+ >8 -+$")
HUNK = re.compile(r"^@@ -\S+ \+([0-9]+)")

ADVICE = ("следы devkit остаются на машине: локальный ID и путь боковой "
          "директории в корп-репозитории не нужны никому, кроме вас\n"
          "осознанный обход: git commit --no-verify")
RIG_ADVICE = ("обвязка devkit живёт в дереве клона открыто, чтобы доску видели поиск "
              "редактора и сессия, но чужому репозиторию она не нужна\n"
              "снять из индекса: git rm --cached %s")


def staged_diff():
    out = subprocess.run(["git", "diff", "--cached", "-U0", "--diff-filter=ACMR"],
                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    return out.stdout.decode("utf-8", "replace").splitlines()


def added_lines(diff):
    """Пары (где, текст) по добавленным строкам. Путь файла идёт отдельной
    парой: имя вида DK-086.md это такой же след, как ID внутри текста."""
    out, path, num = [], "", 0
    for line in diff:
        if line.startswith("+++ b/"):
            path = line[6:]
            out.append((path, path))
            continue
        m = HUNK.match(line)
        if m:
            num = int(m.group(1))
            continue
        if line.startswith("+") and not line.startswith("+++"):
            out.append(("%s:%d" % (path, num), line[1:]))
            num += 1
    return out


def message_lines(path):
    out = []
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for i, line in enumerate(f, 1):
                if SCISSORS.match(line.rstrip("\n")):
                    break
                if line.startswith("#"):
                    continue
                out.append(("%s:%d" % (path, i), line.rstrip("\n")))
    except OSError as e:
        sys.stderr.write("check-traces: %s\n" % e)
        return None
    return out


def report_rig(paths):
    """Обвязка devkit в индексе. Отдельно от следов: тут дело не в словах, а в
    самих файлах, и снимается это не правкой текста, а git rm --cached."""
    if not paths:
        return 0
    sys.stderr.write("обвязка devkit в индексе корп-коммита:\n")
    sys.stderr.write("\n".join(paths) + "\n")
    sys.stderr.write(RIG_ADVICE % " ".join(paths) + "\n")
    return 1


def report(rules, items):
    findings = []
    for where, text in items:
        for kind in corp.scan(rules, text):
            findings.append("%s:[%s] %s" % (where, kind, text.strip()))
    if not findings:
        return 0
    sys.stderr.write("следы devkit в коммите корп-репозитория:\n")
    sys.stderr.write("\n".join(findings[:20]) + "\n")
    sys.stderr.write(ADVICE + "\n")
    return 1


def main(argv):
    local = corp.local_dir(".")
    if not local:
        return 0
    rules = corp.patterns(local)
    if argv[:1] == ["--staged"]:
        rc = report_rig(corp.staged_rig("."))
        return rc or report(rules, added_lines(staged_diff()))
    if argv[:1] == ["--msg"] and len(argv) > 1:
        items = message_lines(argv[1])
        if items is None:
            return 2
        return report(rules, items)
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
