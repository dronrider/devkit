#!/usr/bin/env python3
"""Проверка п. 1 раздела «Код и тексты» RULES.md: только символы клавиатурных
раскладок en/ru, «ёлочки» и №.

Файлы из testdata не проверяются ни по путям, ни хуком: там лежат снимки
чужого вывода, которые переписывать нельзя.

Режимы:
  check-symbols.py <файл>...    находки вида файл:строка:текст, выход 1 если есть
  ... | check-symbols.py --stdin
  check-symbols.py --hook [протокол]
                                хук на запись файла: JSON события на stdin,
                                проверяется записанный фрагмент, а не файл
                                целиком, поэтому совпадения в чужом
                                существующем тексте не всплывают. Разбор входа
                                и канал ответа берутся по имени протокола из
                                hookio.py, голый --hook это claude-code
"""
import re
import sys

import hookio

BAD = re.compile(r"[^\x00-\x7Fа-яА-ЯёЁ«»№]")


def is_testdata(path):
    # Снимки чужого вывода в testdata переписывать нельзя, значит и проверять
    # их незачем: иначе такой снимок красит проверку насовсем.
    return "testdata" in (path or "").split("/")


def scan(lines, where=None):
    findings = []
    for i, line in enumerate(lines, 1):
        if BAD.search(line):
            prefix = "%s:%d:" % (where, i) if where else ""
            findings.append(prefix + line.rstrip("\n"))
    return findings


def run_hook(protocol):
    write = hookio.write_event(protocol)
    if write is None or is_testdata(write.path):
        return 0
    findings = []
    for chunk in write.chunks:
        findings += scan(chunk.splitlines())
    if not findings:
        return 0
    return hookio.reply(protocol).found(
        "запрещённые символы (RULES.md, «Код и тексты» п. 1) в %s:\n%s\n"
        "перепиши клавиатурными символами; чужой код и тестовые данные можно оставить как есть\n"
        % (write.path or "?", "\n".join(findings[:20]))
    )


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-symbols: %s\n" % e)
            return 2
    findings = []
    if argv[:1] == ["--stdin"]:
        findings = scan(sys.stdin)
    else:
        if not argv:
            sys.stderr.write(__doc__)
            return 2
        for path in argv:
            if is_testdata(path):
                continue
            try:
                with open(path, encoding="utf-8", errors="replace") as f:
                    findings += scan(f, where=path)
            except OSError as e:
                sys.stderr.write("check-symbols: %s\n" % e)
                return 2
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
