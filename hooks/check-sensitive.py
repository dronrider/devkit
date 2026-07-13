#!/usr/bin/env python3
"""Проверка п. 8 раздела «Трекинг задач» RULES.board.md: в доску, файлы задач
и LLD не попадают IP, доступы и другие приметы инфраструктуры. Вместо адреса
пишется роль машины («роутер DE», «VPS RU»), конкретика живёт в гитигнорнутом
local-docs.

Режимы:
  check-sensitive.py <файл>...      проверить файлы целиком (фильтра путей нет,
                                    что передали, то и смотрим)
  ... | check-sensitive.py --diff   строки вида файл:строка:текст (staged-дифф
                                    из pre-commit), смотрятся только файлы доски
  check-sensitive.py --hook         PostToolUse-хук Claude Code: JSON на stdin,
                                    записанный фрагмент, только файлы доски;
                                    находки уходят агенту в stderr с выходом 2
"""
import json
import re
import sys

BOARD_PARTS = ("docs/TASKS.md", "docs/TASKS-archive.md", "docs/tasks/", "docs/lld/")

# Loopback, документационные диапазоны RFC 5737 и маски не выдают инфраструктуру.
ALLOWED_IP = re.compile(r"^(127\.|0\.0\.0\.0$|192\.0\.2\.|198\.51\.100\.|203\.0\.113\.|255\.255\.255\.)")

PATTERNS = [
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


def board_path(path):
    p = (path or "").replace("\\", "/")
    return any(part in p for part in BOARD_PARTS)


def line_kinds(line):
    kinds = []
    for name, rx in PATTERNS:
        for m in rx.finditer(line):
            if name == "IP-адрес":
                ip = m.group(0)
                if ALLOWED_IP.match(ip) or any(int(o) > 255 for o in ip.split(".")):
                    continue
            kinds.append(name)
            break
    return kinds


def scan(lines, where=None):
    findings = []
    for i, line in enumerate(lines, 1):
        for kind in line_kinds(line):
            prefix = "%s:%d:" % (where, i) if where else ""
            findings.append("%s[%s] %s" % (prefix, kind, line.rstrip("\n")))
    return findings


def run_diff():
    findings = []
    for raw in sys.stdin:
        parts = raw.rstrip("\n").split(":", 2)
        if len(parts) < 3 or not board_path(parts[0]):
            continue
        for kind in line_kinds(parts[2]):
            findings.append("%s:%s:[%s] %s" % (parts[0], parts[1], kind, parts[2]))
    for f in findings:
        print(f)
    return 1 if findings else 0


def run_hook():
    try:
        data = json.load(sys.stdin)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return 0
    ti = data.get("tool_input") or {}
    path = ti.get("file_path") or ti.get("notebook_path") or ""
    if not board_path(path):
        return 0
    chunks = [ti.get(k) for k in ("new_string", "content", "new_source") if ti.get(k)]
    for e in ti.get("edits") or []:
        if isinstance(e, dict) and e.get("new_string"):
            chunks.append(e["new_string"])
    findings = []
    for chunk in chunks:
        findings += scan(chunk.splitlines())
    if not findings:
        return 0
    sys.stderr.write(
        "чувствительное в файлах доски (RULES.board.md, «Трекинг задач» п. 8) в %s:\n%s\n"
        "вместо адресов и доступов писать роль машины («роутер DE», «VPS RU»), "
        "конкретику держать в гитигнорнутом local-docs\n"
        % (path, "\n".join(findings[:20]))
    )
    return 2


def main(argv):
    if argv[:1] == ["--hook"]:
        return run_hook()
    if argv[:1] == ["--diff"]:
        return run_diff()
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    findings = []
    for path in argv:
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                findings += scan(f, where=path)
        except OSError as e:
            sys.stderr.write("check-sensitive: %s\n" % e)
            return 2
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
