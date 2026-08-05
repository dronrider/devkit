"""Разбор журналов сессий харнеса: куда ушёл объём и перезаписи префикса.

Второй источник данных команды `stats`. Журнал запусков `.devkit/log` знает
только, какие команды звали, а куда сессия дела свой объём, видно в журналах
харнеса: `~/.claude/projects/<слепок пути проекта>/*.jsonl`, где у записей
ассистента лежит `message.usage` с записью и чтением кеша.

Считаются две вещи. Первая это разбивка объёма: сколько стоил старт (префикс,
записанный на первом запросе потока) против истории (всё, что дописалось
дальше: переписка, результаты тулов, тела скиллов). Вторая важнее: сколько раз
префикс переписывался целиком. Дизайн дерева контекста
(docs/lld/DK-100-context-tree.md, раздел «Состав контекста меняется только на
старте») запретил себе механизм, который правит голову контекста по ходу, и
проверяется этот запрет счётом перезаписей, а не обещанием.

Правило подписи перезаписи, оно же ответ на открытое место DK-108. Штатное
дописывание в хвост оставляет прочитанный префикс на месте: чтение кеша от
запроса к запросу растёт, а запись держится на объёме прироста. Перезаписью
считается запрос, у которого сошлись оба признака:

  1. чтение кеша просело относительно предыдущего запроса того же потока
     (прежний префикс не переиспользован);
  2. записано больше, чем прочитано (`cache_creation > cache_read`), то есть
     платили за новый префикс, а не за дописанный хвост.

Порога в токенах тут нет намеренно: оба признака относительные, и правило не
надо перенастраивать под длину сессии или модель. Первый запрос потока это
старт, он пишет префикс с нуля и считается отдельной строкой, а не нарушением.
"""
import json
import os
import re
from pathlib import Path

PROJECTS = "~/.claude/projects"
# Сколько перезаписей и тулов печатать поимённо: остальное сворачивается в
# хвостовую строку, иначе на живой машине вывод уезжает на десятки экранов.
TOP_REWRITES = 10
TOP_TOOLS = 8


def slug(path):
    """Слепок пути проекта: так харнес называет директорию журналов сессий."""
    return re.sub(r"[^A-Za-z0-9]", "-", str(path))


def logs_dir(root, home=None):
    home = Path(home or os.path.expanduser("~"))
    return home / ".claude" / "projects" / slug(root)


def streams(directory):
    """Журналы сессий директории проекта, включая субагентские.

    Журнал одной сессии бьётся на несколько файлов: рядом с `<id>.jsonl` лежит
    директория `<id>/subagents/` с потоками субагентов. У каждого потока свой
    префикс и своя цепочка запросов, поэтому файлы разбираются по отдельности.
    """
    return sorted(p for p in Path(directory).rglob("*.jsonl") if p.is_file())


def requests(path):
    """Запросы одного потока по порядку, без повторов.

    Запись с `usage` повторяется по числу итераций ответа, и все копии несут
    одинаковые числа: без склейки по `requestId` объём сессии вырос бы в разы.
    """
    seen = set()
    out = []
    for ln in path.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            rec = json.loads(ln)
        except ValueError:
            continue
        if not isinstance(rec, dict) or rec.get("type") != "assistant":
            continue
        msg = rec.get("message")
        if not isinstance(msg, dict):
            continue
        usage = msg.get("usage")
        if not isinstance(usage, dict):
            continue
        key = rec.get("requestId") or rec.get("uuid")
        if key in seen:
            continue
        seen.add(key)
        req = {
            "write": int(usage.get("cache_creation_input_tokens") or 0),
            "read": int(usage.get("cache_read_input_tokens") or 0),
            "input": int(usage.get("input_tokens") or 0),
            "output": int(usage.get("output_tokens") or 0),
            "model": msg.get("model") or "?",
        }
        if req["write"] + req["read"] + req["input"] == 0:
            continue
        out.append(req)
    return out


def rewrites(reqs):
    """Номера запросов потока, на которых префикс переписан (правило в шапке).

    Старт (первый запрос) в список не входит: он пишет префикс с нуля.
    """
    out = []
    for i in range(1, len(reqs)):
        cur, prev = reqs[i], reqs[i - 1]
        if cur["read"] < prev["read"] and cur["write"] > cur["read"]:
            out.append(i)
    return out


def tool_volume(path):
    """Объём результатов тулов потока в символах, по тулам.

    В токенах его тут не считают: результаты это код и вывод команд, а
    коэффициент DK-100 снят с русской прозы правил и на них соврал бы.
    """
    names, sizes = {}, {}
    for ln in path.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            rec = json.loads(ln)
        except ValueError:
            continue
        if not isinstance(rec, dict):
            continue
        msg = rec.get("message")
        if not isinstance(msg, dict):
            continue
        content = msg.get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if not isinstance(block, dict):
                continue
            if block.get("type") == "tool_use":
                names[block.get("id")] = block.get("name") or "?"
            elif block.get("type") == "tool_result":
                name = names.get(block.get("tool_use_id"), "неизвестный тул")
                sizes[name] = sizes.get(name, 0) + text_len(block.get("content"))
    return sizes


def text_len(content):
    if isinstance(content, str):
        return len(content)
    if isinstance(content, list):
        total = 0
        for block in content:
            if isinstance(block, dict):
                total += len(block.get("text") or "")
            elif isinstance(block, str):
                total += len(block)
        return total
    return 0


def collect(directory):
    """Сводка по всем потокам директории журналов."""
    data = {
        "streams": 0, "subagents": 0, "requests": 0,
        "start": 0, "history": 0, "read": 0, "output": 0,
        "rewrites": [], "models": {}, "tools": {},
    }
    for path in streams(directory):
        reqs = requests(path)
        if not reqs:
            continue
        data["streams"] += 1
        if path.parent.name == "subagents":
            data["subagents"] += 1
        data["requests"] += len(reqs)
        first = reqs[0]
        data["start"] += first["write"] + first["read"] + first["input"]
        for req in reqs[1:]:
            data["history"] += req["write"] + req["input"]
        for req in reqs:
            data["read"] += req["read"]
            data["output"] += req["output"]
            data["models"][req["model"]] = data["models"].get(req["model"], 0) + 1
        # Имя потока в отчёте короткое: полный id никуда не ведёт, а глазом по
        # нему сравнивают строки между собой.
        name = path.stem[6:] if path.stem.startswith("agent-") else path.stem
        for i in rewrites(reqs):
            data["rewrites"].append({
                "stream": name[:8], "n": i + 1, "of": len(reqs),
                "write": reqs[i]["write"],
                "read": reqs[i]["read"], "was": reqs[i - 1]["read"],
            })
        for name, size in tool_volume(path).items():
            data["tools"][name] = data["tools"].get(name, 0) + size
    return data


def num(value):
    return "{:,}".format(int(value)).replace(",", " ")


def report(directory, out):
    """Печать разбивки. Возвращает False, когда считать было нечего."""
    data = collect(directory)
    if data["streams"] == 0:
        return False
    volume = data["start"] + data["history"]
    tail = "потоков %d" % data["streams"]
    if data["subagents"]:
        tail += " (из них субагентских %d)" % data["subagents"]
    out.write("журналы сессий: %s\n" % directory)
    out.write("%s, запросов %d\n" % (tail, data["requests"]))
    models = sorted(data["models"].items(), key=lambda kv: -kv[1])
    out.write("модели: %s\n\n" % ", ".join("%s %d" % (m, c) for m, c in models))

    width = max(len(num(v)) for v in (data["start"], data["history"], data["read"]))
    out.write("старт    %*s  (%d%%)  префикс первого запроса потока, в среднем %s на поток\n"
              % (width, num(data["start"]), round(100 * data["start"] / volume),
                 num(round(data["start"] / data["streams"]))))
    out.write("история  %*s  (%d%%)  дописано дальше по потоку: переписка, результаты тулов, тела скиллов\n"
              % (width, num(data["history"]), round(100 * data["history"] / volume)))
    out.write("чтение   %*s         повторное чтение префикса, объёма не добавляет\n"
              % (width, num(data["read"])))

    rw = sorted(data["rewrites"], key=lambda r: -r["write"])
    cost = sum(r["write"] for r in rw)
    out.write("\nперезаписи префикса после старта: %d из %d запросов (%d%%), записано %s токенов\n"
              % (len(rw), data["requests"], round(100 * len(rw) / data["requests"]), num(cost)))
    for r in rw[:TOP_REWRITES]:
        out.write("  %-8s  запрос %d из %d: записано %s, чтение просело %s -> %s\n"
                  % (r["stream"], r["n"], r["of"], num(r["write"]),
                     num(r["was"]), num(r["read"])))
    if len(rw) > TOP_REWRITES:
        out.write("  и ещё %d\n" % (len(rw) - TOP_REWRITES))

    tools = sorted(data["tools"].items(), key=lambda kv: -kv[1])
    if tools:
        total = sum(size for _, size in tools)
        width = max(len(num(size)) for _, size in tools[:TOP_TOOLS])
        out.write("\nтоп тулов по объёму результатов, символов:\n")
        for name, size in tools[:TOP_TOOLS]:
            out.write("  %-16s %*s  (%d%%)\n"
                      % (name, width, num(size), round(100 * size / total)))
    return True
