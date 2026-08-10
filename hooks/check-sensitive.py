#!/usr/bin/env python3
"""Проверка п. 8 раздела «Трекинг задач» RULES.board.md: в коммит не
попадают IP, доступы, токены, корп-домены и другие приметы инфраструктуры.
Вместо адреса пишется роль машины («роутер DE», «VPS RU»), конкретика живёт
в гитигнорнутом local-docs. Заодно проверяется сам local-docs: заигнорен ли
он на записи и не уехал ли в коммит, иначе правило держится на том, что
агент вспомнил про .gitignore.

Скоуп шире доски: смотрится любой коммитимый файл вне служебных каталогов
SKIP_DIRS (vendor, node_modules, сборочные артефакты) и тестовых файлов
SKIP_FILES (фикстуры с адресами и токенами как данные). Так секрет, попавший
в исходник или в доку вне docs/, ловится тем же рубежом, что и доска.

Корп-домен в этом файле не стоит: хук берёт его в рантайме из переменной
окружения DEVKIT_CORP_DOMAIN или из ключа corp_domain в RULES.local.md (рядом
с CWD), список через запятую. Источника нет, домен неизвестен, паттерн не
активен, хук молчит. Так значение домена не въезжает ни в код, ни в коммит.

Режимы:
  check-sensitive.py <файл>...      проверить файлы целиком (фильтра путей нет,
                                    что передали, то и смотрим)
  ... | check-sensitive.py --diff   строки вида файл:строка:текст (staged-дифф
                                    из pre-commit), смотрятся файлы вне SKIP_DIRS
                                    и попавший в коммит local-docs
  check-sensitive.py --hook [протокол]
                                    хук на запись файла: JSON события на stdin,
                                    записанный фрагмент, файлы вне SKIP_DIRS.
                                    Разбор входа и канал ответа по имени
                                    протокола из hookio.py, голый --hook это
                                    claude-code
"""
import fnmatch
import os
import re
import subprocess
import sys

import hookio

# Служебные каталоги, где IP или похожие на секрет строки это норма:
# сторонний код, индекс git, сборочный мусор. Сравнение по компоненте пути,
# поэтому «vendor» покрывает и «vendor/lib.go», и «app/vendor/lib.go».
SKIP_DIRS = {
    ".git", ".devkit", "node_modules", "vendor", "__pycache__",
    ".venv", "venv", "dist", "build",
}

# Тестовые файлы тоже держат адреса и токены как фикстуры, и это не утечка:
# рубёж там не смотрит, как и в служебных каталогах. Шаблоны применяются к
# имени файла (последняя компонента пути), поэтому накрывают и
# hooks/foo_test.py, и pkg/internal/test_helpers.go.
SKIP_FILES = (
    "*_test.*",
    "test_*.*",
    "*.spec.*",
)

# Loopback, документационные диапазоны RFC 5737 и маски не выдают инфраструктуру.
ALLOWED_IP = re.compile(r"^(127\.|0\.0\.0\.0$|192\.0\.2\.|198\.51\.100\.|203\.0\.113\.|255\.255\.255\.)")

STATIC_PATTERNS = [
    ("IP-адрес", re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")),
    ("IPv6-адрес", re.compile(r"\b(?:[0-9A-Fa-f]{1,4}:){4,}[0-9A-Fa-f:]+\b")),
    ("приватный ключ", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("токен", re.compile(
        r"\b(?:AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{22,}"
        r"|glpat-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}"
        r"|eyJ[A-Za-z0-9_-]{15,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,})")),
    ("секрет со значением", re.compile(
        r"(?i)\b(?:password|passwd|пароль|secret|token|api[_-]?key|access[_-]?key)\b"
        r"\s*[:=]\s*[\"']?[A-Za-z0-9_\-./+=]{6,}")),
]

DOMAIN_ENV = "DEVKIT_CORP_DOMAIN"
LOCAL_RULES = "RULES.local.md"


LOCAL_DOCS = "local-docs"

LOCAL_DOCS_ADVICE = (
    "local-docs едет в гит (RULES.board.md, «Трекинг задач» п. 8): конкретика "
    "доступов и адресов живёт там и остаётся на машине.\n"
    "что делать: добавить строку local-docs/ в .gitignore, а уже попавшее "
    "убрать из индекса (git rm --cached -r local-docs)")


def local_docs_path(path):
    return LOCAL_DOCS in (path or "").replace("\\", "/").split("/")


def ignored(path):
    """Заигнорен ли путь по мнению самого git. Вне репозитория и без git ответа
    нет, и тогда путь считается заигноренным: рубеж тут не сторож."""
    try:
        r = subprocess.run(["git", "check-ignore", "-q", "--", path],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except OSError:
        return True
    return r.returncode != 1


def in_scope(path):
    """Входит ли путь в скоуп проверки: любой коммитимый файл, кроме
    служебных каталогов SKIP_DIRS и тестовых файлов SKIP_FILES. Так секрет
    в исходнике или в доке вне docs/ ловится тем же рубежом, что и доска, а
    сторонний код, сборочный мусор и тестовые фикстуры с адресами и токенами
    пропускаются. Прямая подача <файл> фильтра путей не имеет."""
    parts = (path or "").replace("\\", "/").split("/")
    if any(part in SKIP_DIRS for part in parts):
        return False
    name = parts[-1] if parts else ""
    return not any(fnmatch.fnmatch(name, pat) for pat in SKIP_FILES)


def local_rule(key):
    """Значение ключа из RULES.local.md рядом с CWD. Формат плоский, как у
    .devkit/tracker.local: «key = value» или «key: value» с решёткой на
    комментарий. Файла или ключа нет, и приходит пусто. Сюда же смотрит хук
    за значением корп-домена, когда источника в окружении нет."""
    try:
        with open(LOCAL_RULES, encoding="utf-8", errors="replace") as f:
            for raw in f:
                line = raw.split("#", 1)[0].strip()
                if not line:
                    continue
                name, sep, val = line.partition("=")
                if not sep:
                    name, sep, val = line.partition(":")
                if not sep or name.strip() != key:
                    continue
                return val.strip()
    except OSError:
        pass
    return ""


def corp_domains():
    """Корп-домены из окружения и RULES.local.md. Значение домена в коде не
    стоит, оно въезжает в хук в рантайме. Источника нет, и приходит пусто:
    паттерн не активен, хук молчит. Возврат: список доменов без дубликатов,
    порядок сохранён."""
    found, seen = [], set()

    def add(value):
        for d in value.split(","):
            d = d.strip().lower()
            if d and d not in seen:
                seen.add(d)
                found.append(d)

    add(os.environ.get(DOMAIN_ENV, ""))
    add(local_rule("corp_domain"))
    return found


def patterns():
    """Список (имя, regex) для текущего запуска. Статические паттерны всегда
    активны, корп-домен достраивается, когда его значение нашлось в окружении
    или в RULES.local.md. Граница домена это «не буква, цифра, точка или
    дефис», чтобы example.corp не матчил my-example.corp.host."""
    rules = list(STATIC_PATTERNS)
    domains = corp_domains()
    if domains:
        rx = re.compile(
            r"(?<![\w.-])(?:" + "|".join(re.escape(d) for d in domains) + r")(?![\w.-])",
            re.IGNORECASE)
        rules.append(("корп-домен", rx))
    return rules


def line_kinds(line, rules):
    kinds = []
    for name, rx in rules:
        for m in rx.finditer(line):
            if name == "IP-адрес":
                ip = m.group(0)
                if ALLOWED_IP.match(ip) or any(int(o) > 255 for o in ip.split(".")):
                    continue
            kinds.append(name)
            break
    return kinds


def scan(lines, rules, where=None):
    findings = []
    for i, line in enumerate(lines, 1):
        for kind in line_kinds(line, rules):
            prefix = "%s:%d:" % (where, i) if where else ""
            findings.append("%s[%s] %s" % (prefix, kind, line.rstrip("\n")))
    return findings


def run_diff(rules):
    findings, staged_local = [], []
    for raw in sys.stdin:
        parts = raw.rstrip("\n").split(":", 2)
        if len(parts) < 3:
            continue
        if local_docs_path(parts[0]) and parts[0] not in staged_local:
            staged_local.append(parts[0])
        if not in_scope(parts[0]):
            continue
        for kind in line_kinds(parts[2], rules):
            findings.append("%s:%s:[%s] %s" % (parts[0], parts[1], kind, parts[2]))
    if staged_local:
        findings.append("\n".join(staged_local) + "\n" + LOCAL_DOCS_ADVICE)
    for f in findings:
        print(f)
    return 1 if findings else 0


def run_hook(protocol, rules):
    write = hookio.write_event(protocol)
    if write is None:
        return 0
    if local_docs_path(write.path) and not ignored(write.path):
        return hookio.reply(protocol).found(LOCAL_DOCS_ADVICE + "\n")
    if not in_scope(write.path):
        return 0
    findings = []
    for chunk in write.chunks:
        findings += scan(chunk.splitlines(), rules)
    if not findings:
        return 0
    return hookio.reply(protocol).found(
        "чувствительное в коммите (RULES.board.md, «Трекинг задач» п. 8) в %s:\n%s\n"
        "вместо адресов и доступов писать роль машины («роутер DE», «VPS RU»), "
        "конкретику держать в гитигнорнутом local-docs\n"
        % (write.path, "\n".join(findings[:20]))
    )


def main(argv):
    rules = patterns()
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]), rules)
        except hookio.Unknown as e:
            sys.stderr.write("check-sensitive: %s\n" % e)
            return 2
    if argv[:1] == ["--diff"]:
        return run_diff(rules)
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    findings = []
    for path in argv:
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                findings += scan(f, rules, where=path)
        except OSError as e:
            sys.stderr.write("check-sensitive: %s\n" % e)
            return 2
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
