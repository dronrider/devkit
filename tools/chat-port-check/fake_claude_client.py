#!/usr/bin/env python3
"""Фикстура теста: клиент Claude Code на stream-json (DK-1624).

Изображает ровно то, что нужно сценарию, без сети и модели. В фазе задачи
собирает журнал с вызовами Bash, длинным выводом с маркерами, правкой файла
и строкой assistant_fact, файл notes.txt пишет в текущий каталог. В фазе
опроса достаёт значения фактов из поданной истории и отвечает JSON, это
доказывает, что факты действительно проехали через stdin. Переменная
FAKE_CLAUDE_REJECT_TOOLS изображает отказ клиента принимать исторические
tool_use: выход 1 и сообщение об ошибке в stderr.
"""
import json
import os
import re
import sys
import uuid

UUID_RE = "[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}"


def emit(event):
    print(json.dumps(event, ensure_ascii=False), flush=True)


def assistant_event(text):
    return {"type": "assistant", "message": {"role": "assistant",
            "content": [{"type": "text", "text": text}]}}


def tool_use_event(call_id, command):
    return {"type": "assistant", "message": {"role": "assistant", "content": [
        {"type": "tool_use", "id": call_id, "name": "Bash",
         "input": {"command": command}}]}}


def tool_result_event(call_id, text):
    return {"type": "user", "message": {"role": "user", "content": [
        {"type": "tool_result", "tool_use_id": call_id,
         "content": [{"type": "text", "text": text}]}]}}


def run_task_phase(prompt):
    command = re.search(r"`([^`]+)`", prompt)
    command = command.group(1) if command else "printf noop"
    edit_value = re.search(r"edit_fact = (%s)" % UUID_RE, prompt).group(1)
    assistant_value = re.search(
        r"assistant_fact = (%s)" % UUID_RE, prompt).group(1)
    marks = {kind: str(uuid.uuid4()) for kind in ("START", "MID", "END")}
    hidden = str(uuid.uuid4())
    lines = ["MARK START %s" % marks["START"]]
    lines += ["line %04d" % i for i in range(1, 301)]
    lines.append("MARK MID %s" % marks["MID"])
    lines.append("HIDDEN TOOL %s" % hidden)
    lines += ["line %04d" % i for i in range(301, 601)]
    lines.append("MARK END %s" % marks["END"])
    write_command = "printf 'edit_fact = %s' > notes.txt" % edit_value
    with open("notes.txt", "w", encoding="utf-8") as handle:
        handle.write("edit_fact = %s" % edit_value)
    # Живой клиент в потоковом режиме не повторяет user input в stdout,
    # поэтому строка пользователя в журнал не попадает.
    emit(assistant_event("Выполняю шаги задачи."))
    emit(tool_use_event("toolu_long_1", command))
    emit(tool_result_event("toolu_long_1", "\n".join(lines)))
    emit(tool_use_event("toolu_write_1", write_command))
    emit(tool_result_event("toolu_write_1", ""))
    emit(assistant_event("assistant_fact = %s" % assistant_value))
    emit({"type": "result", "subtype": "success", "is_error": False,
          "result": "assistant_fact = %s" % assistant_value,
          "permission_denials": []})


def last_user_text(lines):
    question = ""
    for raw in lines:
        try:
            event = json.loads(raw)
        except ValueError:
            continue
        if event.get("type") != "user":
            continue
        content = (event.get("message") or {}).get("content")
        if isinstance(content, list):
            for block in content:
                if isinstance(block, dict) and block.get("type") == "text":
                    question = block.get("text", "")
    return question


def run_answer_phase(lines, question):
    keys = re.search(r"с ключами ([^.]+)\.", question)
    keys = [key.strip() for key in keys.group(1).split(",")] if keys else []
    corpus = "\n".join(lines)
    answer = {}
    for key in keys:
        found = re.search(r"%s = (%s)" % (re.escape(key), UUID_RE), corpus)
        if found:
            answer[key] = found.group(1)
            continue
        mark = {"mark_start": "MARK START", "mark_mid": "MARK MID",
                "mark_end": "MARK END",
                "hidden_tool_fact": "HIDDEN TOOL"}.get(key)
        if mark:
            found = re.search(r"%s (%s)" % (mark, UUID_RE), corpus)
            answer[key] = found.group(1) if found else None
            continue
        if key == "edit_path":
            answer[key] = "notes.txt" if "notes.txt" in corpus else None
            continue
        if key == "edit_line":
            found = re.search(r"(edit_fact = %s)" % UUID_RE, corpus)
            answer[key] = found.group(1) if found else None
            continue
        answer[key] = None
    payload = json.dumps(answer, ensure_ascii=False)
    emit(assistant_event(payload))
    emit({"type": "result", "subtype": "success", "is_error": False,
          "result": payload, "permission_denials": []})


def main():
    lines = sys.stdin.read().splitlines()
    if os.environ.get("FAKE_CLAUDE_REJECT_TOOLS") and any(
            '"type":"tool_use"' in line or '"type": "tool_use"' in line
            for line in lines):
        print("InputValidationError: исторический tool_use не принимается",
              file=sys.stderr)
        return 1
    # Фаза различается по последнему вопросу, а не по фразе из него:
    # исходный prompt законно попадает и в обратную историю.
    question = last_user_text(lines)
    if question.startswith("Без инструментов"):
        run_answer_phase(lines, question)
    else:
        run_task_phase("\n".join(lines))
    return 0


if __name__ == "__main__":
    sys.exit(main())
