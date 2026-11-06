#!/usr/bin/env python3
"""Фикстура теста: codex app-server по JSONL (DK-1624).

Отвечает на подмножество методов сценария без сети и модели. Импортированные
элементы копятся в файле состояния, поэтому второй процесс раунда видит их
после resume, как живой сервер с постоянным журналом. Поведение повторяет
наблюдения ручных проб: импортированные элементы не видны в thread/read и
thread/items/list, повторный inject добавляет дубли, сжатие сохраняет факты,
но теряет точные строки. Состояние лежит по FAKE_CODEX_STATE.
"""
import json
import os
import re
import sys

UUID_RE = "[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}"

DEFAULTS = {"thread_id": "th_fake_1", "items": [], "compacted": False}


def state_path():
    return os.environ.get("FAKE_CODEX_STATE") or os.path.join(
        os.path.sep, "tmp", "fake_codex_state_dk1624.json")


def load_state():
    try:
        with open(state_path(), encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return dict(DEFAULTS)


def save_state(state):
    with open(state_path(), "w", encoding="utf-8") as handle:
        json.dump(state, handle, ensure_ascii=False)


def send(obj):
    print(json.dumps(obj, ensure_ascii=False), flush=True)


def reply(rid, result):
    send({"id": rid, "result": result})


def corpus(state):
    texts = []
    for item in state["items"]:
        if item.get("type") == "message":
            for part in item.get("content", []):
                texts.append(part.get("text", ""))
        elif item.get("type") == "function_call":
            texts.append(item.get("arguments", ""))
        elif item.get("type") == "function_call_output":
            texts.append(item.get("output", ""))
    return "\n".join(texts)


def facts_answer(state, question):
    body = corpus(state)
    answer = {}
    for key in ("user_fact", "assistant_fact", "codex_fact"):
        found = re.search(r"%s = (%s)" % (key, UUID_RE), body)
        answer[key] = found.group(1) if found else None
    for key, mark in (("mark_start", "MARK START"), ("mark_mid", "MARK MID"),
                      ("mark_end", "MARK END")):
        found = re.search(r"%s (%s)" % (mark, UUID_RE), body)
        answer[key] = found.group(1) if found else None
    found = re.search(r"(edit_fact = %s)" % UUID_RE, body)
    answer["edit_line"] = found.group(1) if found else None
    answer["edit_path"] = "notes.txt" if "notes.txt" in body else None
    return json.dumps(answer, ensure_ascii=False)


def count_answer(state):
    count = sum(1 for item in state["items"]
                if item.get("type") == "function_call"
                and item.get("name") == "Bash")
    return str(count)


def exact_answer(state):
    if state["compacted"]:
        return json.dumps({"long_command": None, "mark_mid_line": None})
    command = None
    for item in state["items"]:
        if item.get("type") != "function_call":
            continue
        if "MARK START" in item.get("arguments", ""):
            try:
                command = json.loads(item["arguments"]).get("command")
            except ValueError:
                command = item.get("arguments")
    mid = re.search(r"(MARK MID %s)" % UUID_RE, corpus(state))
    return json.dumps({"long_command": command,
                       "mark_mid_line": mid.group(1) if mid else None},
                      ensure_ascii=False)


def handle_turn(state, question):
    if "codex_fact" in question:
        found = re.search(r"codex_fact = (%s)" % UUID_RE, question)
        text = ("Принято: codex_fact = %s" % found.group(1)
                if found else "Принято.")
    elif "сколько вызовов функции Bash" in question:
        text = count_answer(state)
    elif "long_command" in question:
        text = exact_answer(state)
    else:
        text = facts_answer(state, question)
    send({"method": "item/completed", "params": {
        "threadId": state["thread_id"],
        "item": {"type": "agentMessage", "text": text}}})
    send({"method": "turn/completed", "params": {
        "threadId": state["thread_id"], "turn": {"status": "completed"}}})


def main():
    state = load_state()
    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        message = json.loads(raw)
        rid = message.get("id")
        method = message.get("method")
        params = message.get("params") or {}
        if rid is None:
            continue
        if method == "initialize":
            reply(rid, {"serverInfo": {"name": "fake-codex", "version": "0"}})
        elif method == "thread/start":
            state["thread_id"] = "th_fake_1"
            save_state(state)
            reply(rid, {"thread": {"id": state["thread_id"]}})
        elif method == "thread/resume":
            reply(rid, {})
        elif method == "thread/inject_items":
            if os.environ.get("FAKE_CODEX_REJECT_INJECT"):
                send({"id": rid, "error": {"message":
                                           "inject запрещён фикстурой"}})
                continue
            state["items"] += params.get("items", [])
            save_state(state)
            reply(rid, {})
        elif method == "thread/read":
            reply(rid, {"thread": {"id": state["thread_id"]}, "turns": []})
        elif method == "thread/items/list":
            reply(rid, {"items": [], "nextCursor": None})
        elif method == "thread/compact/start":
            reply(rid, {})
            state["compacted"] = True
            save_state(state)
            # Живой сервер шлёт сжатие отдельным item/completed до
            # turn/completed, в самом turn/completed его нет.
            send({"method": "item/completed", "params": {
                "threadId": state["thread_id"],
                "item": {"type": "contextCompaction"}}})
            send({"method": "turn/completed", "params": {
                "threadId": state["thread_id"],
                "turn": {"status": "completed"}}})
        elif method == "turn/start":
            reply(rid, {})
            question = "\n".join(
                part.get("text", "") for part in params.get("input", []))
            handle_turn(state, question)
        else:
            send({"id": rid, "error": {"message": "метод не поддерживается: "
                                           + str(method)}})
    return 0


if __name__ == "__main__":
    sys.exit(main())
