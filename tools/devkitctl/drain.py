"""Регулярный замер расхода контекста по журналам сессий (команда drain).

Превращает разовый скрипт tstats.py из DK-148 в повторяемую команду: обходит
журналы сессий харнеса и печатает сводку того, куда уходит вывод инструментов
и токены. Журналы разбираются общим слоем sessions (тем же, что и stats
--context), а не своим парсером: требование DK-148 -- не заводить второй
разбор jsonl рядом с существующим.

Привязка результата к вызову идёт по tool_use_id: объём вывода каждого
вызова приклеивается к его головной команде и инструменту, а не разваливается
по конвейеру. Разбор Bash по головной команде, sed (правка файла против
чтения фрагмента), чтения файлом целиком против куска, повторные чтения,
перцентили размера одного вывода и хвост самых жирных -- всё это решает одна
проходка по записям потока, как в разовом скрипте.
"""
import re
from collections import Counter, defaultdict

import context
import sessions

# Сколько строк держать в хвосте самых жирных выводов: на живой машине их
# тысячи, и держать все бессмысленно, замер режет до этого размера после
# сортировки.
TOP_FAT = 60

SED_INPLACE = re.compile(r"\bsed\b[^|;&]*\s-[a-zA-Z]*i")
SED_PRINT = re.compile(r"\bsed\b[^|;&]*-n[^|;&]*[0-9,$]+\s*p")

# Уточнение головной команды для мультикоманд: «go test» и «go build» это
# разные по расходу вызовы, и сводить их в одну «go» значит потерять сигнал.
MULTI = {"git", "go", "npm", "cargo", "python3", "python", "docker", "gh",
         "taskctl", "shipctl", "agentctl", "devkitctl", "make", "brew",
         "pnpm", "yarn", "kubectl", "systemctl", "pytest", "uv"}

SPLIT = re.compile(r"\|\||&&|\||;|\n")

# Обломки многострочных скриптов и heredoc, командами не являются.
NOISE = {"if", "fi", "then", "else", "elif", "for", "do", "done", "while",
         "case", "esac", "}", "{", "EOF", "PY", "SH", "import", "def", "class",
         "return", "in", "e", "f", "s", "print", "with", "try", "except",
         "from", "-", "--", "*", "*)", ")", "&&", "||", "exit", "break"}

TOK_KEYS = ("input_tokens", "output_tokens",
            "cache_creation_input_tokens", "cache_read_input_tokens")

DENY_MARKS = ("permission", "не разреш", "requested permissions",
              "user doesn't want")


def cmd_keys(command):
    """Первое слово каждого сегмента конвейера, с уточнением для мультикоманд.

    «cd /path && cmd» это навигация, содержательная команда в следующем
    сегменте; присваивания переменных и sudo пропускаются как не-команды.
    Возвращает список головных команд сегментов в порядке конвейера.
    """
    keys = []
    for seg in SPLIT.split(command or ""):
        parts = seg.strip().split()
        while parts and ("=" in parts[0].split("/")[-1][:20]
                         and not parts[0].startswith("-")
                         or parts[0] in ("sudo", "command", "time", "env")):
            parts = parts[1:]
        if not parts or parts[0] == "cd":
            continue
        head = parts[0].split("/")[-1] or parts[0]
        if head in NOISE or head.startswith(("\"", "'", "#", "$")):
            continue
        if head in MULTI and len(parts) > 1 and not parts[1].startswith("-"):
            head = head + " " + parts[1]
        keys.append(head)
    return keys


def _sed_kind(command):
    if SED_INPLACE.search(command):
        return "правка файла (-i)"
    if SED_PRINT.search(command):
        return "чтение фрагмента (-n Np)"
    return "фильтр в конвейере"


def _read_kind(inp):
    if inp.get("limit") or inp.get("offset"):
        return "кусок (limit/offset)"
    if inp.get("pages"):
        return "страницы PDF"
    return "файл целиком"


def _label(tool, inp):
    if tool == "Bash":
        return " ".join((inp.get("command") or "").split())[:120]
    if tool in ("Read", "Edit", "Write", "NotebookEdit"):
        return str(inp.get("file_path") or "")
    if tool in ("Grep", "Glob"):
        return str(inp.get("pattern") or "")
    return ""


def fresh():
    """Пустая сводка: все счётчики, что собирает замер, в одном месте."""
    return {
        "sessions": 0, "lines_bad": 0,
        "calls": Counter(), "out_chars": Counter(),
        "bash_calls": Counter(), "bash_chars": Counter(),
        "bash_seen": Counter(), "bash_head": Counter(),
        "sizes": [], "sizes_by_tool": defaultdict(list),
        "fattest": [], "tok": Counter(),
        "sed_kind": Counter(), "sed_chars": Counter(),
        "read_kind": Counter(), "read_chars": Counter(),
        "shell_read": Counter(), "shell_read_chars": Counter(),
        "repeat_by_file": Counter(), "total_reads": 0,
        "denied": Counter(),
    }


def _collect_stream(path, data):
    """Один поток: проходка по записям с тем же состоянием, что в разовом скрипте.

    pending хранит метаданные вызова от tool_use до tool_result: объём вывода
    приклеивается к команде по tool_use_id. reads_here считает повторы Read
    внутри сессии, а не по всему корпусу.
    """
    pending = {}
    reads_here = Counter()
    for rec, bad in sessions.parsed(path):
        if bad:
            data["lines_bad"] += 1
        if rec is None:
            continue
        msg = rec.get("message")
        if not isinstance(msg, dict):
            continue
        content = msg.get("content")

        if rec.get("type") == "assistant":
            u = msg.get("usage") or {}
            for k in TOK_KEYS:
                v = u.get(k)
                if isinstance(v, int):
                    data["tok"][k] += v
            if not isinstance(content, list):
                continue
            for blk in content:
                if not isinstance(blk, dict) or blk.get("type") != "tool_use":
                    continue
                tool = blk.get("name") or "?"
                inp = blk.get("input") or {}
                data["calls"][tool] += 1
                extra = None
                if tool == "Bash":
                    cm = inp.get("command") or ""
                    ks = cmd_keys(cm)
                    for k in set(ks):
                        data["bash_calls"][k] += 1
                    if ks:
                        data["bash_head"][ks[0]] += 1
                    if "sed" in set(ks):
                        what = _sed_kind(cm)
                        data["sed_kind"][what] += 1
                        extra = ("sed", what)
                    elif ks and ks[0] in ("cat", "head", "tail"):
                        extra = ("shellread", ks[0])
                        data["shell_read"][ks[0]] += 1
                elif tool == "Read":
                    reads_here[str(inp.get("file_path") or "")] += 1
                    what = _read_kind(inp)
                    data["read_kind"][what] += 1
                    extra = ("read", what)
                pending[blk.get("id")] = (tool, inp, extra)

        elif rec.get("type") == "user" and isinstance(content, list):
            for blk in content:
                if not isinstance(blk, dict) or blk.get("type") != "tool_result":
                    continue
                text = sessions.result_text(blk.get("content"))
                n = len(text)
                key = blk.get("tool_use_id") or blk.get("id")
                tool, inp, extra = pending.pop(key, ("?", {}, None))
                if extra:
                    kind, what = extra
                    if kind == "sed":
                        data["sed_chars"][what] += n
                    elif kind == "read":
                        data["read_chars"][what] += n
                    elif kind == "shellread":
                        data["shell_read_chars"][what] += n
                data["out_chars"][tool] += n
                data["sizes"].append(n)
                data["sizes_by_tool"][tool].append(n)
                if tool == "Bash":
                    ks = cmd_keys(inp.get("command"))
                    if ks:
                        # Объём вызова на головную команду, иначе конвейер
                        # «git log | head» задвоит счёт по обоим участникам.
                        data["bash_chars"][ks[0]] += n
                    for k in set(ks):
                        data["bash_seen"][k] += n
                if blk.get("is_error"):
                    low = text[:400].lower()
                    if any(mark in low for mark in DENY_MARKS):
                        data["denied"][tool] += 1
                data["fattest"].append((n, tool, _label(tool, inp)))
                if len(data["fattest"]) > 4000:
                    data["fattest"].sort(reverse=True)
                    del data["fattest"][300:]

    for path_label, count in reads_here.items():
        data["total_reads"] += count
        if count > 1:
            data["repeat_by_file"][path_label] += count - 1
    data["sessions"] += 1


def collect(directory):
    """Сводка по всем потокам директории журналов."""
    data = fresh()
    for path in context.streams(directory):
        _collect_stream(path, data)
    data["fattest"].sort(reverse=True)
    del data["fattest"][TOP_FAT:]
    return data


def _pct(vals, p):
    if not vals:
        return 0
    s = sorted(vals)
    return s[min(len(s) - 1, int(len(s) * p / 100))]


def _human(n):
    for unit in ("", "K", "M", "G"):
        if abs(n) < 1000:
            return f"{n:.0f}{unit}" if unit == "" else f"{n:.1f}{unit}"
        n /= 1000
    return f"{n:.1f}T"


def report(directory, out):
    """Печать сводки. Возвращает False, когда вызывающих потоков не нашлось."""
    data = collect(directory)
    if data["sessions"] == 0:
        return False
    total_out = sum(data["out_chars"].values())
    total_calls = sum(data["calls"].values())
    out.write("сессий: %d, вызовов инструментов: %d, битых строк: %d\n"
              % (data["sessions"], total_calls, data["lines_bad"]))
    out.write("суммарный вывод инструментов: %s символов (~%s токенов)\n\n"
              % (_human(total_out), _human(total_out / 3.5)))

    out.write("=== ТОКЕНЫ ПО USAGE (все сессии) ===\n")
    tok = data["tok"]
    tot_tok = sum(tok[k] for k in TOK_KEYS)
    for k in ("cache_read_input_tokens", "cache_creation_input_tokens",
              "input_tokens", "output_tokens"):
        share = 100 * tok[k] / tot_tok if tot_tok else 0
        out.write("  %-32s %8s  %5.1f%%\n" % (k, _human(tok[k]), share))
    out.write("\n")

    out.write("=== ИНСТРУМЕНТЫ: вызовы и объём вывода ===\n")
    out.write("%-14s %8s %6s %8s %6s %8s %8s %9s\n"
              % ("инструмент", "вызовов", "%выз", "вывод", "%выв",
                 "медиана", "p90", "max"))
    for tool, n in data["calls"].most_common(20):
        ch = data["out_chars"][tool]
        ss = data["sizes_by_tool"].get(tool, [])
        out.write("%-14s %8d %5.1f%% %8s %5.1f%% %8d %8d %9d\n"
                  % (tool, n, 100 * n / total_calls if total_calls else 0,
                     _human(ch), 100 * ch / total_out if total_out else 0,
                     _pct(ss, 50), _pct(ss, 90), max(ss) if ss else 0))
    out.write("\n")

    nbash = data["calls"]["Bash"]
    out.write("=== BASH: топ команд по ЧИСЛУ вызовов (доля от вызовов Bash) ===\n")
    for k, n in data["bash_calls"].most_common(25):
        out.write("  %-26s в %6d вызовах (%4.1f%%), головной в %6d, "
                  "вывод как головной %8s\n"
                  % (k, n, 100 * n / nbash if nbash else 0,
                     data["bash_head"][k], _human(data["bash_chars"][k])))
    out.write("\n")

    bash_out = data["out_chars"]["Bash"]
    out.write("=== BASH: топ команд по ОБЪЁМУ вывода (команда головная в вызове) ===\n")
    for k, ch in data["bash_chars"].most_common(25):
        n = data["bash_head"][k]
        avg = ch / n if n else 0
        out.write("  %-26s %8s (%4.1f%% Bash)  вызовов %5d  в среднем %7s\n"
                  % (k, _human(ch), 100 * ch / bash_out if bash_out else 0,
                     n, _human(avg)))
    out.write("\n")

    out.write("=== ЧЕМ БЫ ЭТО МОГЛО БЫТЬ: доля вызовов Bash с поиском и правкой текста ===\n")
    for k in ("grep", "rg", "sed", "awk", "cat", "head", "tail", "find", "ls", "echo"):
        n = data["bash_calls"].get(k, 0)
        out.write("  %-8s %6d вызовов (%4.1f%%), объём где головная: %8s\n"
                  % (k, n, 100 * n / nbash if nbash else 0,
                     _human(data["bash_chars"].get(k, 0))))
    out.write("\n")

    out.write("=== SED: зачем его зовут ===\n")
    ts = sum(data["sed_kind"].values())
    for k, n in data["sed_kind"].most_common():
        out.write("  %-28s %6d (%4.1f%%)  вывод %8s\n"
                  % (k, n, 100 * n / ts if ts else 0, _human(data["sed_chars"][k])))
    out.write("\n")

    out.write("=== ЧТЕНИЕ ФАЙЛА: инструментом Read или средствами shell ===\n")
    tr = sum(data["read_kind"].values())
    for k, n in data["read_kind"].most_common():
        out.write("  Read, %-22s %6d (%4.1f%%)  вывод %8s  в среднем %7s\n"
                  % (k, n, 100 * n / tr if tr else 0, _human(data["read_chars"][k]),
                     _human(data["read_chars"][k] / n if n else 0)))
    for k, n in data["shell_read"].most_common():
        out.write("  shell, %-21s %6d            вывод %8s  в среднем %7s\n"
                  % (k, n, _human(data["shell_read_chars"][k]),
                     _human(data["shell_read_chars"][k] / n if n else 0)))
    out.write("\n")

    out.write("=== РАСПРЕДЕЛЕНИЕ РАЗМЕРА ОДНОГО ВЫВОДА ===\n")
    sizes = data["sizes"]
    for p in (50, 75, 90, 95, 99):
        out.write("  p%-3d %9d символов\n" % (p, _pct(sizes, p)))
    out.write("  max  %9d символов\n" % (max(sizes) if sizes else 0))
    big = [s for s in sizes if s > 20000]
    out.write("  вызовов тяжелее 20K символов: %d (%.2f%% вызовов, %.1f%% всего вывода)\n\n"
              % (len(big), 100 * len(big) / len(sizes) if sizes else 0,
                 100 * sum(big) / total_out if total_out else 0))

    out.write("=== ТОП-25 САМЫХ ЖИРНЫХ ОДИНОЧНЫХ ВЫВОДОВ ===\n")
    for n, tool, label in data["fattest"][:25]:
        out.write("  %7s  %-8s %s\n" % (_human(n), tool, label[:90]))
    out.write("\n")

    waste = sum(data["repeat_by_file"].values())
    out.write("=== ПОВТОРНЫЕ ЧТЕНИЯ ОДНОГО ФАЙЛА ВНУТРИ СЕССИИ (топ 15) ===\n")
    out.write("  всего Read: %d, из них повторных внутри сессии: %d (%.1f%%)\n"
              % (data["total_reads"], waste,
                 100 * waste / data["total_reads"] if data["total_reads"] else 0))
    for k, v in data["repeat_by_file"].most_common(15):
        out.write("  +%4d  %s\n" % (v, k[:95]))
    out.write("\n")

    if data["denied"]:
        out.write("=== ОТКАЗЫ РАЗРЕШЕНИЙ ===\n")
        for k, v in data["denied"].most_common(10):
            out.write("  %-14s %d\n" % (k, v))
    return True
