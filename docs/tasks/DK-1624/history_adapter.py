"""Перевод истории разговора между Claude Code и Codex (DK-1624).

Прямое направление превращает журнал клиента Claude Code в потоковом формате
stream-json в элементы Responses API, которые принимает метод thread/inject_items
сервера codex app-server. Обратное направление собирает известный обмен с Codex
в строки stream-json на вход клиента Claude Code. Оба преобразования чистые:
список на входе, список на выходе, поэтому границы форматов проверяются
тестами без живых клиентов.

Порядок элементов сохраняется, потому что причинность «вызов инструмента
после сообщения, результат после вызова» проверяется принимающей моделью
именно порядком. Служебные строки журнала (system, result) это обвязка
протокола клиента, а не разговор, и в историю они не едут.
"""
import json
import uuid


def claude_journal_to_codex_items(lines):
    """Строки журнала stream-json -> элементы Responses API для inject_items."""
    items = []
    for raw in lines:
        line = raw.strip() if isinstance(raw, str) else raw
        if not line:
            continue
        event = json.loads(line)
        kind = event.get("type")
        if kind not in ("user", "assistant"):
            continue
        message = event.get("message") or {}
        content = message.get("content")
        if isinstance(content, str):
            blocks = [{"type": "text", "text": content}]
        elif isinstance(content, list):
            blocks = content
        else:
            continue
        pending_text = []
        for block in blocks:
            btype = block.get("type")
            if btype == "text" and block.get("text"):
                pending_text.append(block["text"])
                continue
            if pending_text:
                items.append(_message_item(kind, pending_text))
                pending_text = []
            if kind == "assistant" and btype == "tool_use":
                items.append({
                    "type": "function_call",
                    "call_id": block.get("id", ""),
                    "name": block.get("name", ""),
                    "arguments": json.dumps(block.get("input") or {},
                                            ensure_ascii=False),
                })
            elif kind == "user" and btype == "tool_result":
                items.append({
                    "type": "function_call_output",
                    "call_id": block.get("tool_use_id", ""),
                    "output": _tool_result_text(block),
                })
        if pending_text:
            items.append(_message_item(kind, pending_text))
    return items


def _message_item(role, texts):
    content_key = "output_text" if role == "assistant" else "input_text"
    return {
        "type": "message",
        "role": "assistant" if role == "assistant" else "user",
        "content": [{"type": content_key, "text": "\n".join(texts)}],
    }


def _tool_result_text(block):
    content = block.get("content")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = [part.get("text", "") for part in content
                 if isinstance(part, dict) and part.get("type") == "text"]
        return "\n".join(parts)
    return str(content)


def codex_items_to_exchange(items):
    """Элементы Responses API -> ходы обмена для обратной сборки.

    Возвращает список словарей трёх видов: text (роль и текст), tool_call
    (имя, call_id, аргументы строкой) и tool_output (call_id, вывод).
    """
    exchange = []
    for item in items:
        itype = item.get("type")
        if itype == "message":
            texts = [part.get("text", "") for part in item.get("content", [])
                     if isinstance(part, dict)]
            exchange.append({"kind": "text",
                             "role": item.get("role", "user"),
                             "text": "\n".join(texts)})
        elif itype == "function_call":
            exchange.append({"kind": "tool_call",
                             "name": item.get("name", ""),
                             "call_id": item.get("call_id", ""),
                             "arguments": item.get("arguments", "")})
        elif itype == "function_call_output":
            exchange.append({"kind": "tool_output",
                             "call_id": item.get("call_id", ""),
                             "output": item.get("output", "")})
    return exchange


def exchange_to_claude_stream_lines(exchange):
    """Ходы обмена -> строки stream-json на вход клиента Claude Code.

    Формат строк пользователя с uuid и session_id повторяет подтверждённую
    ручную пробу диспетчера, ассистентские строки идут без служебных полей.
    Строки tool_use и tool_result в историческом входе это экспериментальная
    гипотеза задачи, поэтому собираются отдельным видом хода и при отказе
    клиента исключаются повторной сборкой без них.
    """
    lines = []
    for step in exchange:
        kind = step.get("kind")
        if kind == "text":
            role = "assistant" if step["role"] == "assistant" else "user"
            event = {"type": role, "message": {
                "role": role,
                "content": [{"type": "text", "text": step["text"]}],
            }}
            if role == "user":
                event["parent_tool_use_id"] = None
                event["session_id"] = ""
                event["uuid"] = str(uuid.uuid4())
            lines.append(json.dumps(event, ensure_ascii=False))
        elif kind == "tool_call":
            arguments = step.get("arguments", "")
            try:
                parsed = json.loads(arguments) if arguments else {}
            except ValueError:
                parsed = {"raw": arguments}
            lines.append(json.dumps({
                "type": "assistant",
                "message": {"role": "assistant", "content": [{
                    "type": "tool_use",
                    "id": step.get("call_id", ""),
                    "name": step.get("name", ""),
                    "input": parsed,
                }]},
            }, ensure_ascii=False))
        elif kind == "tool_output":
            lines.append(json.dumps({
                "type": "user",
                "message": {"role": "user", "content": [{
                    "type": "tool_result",
                    "tool_use_id": step.get("call_id", ""),
                    "content": [{"type": "text", "text": step["output"]}],
                }]},
                "parent_tool_use_id": None,
                "session_id": "",
                "uuid": str(uuid.uuid4()),
            }, ensure_ascii=False))
    return lines
