#!/usr/bin/env python3
"""Сценарий переноса чата Claude -> Codex -> Claude (DK-1624).

  python3 chat-port-check.py --workdir DIR --out DIR --claude-cmd CMD
      [--claude-model M] [--codex-cmd CMD] [--codex-model M] [--timeout S]

Каталоги и команды передаёт диспетчер: --workdir это родитель временных
проектов, --out принимает отчёт и сырые журналы, --claude-cmd это обёртка
действующей подписки. Выход 0: прогон дошёл до конца, исходы проверок лежат
в report.json и бывают отрицательными. Выход 1: какая-то проверка не
состоялась из-за сбоя сценария. Выход 2: ошибка запуска, аргументы или
каталоги.

Сценарий гонит живой разговор по цепочке: сеанс Claude с командой, длинным
выводом и правкой файла, импорт его журнала в Codex через inject_items,
опрос фактов, перезапуск app-server с resume, повторный импорт, сжатие и
обратная подача всей цепочки клиенту Claude. Факты это случайные значения,
которых нет в последней реплике и в файлах принимающей стороны, поэтому
ответ возможен только из перенесённой истории. Подтверждённые куски
протокола взяты из ручных проб диспетчера в файле задачи.
"""
import argparse
import datetime
import json
import os
import platform
import re
import shlex
import subprocess
import sys
import uuid
from pathlib import Path

import claude_client
import codex_client
import history_adapter

CLIENT_NAME = "devkit_dk1624_port_check"
EDIT_FILE = "notes.txt"
BLOCK_LINES = 300
Q_MAIN_KEYS = ("user_fact", "assistant_fact", "mark_start", "mark_mid",
               "mark_end", "edit_path", "edit_line")
FACT_KEYS = Q_MAIN_KEYS + ("codex_fact", "hidden_tool_fact")
Q_MAIN = ("Без инструментов, только из истории разговора выше, верни JSON-объект "
          "с ключами %s. Значения точно как в истории. Если значения нет, ставь "
          "null. Только JSON.") % ", ".join(Q_MAIN_KEYS)
Q_COUNT = ("Без инструментов ответь одним числом: сколько вызовов функции Bash "
           "в истории разговора выше?")
Q_EXACT = ("Без инструментов, только из истории, верни JSON-объект с ключами "
           "long_command, mark_mid_line. long_command это полная строка "
           "исторической команды с seq, mark_mid_line это полная строка вывода "
           "с MARK MID. Если точных строк нет или не видны целиком, ставь null. "
           "Только JSON.")
Q_REVERSE = ("Без инструментов, только из истории выше, верни JSON-объект с "
             "ключами %s. Значения точно как в истории. Если значения нет, "
             "ставь null. Только JSON.") % ", ".join(FACT_KEYS)
LONG_CMD = ("printf 'MARK START %%s\\n' \"$(uuidgen)\"; "
            "for i in $(seq 1 %d); do printf 'line %%04d\\n' \"$i\"; done; "
            "printf 'MARK MID %%s\\n' \"$(uuidgen)\"; "
            "printf 'HIDDEN TOOL %%s\\n' \"$(uuidgen)\"; "
            "for i in $(seq %d %d); do printf 'line %%04d\\n' \"$i\"; done; "
            "printf 'MARK END %%s\\n' \"$(uuidgen)\""
            ) % (BLOCK_LINES, BLOCK_LINES + 1, BLOCK_LINES * 2)
MARK_RE = re.compile(r"MARK (START|MID|END) (\S+)")
HIDDEN_RE = re.compile(r"HIDDEN TOOL (\S+)")


def parse_json_object(text):
    """Последний полный JSON-объект из ответа модели или None.

    Повтор исторических user-сообщений может вызвать несколько ответов за
    один прогон: ранние отвечают на старые вопросы, последний на конечный.
    """
    decoder = json.JSONDecoder()
    last = None
    pos = 0
    while True:
        start = text.find("{", pos)
        if start < 0:
            return last
        try:
            obj, end = decoder.raw_decode(text, start)
        except ValueError:
            pos = start + 1
            continue
        if isinstance(obj, dict):
            last = obj
        pos = end


def mask_env(command):
    return re.sub(r"([A-Za-z_][A-Za-z0-9_]*)=\S+", r"\1=***", command)


def fact_matches(key, value, facts):
    """Сверка одного факта. None в ответе это потеря, а не совпадение."""
    if value is None:
        return False
    if key == "edit_line":
        return facts["_edit_value"] in str(value)
    if key == "edit_path":
        # Модель вправе назвать полный путь: сверяется имя файла, а не форма.
        return os.path.basename(
            str(value).strip().rstrip("/")) == facts.get(key)
    if key == "codex_fact":
        return facts["codex_fact"] in str(value)
    return str(value).strip() == facts.get(key)


def match_facts(parsed, keys, facts):
    if not parsed:
        return {key: None for key in keys}
    return {key: fact_matches(key, parsed.get(key), facts)
            for key in keys}


def outcome(values):
    if values is None:
        return "не поддерживается"
    if any(value is False for value in values):
        return "потеряно"
    if values and all(value is True for value in values):
        return "перенесено"
    return "не проверено"


VERDICT_TO_VALUE = {"перенесено": True, "потеряно": False,
                    "не проверено": None, "не поддерживается": None}


class Scenario:
    def __init__(self, args):
        self.args = args
        self.out = Path(args.out)
        self.work = Path(args.workdir)
        self.facts = {}
        self.turns = []
        self.forward_items = []
        self.thread_id = None
        self.inject_ok = False
        self.report = {
            "task": "DK-1624",
            "started": datetime.datetime.now().isoformat(timespec="seconds"),
            "limits": {
                "payload_lines": BLOCK_LINES * 2 + 4,
                "marker_positions": [1, BLOCK_LINES + 2, BLOCK_LINES * 2 + 4],
                "turn_timeout_s": args.timeout,
                "history_items": None,
                "history_bytes": None,
                "long_output_bytes": None,
            },
            "setup": {
                "claude_cmd": mask_env(args.claude_cmd),
                "claude_model": args.claude_model or "",
                "codex_cmd": mask_env(args.codex_cmd),
                "codex_model": args.codex_model,
                "workdir": self.work.name,
                "python": platform.python_version(),
            },
            "facts": self.facts,
            "stages": {},
            "errors": [],
            "table": [],
        }

    def save(self):
        self.report["finished"] = datetime.datetime.now().isoformat(
            timespec="seconds")
        (self.out / "report.json").write_text(
            json.dumps(self.report, ensure_ascii=False, indent=2),
            encoding="utf-8")

    def stage(self, name, data):
        self.report["stages"][name] = data
        self.save()

    def fail(self, name, error):
        """Сбой сценария: отдельная строка в errors, стадия помечена."""
        self.report["errors"].append("%s: %s" % (name, error))
        existing = self.report["stages"].get(name)
        if isinstance(existing, dict):
            existing["scenario_error"] = str(error)
        else:
            self.report["stages"][name] = {"scenario_error": str(error)}
        self.save()

    def prepare_dirs(self):
        """Свежие каталоги или отказ до изменений.

        Прогон работает только в новых каталогах: существующие проекты и
        отчёт прошлого запуска не удаляются и не дописываются, повтор идёт
        другим --workdir и --out.
        """
        busy = [name for name in ("claude_proj", "codex_proj", "reverse_proj")
                if (self.work / name).exists()]
        if busy:
            raise FileExistsError(
                "в workdir уже есть %s, повторный прогон идёт в свежем "
                "каталоге" % ", ".join(busy))
        if (self.out / "report.json").exists():
            raise FileExistsError(
                "в out уже лежит report.json прошлого прогона, возьми свежий "
                "каталог")
        for name in ("claude_proj", "codex_proj", "reverse_proj"):
            (self.work / name).mkdir(parents=True)
        self.out.mkdir(parents=True, exist_ok=True)

    def probe_versions(self):
        versions = {}
        for label, argv in (
                ("claude", shlex.split(self.args.claude_cmd) + ["--version"]),
                ("codex", shlex.split(self.args.codex_cmd)[:1] + ["--version"])):
            try:
                done = subprocess.run(argv, capture_output=True, text=True,
                                      stdin=subprocess.DEVNULL, timeout=30)
                versions[label] = (done.stdout + done.stderr).strip()[:200]
            except (OSError, subprocess.TimeoutExpired) as exc:
                versions[label] = "не получена: %s" % exc
        self.stage("versions", versions)

    def run_claude_phase(self):
        values = {key: str(uuid.uuid4()) for key in
                  ("user_fact", "edit_value", "assistant_fact")}
        self.facts.update({
            "user_fact": values["user_fact"],
            "assistant_fact": values["assistant_fact"],
            "_edit_value": values["edit_value"],
            "edit_line": "edit_fact = %s" % values["edit_value"],
            "edit_path": EDIT_FILE,
        })
        prompt = (
            "Задача в этом каталоге, ровно три шага, без лишних действий.\n"
            "Факт для запоминания: user_fact = %s.\n"
            "1) Выполни одну команду Bash: `%s`\n"
            "2) Создай в текущем каталоге файл %s ровно со строкой "
            "edit_fact = %s и ничем больше.\n"
            "3) В конце ответь одной строкой вида assistant_fact = %s\n"
        ) % (values["user_fact"], LONG_CMD, EDIT_FILE, values["edit_value"],
             values["assistant_fact"])
        self.seed_prompt = prompt
        input_lines = history_adapter.exchange_to_claude_stream_lines(
            [{"kind": "text", "role": "user", "text": prompt}])
        (self.out / "claude_phase_in.jsonl").write_text(
            "".join(line + "\n" for line in input_lines), encoding="utf-8")
        argv = claude_client.build_argv(self.args.claude_cmd,
                                        self.args.claude_model,
                                        allowed_tools="Bash")
        run = claude_client.run(argv, cwd=str(self.work / "claude_proj"),
                                input_lines=input_lines,
                                timeout=self.args.timeout,
                                stdout_log=self.out / "claude_phase_out.jsonl",
                                stderr_log=self.out / "claude_phase_err.txt")
        result = run["result"] or {}
        events = run["events"]
        marks = {}
        for text in claude_client.tool_result_texts(events):
            for kind, value in MARK_RE.findall(text):
                marks["mark_%s" % kind.lower()] = value
            hidden = HIDDEN_RE.search(text)
            if hidden:
                self.facts["hidden_tool_fact"] = hidden.group(1)
            self.report["limits"]["long_output_bytes"] = max(
                self.report["limits"]["long_output_bytes"] or 0, len(text))
        self.facts.update(marks)
        assistant_texts = claude_client.event_texts(events, "assistant",
                                                    "assistant")
        found = re.search(r"assistant_fact\s*=\s*(\S+)",
                          "\n".join(assistant_texts))
        if found:
            self.facts["assistant_fact"] = found.group(1)
        edit_file = self.work / "claude_proj" / EDIT_FILE
        self.stage("claude_phase", {
            "exit": run["exit"],
            "timed_out": run["timed_out"],
            "subtype": result.get("subtype", ""),
            "is_error": result.get("is_error"),
            "permission_denials": result.get("permission_denials", []),
            "stderr": run["stderr"][:2000],
            "assistant_fact_found": bool(found),
            "marks_found": sorted(marks),
            "hidden_fact_found": "hidden_tool_fact" in self.facts,
            "edit_file_written": edit_file.exists(),
        })

    def build_items(self):
        lines = (self.out / "claude_phase_out.jsonl").read_text(
            encoding="utf-8", errors="replace").splitlines()
        items = history_adapter.claude_journal_to_codex_items(lines)
        restored = self.restore_seed_user_message(items)
        payload = json.dumps(items, ensure_ascii=False)
        self.report["limits"]["history_items"] = len(items)
        self.report["limits"]["history_bytes"] = len(payload)
        self.stage("history_items", {
            "count": len(items),
            "kinds": {kind: sum(1 for item in items if item["type"] == kind)
                      for kind in ("message", "function_call",
                                   "function_call_output")},
            "payload_bytes": len(payload),
            "seed_user_message_restored": restored,
        })
        return items

    def restore_seed_user_message(self, items):
        """Вернуть в историю исходную реплику пользователя, если её нет.

        stdout клиента в потоковом режиме не повторяет user input, а без
        реплики Codex не видит user_fact: это дефект сборки истории, а не
        ограничение приёмника. Точный prompt сохранён сценарием, реплика
        восстанавливается из него; если журнал реплику уже содержит,
        добавления нет.
        """
        needle = self.facts.get("user_fact", "")
        for item in items:
            if item.get("type") != "message" or item.get("role") != "user":
                continue
            if needle and needle in json.dumps(item, ensure_ascii=False):
                return False
        items.insert(0, {"type": "message", "role": "user",
                         "content": [{"type": "input_text",
                                      "text": self.seed_prompt}]})
        return True

    def visibility(self, server, thread_id):
        """Лента против контекста: видны ли импортированные элементы в API."""
        needle = self.facts.get("user_fact", "")
        try:
            thread = server.read_thread(thread_id)
            listing = server.list_items(thread_id)
        except codex_client.CodexError as exc:
            return {"error": str(exc)}
        return {
            "thread_read_shows_injected": needle in json.dumps(
                thread, ensure_ascii=False),
            "items_list_shows_injected": needle in json.dumps(
                listing, ensure_ascii=False),
            "items_list_next_cursor": listing.get("nextCursor"),
        }

    def ask(self, server, thread_id, question, tag):
        turn = server.run_turn(thread_id, question)
        self.turns.append({"tag": tag, "input": question,
                           "answer": turn["answer"]})
        return turn

    def codex_round(self, index, resume_id, actions):
        """Один процесс app-server: старт или resume, затем действия раунда.

        Возвращает id потока: он переживает процесс и нужен следующему
        раунду даже при ошибке в действиях.
        """
        server = codex_client.CodexAppServer(
            shlex.split(self.args.codex_cmd),
            self.out / ("codex%d_in.jsonl" % index),
            self.out / ("codex%d_out.jsonl" % index),
            self.out / ("codex%d_err.txt" % index),
            turn_timeout=self.args.timeout)
        thread_id = resume_id
        try:
            server.start()
            server.initialize(CLIENT_NAME)
            if resume_id is None:
                thread_id = server.start_thread(
                    cwd=str(self.work / "codex_proj"),
                    model=self.args.codex_model)
            else:
                server.resume_thread(resume_id)
            actions(server, thread_id)
        except (codex_client.CodexError, OSError) as exc:
            self.fail("codex_round%d" % index, exc)
        finally:
            server.stop()
        return thread_id

    def run_round1(self):
        stage = {}

        def actions(server, thread_id):
            try:
                server.inject_items(thread_id, self.forward_items)
                self.inject_ok = True
            except codex_client.CodexError as exc:
                stage["inject_accepted"] = False
                stage["inject_error"] = str(exc)
                self.stage("codex_round1", stage)
                return
            stage["inject_accepted"] = True
            stage["thread_id"] = thread_id
            stage["feed_visibility"] = self.visibility(server, thread_id)
            turn = self.ask(server, thread_id, Q_MAIN, "main")
            stage["question"] = Q_MAIN
            stage["answer_raw"] = turn["answer"]
            stage["turn_status"] = turn["status"]
            stage["turn_error"] = turn["error"]
            parsed = parse_json_object(turn["answer"])
            stage["parsed"] = parsed
            stage["match"] = match_facts(parsed, Q_MAIN_KEYS, self.facts)
            self.stage("codex_round1", stage)

        self.thread_id = self.codex_round(1, None, actions)

    def run_round2(self):
        if self.thread_id is None:
            self.stage("codex_round2", {
                "skipped": "первый раунд не дал thread_id"})
            return
        expected = sum(1 for item in self.forward_items
                       if item.get("type") == "function_call"
                       and item.get("name") == "Bash")
        stage = {"expected_bash_calls": expected}

        def actions(server, thread_id):
            turn = self.ask(server, thread_id, Q_MAIN, "restart_main")
            parsed = parse_json_object(turn["answer"])
            stage["restart_answer_raw"] = turn["answer"]
            stage["restart_match"] = match_facts(parsed, Q_MAIN_KEYS,
                                                 self.facts)
            try:
                server.inject_items(thread_id, self.forward_items)
                stage["reimport_accepted"] = True
            except codex_client.CodexError as exc:
                stage["reimport_accepted"] = False
                stage["reimport_error"] = str(exc)
            turn = self.ask(server, thread_id, Q_COUNT, "count")
            stage["count_answer_raw"] = turn["answer"]
            digits = re.findall(r"\d+", turn["answer"])
            stage["count_reported"] = int(digits[-1]) if digits else None
            if stage["count_reported"] is None:
                stage["duplicates"] = "не определено"
            elif stage["count_reported"] == expected:
                stage["duplicates"] = "нет"
            else:
                stage["duplicates"] = "да"
            turn = self.ask(server, thread_id, Q_EXACT, "exact_before")
            stage["exact_before"] = parse_json_object(turn["answer"])
            try:
                compact = server.compact_thread(thread_id)
                stage["compact_completed_raw"] = compact["completed"]
                stage["compaction_seen"] = compact["compaction_seen"]
            except codex_client.CodexError as exc:
                stage["compact_error"] = str(exc)
            turn = self.ask(server, thread_id, Q_EXACT, "exact_after")
            stage["exact_after"] = parse_json_object(turn["answer"])
            turn = self.ask(
                server, thread_id,
                "Запомни новый факт: codex_fact = %s. Без инструментов "
                "подтверди получение одной строкой со значением codex_fact."
                % self.facts["codex_fact"], "codex_fact")
            stage["codex_fact_answer_raw"] = turn["answer"]
            stage["codex_fact_echoed"] = (self.facts["codex_fact"]
                                          in turn["answer"])
            self.stage("codex_round2", stage)

        self.codex_round(2, self.thread_id, actions)

    def run_reverse(self):
        exchange = history_adapter.codex_items_to_exchange(self.forward_items)
        for turn in self.turns:
            exchange.append({"kind": "text", "role": "user",
                             "text": turn["input"]})
            if turn["answer"]:
                exchange.append({"kind": "text", "role": "assistant",
                                 "text": turn["answer"]})
        # История для native-проекции: та же цепочка без конечного вопроса,
        # вопрос задаётся потом обычным resume поверх спроецированного
        # журнала.
        (self.out / "native_history_in.jsonl").write_text(
            "".join(line + "\n" for line in
                    history_adapter.exchange_to_claude_stream_lines(exchange)),
            encoding="utf-8")
        attempts = []
        for tag, use_tools in (("with_tools", True), ("text_only", False)):
            steps = exchange if use_tools else [
                step for step in exchange if step["kind"] == "text"]
            lines = history_adapter.exchange_to_claude_stream_lines(
                steps + [{"kind": "text", "role": "user",
                          "text": Q_REVERSE}])
            (self.out / ("reverse_%s_in.jsonl" % tag)).write_text(
                "".join(line + "\n" for line in lines), encoding="utf-8")
            argv = claude_client.build_argv(self.args.claude_cmd,
                                            self.args.claude_model,
                                            allowed_tools=None,
                                            max_turns=1)
            run = claude_client.run(
                argv, cwd=str(self.work / "reverse_proj"),
                input_lines=lines, timeout=self.args.timeout,
                stdout_log=self.out / ("reverse_%s_out.jsonl" % tag),
                stderr_log=self.out / ("reverse_%s_err.txt" % tag))
            result = run["result"] or {}
            parsed = parse_json_object(str(result.get("result", "")))
            attempt = {
                "tag": tag,
                "exit": run["exit"],
                "timed_out": run["timed_out"],
                "subtype": result.get("subtype", ""),
                "is_error": result.get("is_error"),
                "permission_denials": result.get("permission_denials", []),
                "answer_raw": str(result.get("result", ""))[:4000],
                "stderr": run["stderr"][:2000],
                "parsed": parsed,
                "match": match_facts(parsed, FACT_KEYS, self.facts),
            }
            attempts.append(attempt)
            self.stage("reverse_%s" % tag, attempt)
            accepted = (run["exit"] == 0 and not result.get("is_error")
                        and parsed is not None)
            if accepted:
                break
        self.report["stages"]["reverse"] = {"attempts": attempts}
        self.save()

    def build_table(self):
        stages = self.report["stages"]
        round1 = stages.get("codex_round1", {})
        round2 = stages.get("codex_round2", {})
        visible = (round1.get("feed_visibility") or {}).get(
            "thread_read_shows_injected")
        feed = {True: "виден в thread/read",
                False: "нет в thread/read"}.get(visible, "не проверено")
        main = round1.get("match") or {}
        restart = round2.get("restart_match") or {}
        # Сохранность после перезапуска меряется только по фактам, которые
        # подтверждены до перезапуска: потерянное при самом импорте сюда не
        # входит, иначе дефект сборки истории выглядел бы потерей resume.
        preserved = [restart.get(key) for key in Q_MAIN_KEYS
                     if main.get(key) is True]
        if preserved:
            stages["restart_note"] = (
                "строка меряется по фактам, подтверждённым в первом раунде")
        rows = [
            ("claude->codex", "текст пользователя", feed,
             [main.get("user_fact")]),
            ("claude->codex", "текст ассистента", feed,
             [main.get("assistant_fact")]),
            ("claude->codex", "вызов инструмента: имя и аргументы", feed,
             [main.get("edit_path"), main.get("edit_line")]),
            ("claude->codex", "результат инструмента: длинный вывод", feed,
             [main.get("mark_start"), main.get("mark_mid"),
              main.get("mark_end")]),
            ("claude->codex", "те же факты после перезапуска app-server", feed,
             preserved),
        ]
        if "duplicates" in round2:
            verdict = {"нет": "перенесено", "да": "потеряно",
                       "не определено": "не проверено"}[round2["duplicates"]]
            rows.append(("claude->codex", "повторный импорт без дублей", feed,
                         [VERDICT_TO_VALUE[verdict]]))
        if "exact_after" in round2:
            before = round2.get("exact_before") or {}
            after = round2.get("exact_after") or {}
            if not round2.get("compaction_seen"):
                verdict = "не проверено"
                reason = round2.get("compact_error",
                                    "turn/completed без contextCompaction")
            elif after.get("long_command") or after.get("mark_mid_line"):
                verdict = "перенесено"
                reason = "точные строки пережили сжатие"
            elif before.get("long_command") or before.get("mark_mid_line"):
                verdict = "потеряно"
                reason = "до сжатия строки возвращались, после сжатия null"
            else:
                verdict = "не проверено"
                reason = "точных строк не было и до сжатия"
            rows.append(("claude->codex", "сжатие: факты против точных строк",
                         feed, [VERDICT_TO_VALUE[verdict]]))
            stages["compaction_note"] = reason
        attempts = stages.get("reverse", {}).get("attempts", [])
        if attempts:
            first, best = attempts[0], attempts[-1]
            match = best.get("match") or {}
            reverse_feed = "не проверено, транскрипт клиента вне эксперимента"
            rows.append(("codex->claude", "текст пользователя", reverse_feed,
                         [match.get("user_fact"), match.get("codex_fact")]))
            rows.append(("codex->claude", "текст ассистента", reverse_feed,
                         [match.get("assistant_fact")]))
            # Инструментальная история меряется только прогоном с ней.
            # Таймаут и нечитаемый ответ это непроверенный случай, а не
            # отказ клиента поддерживать формат.
            if first.get("timed_out"):
                tools_verdict = "не проверено"
                tools_reason = "таймаут прогона с инструментальной историей"
                tools_values = [None]
            elif first.get("exit") != 0 and first.get("parsed") is None:
                tools_verdict = "не поддерживается"
                tools_reason = "клиент отказал на входе с tool_use"
                tools_values = None
            elif first.get("parsed") is None:
                tools_verdict = "не проверено"
                tools_reason = "ответ без единого JSON-объекта"
                tools_values = [None]
            else:
                first_match = first.get("match") or {}
                tools_values = [first_match.get("hidden_tool_fact")]
                tools_verdict = outcome(tools_values)
                tools_reason = "чистая проба hidden_tool_fact"
            rows.append(("codex->claude", "история инструментов", reverse_feed,
                         None if tools_verdict == "не поддерживается"
                         else tools_values))
            stages["reverse_outcome_tools"] = tools_verdict
            stages["reverse_tools_reason"] = tools_reason
            if len(attempts) > 1:
                stages["reverse_fallback_note"] = (
                    "текстовые строки сняты fallback-прогоном без "
                    "инструментальной истории, она в нём теряется по самой "
                    "схеме fallback")
            stages["reverse_provenance_note"] = (
                "чистая проба инструментальной истории это hidden_tool_fact: "
                "значение печатается исторической командой и не спрашивается "
                "у Codex ни в одном turn, поэтому в обратной истории оно "
                "доступно только из tool_result; маркеры, edit_line, "
                "user_fact и assistant_fact повторяются в репликах и "
                "ответах Codex, перенос инструментов по ним не доказывается; "
                "native-инструменты Codex (custom_tool_call) сценарий не "
                "создаёт, их обратный перенос проверен ручными прогонами "
                "диспетчера и в полном потоке теряется")
        table = []
        for direction, kind, feed_value, values in rows:
            row_outcome = outcome(values)
            table.append({"direction": direction, "kind": kind,
                          "feed": feed_value, "model": row_outcome,
                          "values": values, "outcome": row_outcome})
        self.report["table"] = table

    def run(self):
        self.save()
        self.probe_versions()
        self.facts["codex_fact"] = str(uuid.uuid4())
        try:
            self.run_claude_phase()
        except OSError as exc:
            self.fail("claude_phase", exc)
            self.build_table()
            self.save()
            return
        self.forward_items = self.build_items()
        self.run_round1()
        self.run_round2()
        try:
            self.run_reverse()
        except OSError as exc:
            self.fail("reverse", exc)
        self.build_table()
        self.save()


def main():
    parser = argparse.ArgumentParser(
        description="Эксперимент переноса чата Claude -> Codex -> Claude")
    parser.add_argument("--workdir", required=True,
                        help="родительский каталог временных проектов")
    parser.add_argument("--out", required=True,
                        help="каталог отчёта и сырых журналов")
    parser.add_argument("--claude-cmd", required=True,
                        help="команда клиента Claude Code с обвязкой подписки")
    parser.add_argument("--claude-model", default=None,
                        help="модель клиента Claude Code, если нужна")
    parser.add_argument("--codex-cmd", default="codex app-server --stdio",
                        help="команда запуска codex app-server")
    parser.add_argument("--codex-model", default="gpt-6.1-sol",
                        help="модель Codex")
    parser.add_argument("--timeout", type=int, default=300,
                        help="таймаут одного прогона клиента и turn, секунды")
    args = parser.parse_args()
    if not args.claude_cmd.strip():
        parser.error("--claude-cmd пуст")
    scenario = Scenario(args)
    try:
        scenario.prepare_dirs()
    except OSError as exc:
        print("каталоги недоступны: %s" % exc, file=sys.stderr)
        return 2
    scenario.run()
    print("отчёт: %s" % (scenario.out / "report.json"))
    for row in scenario.report["table"]:
        print("[%s] %s: %s" % (row["direction"], row["kind"], row["outcome"]))
    return 1 if scenario.report["errors"] else 0


if __name__ == "__main__":
    sys.exit(main())
