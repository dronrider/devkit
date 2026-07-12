#!/usr/bin/env python3
"""Проверка п. 1 раздела «Код и тексты» RULES.md: только символы клавиатурных
раскладок en/ru, «ёлочки» и №.

Режимы:
  check-symbols.py <файл>...    находки вида файл:строка:текст, выход 1 если есть
  ... | check-symbols.py --stdin
  check-symbols.py --hook       PostToolUse-хук Claude Code: JSON на stdin,
                                проверяется записанный фрагмент (new_string /
                                content), а не файл целиком, поэтому совпадения
                                в чужом существующем тексте не всплывают;
                                находки уходят агенту в stderr с выходом 2
"""
import json
import re
import sys

BAD = re.compile(r"[^\x00-\x7Fа-яА-ЯёЁ«»№]")


def scan(lines, where=None):
    findings = []
    for i, line in enumerate(lines, 1):
        if BAD.search(line):
            prefix = "%s:%d:" % (where, i) if where else ""
            findings.append(prefix + line.rstrip("\n"))
    return findings


def run_hook():
    try:
        data = json.load(sys.stdin)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return 0
    ti = data.get("tool_input") or {}
    chunks = [ti.get(k) for k in ("new_string", "content", "new_source") if ti.get(k)]
    for e in ti.get("edits") or []:
        if isinstance(e, dict) and e.get("new_string"):
            chunks.append(e["new_string"])
    findings = []
    for chunk in chunks:
        findings += scan(chunk.splitlines())
    if not findings:
        return 0
    path = ti.get("file_path") or ti.get("notebook_path") or "?"
    sys.stderr.write(
        "запрещённые символы (RULES.md, «Код и тексты» п. 1) в %s:\n%s\n"
        "перепиши клавиатурными символами; чужой код и тестовые данные можно оставить как есть\n"
        % (path, "\n".join(findings[:20]))
    )
    return 2


def main(argv):
    if argv[:1] == ["--hook"]:
        return run_hook()
    findings = []
    if argv[:1] == ["--stdin"]:
        findings = scan(sys.stdin)
    else:
        if not argv:
            sys.stderr.write(__doc__)
            return 2
        for path in argv:
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
