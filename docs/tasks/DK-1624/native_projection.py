#!/usr/bin/env python3
"""Одноразовый форматтер native-проекции журнала Claude Code (DK-1624).

Обратный перенос Codex -> Claude проверялся двумя путями с разными исходами:
потоковый replay stream-json в полном потоке трижды терял новый
инструментальный факт Codex, а ручная проекция публичной истории в
native-журнал с продолжением через --resume вернула все факты. Модуль
повторяет ту ручную проекцию программно и только её, общего переводчика
форматов тут нет. Формат журнала внутренний для Claude Code 2.1.312,
стабильным import API он не объявляется.

Вход: seed-журнал новой синтетической сессии (одна пара
«Reply only RESUME_PROBE_READY.»), публичные stream-json записи истории без
конечного вопроса, ожидаемые UUID и каталог seed, подпись модели-получателя.
Выход: новый native UUID и файл <UUID>.jsonl рядом с seed-журналом. Файл
создаётся эксклюзивно, существующие журналы не заменяются и не дописываются.

Служебные записи и thinking не переносятся. Исторические message id, время
и модель не сохраняются: у записей новые uuid, время и модель-получатель.
Тексты, аргументы tool_use (включая raw_input и codex_type custom-инструментов
Codex) и tool_result с признаком is_error сохраняются как есть. Чужие сессии
не ищутся, config и auth клиента не читаются: модуль читает только два
переданных файла.

  python3 native_projection.py --seed SEED.jsonl --history HISTORY.jsonl \
      --seed-uuid UUID --cwd DIR --model MODEL
"""
import argparse
import datetime
import json
import os
import sys
import uuid
from pathlib import Path

SEED_PROMPT = "Reply only RESUME_PROBE_READY."
SEED_ANSWER = "RESUME_PROBE_READY"
SERVICE_TYPES = frozenset({"system", "summary", "result", "attachment"})
META_KEYS = ("version", "gitBranch", "userType", "entrypoint")


class ProjectionError(Exception):
    """Отказ проекции: неподходящий seed, история или занятое имя файла."""


def read_jsonl(path):
    """Строки JSONL-файла, пустые строки пропущены."""
    records = []
    for raw in Path(path).read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line:
            continue
        try:
            records.append(json.loads(line))
        except ValueError as exc:
            raise ProjectionError("%s: не JSON-строка: %s" % (path, exc))
    return records


def _plain_text(content):
    if isinstance(content, str):
        return content
    parts = []
    for block in content or []:
        if isinstance(block, dict) and block.get("type") == "text":
            parts.append(block.get("text", ""))
    return "\n".join(parts)


def _visible_blocks(content):
    """Блоки без thinking: служебные размышления модели в журнал не едут."""
    if content is None:
        return []
    if isinstance(content, str):
        return [{"type": "text", "text": content}]
    return [block for block in content
            if isinstance(block, dict) and block.get("type") != "thinking"]


def _has_visible_content(record):
    """Есть ли в записи разговора видимый блок.

    Клиент 2.1.312 пишет thinking и text одной реплики отдельными
    assistant-строками с одним message.id: thinking-only строка это не
    отдельный ответ, а невидимая часть того же.
    """
    if record.get("type") != "assistant":
        return True
    content = (record.get("message") or {}).get("content")
    return bool(_visible_blocks(content))


def load_seed(path, expected_uuid, expected_cwd):
    """Прочитать и проверить seed-журнал новой синтетической сессии.

    Проверки защищают чужие сессии от проекции: имя файла и sessionId
    совпадают с ожидаемым UUID, cwd один и равен ожидаемому, разговор это
    ровно одна пара с фиксированным вопросом и ответом-маркером.
    """
    records = read_jsonl(path)
    if not records:
        raise ProjectionError("seed-журнал %s пуст" % path)
    foreign = {record.get("sessionId") for record in records
               if record.get("sessionId")} - {expected_uuid}
    if Path(path).stem != expected_uuid or foreign:
        raise ProjectionError(
            "seed-журнал не совпадает с ожидаемым UUID %s" % expected_uuid)
    cwds = {record.get("cwd") for record in records if record.get("cwd")}
    if cwds != {expected_cwd}:
        raise ProjectionError(
            "каталог seed-журнала не совпадает с ожидаемым %s" % expected_cwd)
    conversation = [record for record in records
                    if record.get("type") in ("user", "assistant")
                    and _has_visible_content(record)]
    shapes = [record.get("type") for record in conversation]
    if shapes != ["user", "assistant"]:
        raise ProjectionError(
            "разговор seed-журнала не одна пара, а %s" % shapes)
    question = _plain_text(
        (conversation[0].get("message") or {}).get("content"))
    if question != SEED_PROMPT and not question.endswith(SEED_PROMPT):
        raise ProjectionError(
            "вопрос seed-журнала не заканчивается на %r" % SEED_PROMPT)
    answer_content = (conversation[1].get("message") or {}).get("content")
    if SEED_ANSWER not in _plain_text(answer_content):
        raise ProjectionError(
            "ответ seed-журнала не содержит %s" % SEED_ANSWER)
    meta = {key: conversation[0][key] for key in META_KEYS
            if key in conversation[0]}
    if "version" not in meta:
        raise ProjectionError("у первой записи seed-журнала нет поля version")
    return {
        # Точный вопрос без служебной приставки CLI к user-тексту.
        "question": SEED_PROMPT,
        "answer_blocks": _visible_blocks(answer_content),
        "meta": meta,
        "cwd": expected_cwd,
    }


def _assistant_message(content, model, record_uuid):
    blocks = content if isinstance(content, list) else [
        {"type": "text", "text": content}]
    has_tool_use = any(isinstance(block, dict)
                       and block.get("type") == "tool_use" for block in blocks)
    return {
        "id": "msg_" + record_uuid,
        "type": "message",
        "role": "assistant",
        "model": model,
        "content": content,
        "stop_reason": "tool_use" if has_tool_use else "end_turn",
        "stop_sequence": None,
        "usage": {"input_tokens": 0, "output_tokens": 0},
    }


def project(seed, history_records, model):
    """Собрать записи нового журнала из seed-пары и публичной истории.

    Возвращает (новый UUID сессии, записи). Родительская цепочка идёт
    последовательно, время записей растёт от текущего момента.
    """
    new_session = str(uuid.uuid4())
    base = datetime.datetime.now(datetime.timezone.utc)
    records = []

    def timestamp(index):
        moment = base + datetime.timedelta(seconds=index)
        return moment.isoformat(timespec="milliseconds").replace("+00:00", "Z")

    def emit(kind, content):
        record_uuid = str(uuid.uuid4())
        record = {
            "type": kind,
            "message": ({"role": "user", "content": content} if kind == "user"
                        else _assistant_message(content, model, record_uuid)),
            "uuid": record_uuid,
            "parentUuid": records[-1]["uuid"] if records else None,
            "isSidechain": False,
            "cwd": seed["cwd"],
            "sessionId": new_session,
            "timestamp": timestamp(len(records)),
        }
        record.update(seed["meta"])
        records.append(record)

    emit("user", [{"type": "text", "text": seed["question"]}])
    if seed["answer_blocks"]:
        emit("assistant", seed["answer_blocks"])
    for event in history_records:
        kind = event.get("type")
        if kind in SERVICE_TYPES:
            continue
        if kind not in ("user", "assistant"):
            raise ProjectionError(
                "в истории неизвестный тип записи %r" % kind)
        content = (event.get("message") or {}).get("content")
        if isinstance(content, list):
            content = _visible_blocks(content)
            if not content:
                continue
        if not content:
            continue
        emit(kind, content)
    return new_session, records


def write_journal(seed_path, new_session, records):
    """Записать новый журнал рядом с seed-журналом, эксклюзивным созданием."""
    target = Path(seed_path).parent / (new_session + ".jsonl")
    payload = "".join(json.dumps(record, ensure_ascii=False) + "\n"
                      for record in records)
    try:
        descriptor = os.open(target, os.O_CREAT | os.O_EXCL | os.O_WRONLY,
                             0o644)
    except FileExistsError:
        raise ProjectionError(
            "файл %s уже существует, журнал не заменяется" % target.name)
    with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
        handle.write(payload)
    return target


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Проекция публичной истории в native-журнал Claude Code")
    parser.add_argument("--seed", required=True,
                        help="seed-журнал новой синтетической сессии")
    parser.add_argument("--history", required=True,
                        help="публичные stream-json записи истории без "
                             "конечного вопроса")
    parser.add_argument("--seed-uuid", required=True,
                        help="ожидаемый UUID seed-сессии")
    parser.add_argument("--cwd", required=True,
                        help="ожидаемый каталог seed-сессии")
    parser.add_argument("--model", required=True,
                        help="подпись модели-получателя в записях assistant")
    args = parser.parse_args(argv)
    try:
        seed = load_seed(args.seed, args.seed_uuid, args.cwd)
        history = read_jsonl(args.history)
        new_session, records = project(seed, history, args.model)
        target = write_journal(args.seed, new_session, records)
    except ProjectionError as exc:
        print("проекция отказана: %s" % exc, file=sys.stderr)
        return 2
    print("native uuid: %s" % new_session)
    print("журнал: %s" % target.name)
    return 0


if __name__ == "__main__":
    sys.exit(main())
