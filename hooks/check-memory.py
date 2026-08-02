#!/usr/bin/env python3
"""Линт индекса памяти MEMORY.md: одна короткая строка-указатель на запись.

Индекс грузится в контекст каждой сессии проекта, поэтому содержимое живёт в
файлах памяти, а здесь только «- [заголовок](файл.md) - крючок». Разжиревшие
строки (журнал со статусами, хешами коммитов, версиями) уходят в файл памяти.

Режимы:
  check-memory.py <MEMORY.md>...   проверка файлов, выход 1 если есть находки
  check-memory.py --hook [протокол]
                                   хук на запись файла: JSON события на stdin,
                                   проверяется записанный фрагмент. Где лежит
                                   индекс, знает профиль харнеса (`[hooks]
                                   memory_index`); без ключа памяти у
                                   инструмента нет, и хук молчит
"""
import sys

import hookio

MAX_LINE = 160


def scan(lines, where=None):
    findings = []
    for i, line in enumerate(lines, 1):
        line = line.rstrip("\n")
        if not line.strip():
            continue
        prefix = "%s:%d: " % (where, i) if where else "строка %d: " % i
        if not line.startswith("- ["):
            findings.append(prefix + "не строка-указатель вида «- [заголовок](файл.md) - крючок»")
        elif len(line) > MAX_LINE:
            findings.append(prefix + "длина %d > %d символов, содержимому место в файле памяти" % (len(line), MAX_LINE))
    return findings


def run_hook(protocol):
    write = hookio.write_event(protocol)
    # Где лежит индекс, знает профиль харнеса. Ключа нет, значит памяти у
    # инструмента нет, и находки про её индекс были бы шумом.
    index = hookio.memory_index(protocol)
    if write is None or not index or not write.path.endswith(index):
        return 0
    findings = []
    for chunk in write.chunks:
        findings += scan(chunk.splitlines())
    if not findings:
        return 0
    return hookio.reply(protocol).found(
        "индекс памяти пухнет (%s):\n%s\n"
        "в MEMORY.md держи короткие строки-указатели, содержимое и статусы пиши в файл памяти\n"
        % (write.path, "\n".join(findings[:10]))
    )


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-memory: %s\n" % e)
            return 2
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    findings = []
    for path in argv:
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                findings += scan(f, where=path)
        except OSError as e:
            sys.stderr.write("check-memory: %s\n" % e)
            return 2
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
