#!/usr/bin/env python3
# Агентская приёмка DK-1606. Передача роли в одну секунду качала основную
# строку общего списка чатов: спор одинаковых секунд решал порядок обхода, и
# живой разговор пропадал из списка, вместо него читалась сырая строка.
# Сценарий собирает детерминированные журналы и проверяет, что список и геттер
# task/chat выбирают носителя одним правилом. Носители не запускаются.

import os
import re
import sys
import uuid
from datetime import datetime, timedelta

from continue_live_check import Stand, write_text

TASK = "XR-010"
WINDOW = "task-XR-010"
READS = 64


def need(cond, msg):
    if not cond:
        raise AssertionError(msg)


def transcript_path(s, sid):
    proj = re.sub(r"[^A-Za-z0-9-]", "-", s.proj)
    return os.path.realpath(
        os.path.join(s.home, ".claude", "projects", proj, sid + ".jsonl"))


def session_line(s, sid, path, stamp):
    return (
        "%s сессия %s задача %s проект demo дерево %s транскрипт %s "
        "источник заказ повод startup tmux %s панель - носитель -\n"
        % (stamp, sid, TASK, os.path.realpath(s.proj), path, WINDOW))


def rewrite(path, text):
    # файлы журналов должны содержать только строки сценария, поэтому
    # прежнее содержимое снимается целиком
    if os.path.exists(path):
        os.remove(path)
    write_text(path, text)


def snapshot(paths):
    data = {}
    for path in paths:
        with open(path, "rb") as fh:
            data[path] = fh.read()
    return data


def check_unchanged(before, label):
    for path, data in sorted(before.items()):
        with open(path, "rb") as fh:
            need(fh.read() == data,
                 "%s: после чтений изменился файл %s" % (label, path))


def read_list(s, raw_sids, label):
    # полный список не печатается, наблюдение сводится к счётчикам
    for n in range(READS):
        _, body = s.api("GET", "/api/projects/demo/chats?all=1&days=0")
        rows = body.get("chats", [])
        main_rows = [r for r in rows
                     if r.get("id") == TASK and r.get("main")]
        raw_rows = [r.get("id") for r in rows if r.get("id") in raw_sids]
        need(len(main_rows) == 1 and not raw_rows,
             "%s, чтение %d: ожидалась одна main-строка %s и ноль сырых "
             "строк из %s, наблюдение: main %d, сырые %s"
             % (label, n, TASK, sorted(raw_sids), len(main_rows), raw_rows))


def chat_of(body):
    chat = body.get("chat")
    if isinstance(chat, dict):
        chat = chat.get("sid") or chat.get("id")
    return chat


def check_getter(s, want, label):
    _, body = s.api("GET", "/api/projects/demo/tasks/%s/chat" % TASK)
    got = chat_of(body)
    need(got == want,
         "%s: геттер выбрал %r, ожидался %r" % (label, got, want))


def run(s):
    base = datetime.now() - timedelta(minutes=1)
    stamp = base.strftime("%Y-%m-%dT%H:%M:%S")
    later = (base + timedelta(seconds=1)).strftime("%Y-%m-%dT%H:%M:%S")

    old_sid = str(uuid.uuid4())
    fresh_sid = str(uuid.uuid4())
    s.seed_transcript(old_sid)
    s.seed_transcript(fresh_sid)
    old_path = transcript_path(s, old_sid)
    fresh_path = transcript_path(s, fresh_sid)
    for path in (old_path, fresh_path):
        need(os.path.isfile(path), "транскрипт не создан: %s" % path)

    main_log = os.path.join(s.home, ".devkit", "main-chat.log")
    sessions_log = os.path.join(s.home, ".devkit", "sessions.log")
    rewrite(main_log,
            "%s назначение %s разговор tmux-%s проект demo "
            "источник передача прежде -\n" % (stamp, TASK, WINDOW))
    rewrite(sessions_log,
            session_line(s, old_sid, old_path, stamp)
            + session_line(s, fresh_sid, fresh_path, stamp))

    before = snapshot([main_log, sessions_log, old_path, fresh_path])
    read_list(s, {old_sid, fresh_sid}, "случай 1")
    check_getter(s, fresh_sid, "случай 1")
    check_unchanged(before, "случай 1")
    print("случай 1: список дал одну main-строку %s в %d чтениях, "
          "геттер выбрал свежий SID %s" % (TASK, READS, fresh_sid))

    third_sid = str(uuid.uuid4())
    s.seed_transcript(third_sid)
    third_path = transcript_path(s, third_sid)
    need(os.path.isfile(third_path), "транскрипт не создан: %s" % third_path)
    rewrite(sessions_log,
            session_line(s, third_sid, third_path, later)
            + session_line(s, old_sid, old_path, stamp)
            + session_line(s, fresh_sid, fresh_path, stamp))

    before = snapshot([main_log, sessions_log, old_path, fresh_path,
                       third_path])
    read_list(s, {old_sid, fresh_sid, third_sid}, "случай 2")
    check_getter(s, third_sid, "случай 2")
    check_unchanged(before, "случай 2")
    print("случай 2: строка секундой позже стоит в файле первой, список дал "
          "одну main-строку %s в %d чтениях, геттер выбрал %s"
          % (TASK, READS, third_sid))


def main():
    s = Stand()
    ok = False
    try:
        s.setup()
        run(s)
        ok = True
        print("DK-1606 main_row_check: ok")
        return 0
    except Exception as exc:
        print("DK-1606 main_row_check: провал: %s" % exc, file=sys.stderr)
        print("артефакты: %s" % s.work, file=sys.stderr)
        return 1
    finally:
        s.teardown(ok)


if __name__ == "__main__":
    sys.exit(main())
