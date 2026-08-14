"""Общий разбор журналов сессий харнеса.

Журналы `~/.claude/projects/<слепок пути>/*.jsonl` читают две команды: stats
--context (перезаписи префикса и объём по тулам) и drain (куда уходит вывод
инструментов). Разбор jsonl-строк и сопоставление tool_use с tool_result по id
живут тут, чтобы вторая команда не повела свой парсер рядом с первой
(требование DK-148: разбор jsonl не задвоен со stats).

Каждый потребитель берёт из журнала своё: requests -- usage-записи потока,
tool_pairs -- связки вызов-результат, records -- сырые записи для всего
остального. Сопоставление tool_use -> tool_result идёт по tool_use_id так же,
как в разовом скрипте DK-148: идентификатор ставится на вызов, а результат
приклеивается к нему по ключу.
"""
import json


def records(path):
    """Записи одного потока как словари, по порядку, без пустых и битых строк.

    Битая строка в журнале это норма: харнес пишет поток в реальном времени, и
    обрыв записи при отмене сессии не должен валить разбор.
    """
    for ln in path.read_text(encoding="utf-8", errors="replace").splitlines():
        ln = ln.strip()
        if not ln:
            continue
        try:
            rec = json.loads(ln)
        except ValueError:
            continue
        if isinstance(rec, dict):
            yield rec


def result_text(content):
    """Склеенный текст результата tool_result: список блоков или строка."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for block in content:
            if isinstance(block, dict):
                text = block.get("text")
                if isinstance(text, str):
                    parts.append(text)
            elif isinstance(block, str):
                parts.append(block)
        return "".join(parts)
    return ""


def tool_pairs(path):
    """Связки (tool_use, result_text, is_error) одного потока по порядку.

    Сначала по всему потоку строится соответствие id -> блок tool_use, а потом
    обходятся tool_result: так порядок вызов-раньше-результата не навязывается,
    и оборванный журнал, где результат пришёл без предшествующего tool_use
    (поток начинается с середины), помечается None на месте блока вызова.
    """
    uses, results = {}, []
    for rec in records(path):
        msg = rec.get("message")
        if not isinstance(msg, dict):
            continue
        content = msg.get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if not isinstance(block, dict):
                continue
            kind = block.get("type")
            if kind == "tool_use":
                uses[block.get("id")] = block
            elif kind == "tool_result":
                results.append(block)
    for block in results:
        yield (uses.get(block.get("tool_use_id")),
               result_text(block.get("content")),
               bool(block.get("is_error")))
