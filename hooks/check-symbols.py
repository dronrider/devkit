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

# Разбор по видам находок: что запрещено, сказано в правилах, а чем это
# переписывать, приходится решать заново каждый раз, и подсказка нужна ровно
# в момент ошибки. Порядок перечня и есть порядок разбора в выводе.
ADVICE = (
    (re.compile("[\u2012-\u2015]"),
     "длинное тире: перестроить предложение живой прозой, а не подменять "
     "двоеточием, запятой или дефисом"),
    (re.compile("[\u2190-\u21ff\u2794-\u27bf\u2b00-\u2b11]"),
     "стрелка: писать её знаками ASCII, -> <- =>"),
    (re.compile("\u2026"),
     "многоточие: три точки подряд"),
    (re.compile("[\u2018\u2019\u201a\u201c\u201d\u201e]"),
     "кавычки-лапки: наши ёлочки"),
    (re.compile("[\u00a0\u2007\u2009\u202f]"),
     "неразрывный пробел: обычный пробел"),
    (re.compile("[\u2600-\u27bf\ufe0f\U0001f000-\U0001faff]"),
     "эмодзи: убрать, замены им нет"),
)

OTHER = ("символ вне раскладок en/ru: подобрать клавиатурный аналог; "
         "чужой код и тестовые данные можно оставить как есть")


def advice(findings):
    """Строки разбора по видам символов, которые встретились в находках."""
    seen = {c for f in findings for c in BAD.findall(f)}
    lines = [hint for rx, hint in ADVICE if any(rx.match(c) for c in seen)]
    rest = [c for c in seen if not any(rx.match(c) for rx, _ in ADVICE)]
    if rest or not lines:
        lines.append(OTHER)
    return lines


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
        "как переписать:\n%s\n"
        % (write.path or "?", "\n".join(findings[:20]),
           "\n".join("- " + a for a in advice(findings)))
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
    if findings:
        print("как переписать:")
        for a in advice(findings):
            print("- " + a)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
