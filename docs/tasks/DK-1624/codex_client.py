"""Клиент codex app-server: JSONL через stdin/stdout (DK-1624).

Сервер запускается субпроцессом с двумя каналами PIPE. PTY не нужен и даже
вреден: он режет длину строки, а payload импорта истории длинный. Ответ RPC
приходит строкой с тем же id, между ответами сервер сыплет уведомления, они
копятся в журнал событий и раздаются ожиданиям turn. Завершение turn видно
уведомлением turn/completed, ответ метода turn/start приходит раньше самих
событий и результата не несёт.

Каждая строка в обе стороны дописывается в отдельный файл журнала: сырые
журналы остаются в каталоге запуска и в git не едут, а разбор отказа без
них невозможен.
"""
import json
import subprocess
import threading


class CodexError(Exception):
    """Ответ error от сервера или разрыв протокола."""


class CodexAppServer:
    """Один процесс app-server: запуск, запросы, ожидание turn, остановка."""

    def __init__(self, argv, log_in, log_out, log_err, turn_timeout=300):
        self.argv = argv
        self.log_in = open(log_in, "a", encoding="utf-8")
        self.log_out = open(log_out, "a", encoding="utf-8")
        self.err = open(log_err, "ab")
        self.turn_timeout = turn_timeout
        self.proc = None
        self.events = []
        self._next_id = 1
        self._watchdog = None
        self._timed_out = False

    def start(self):
        self.proc = subprocess.Popen(
            self.argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=self.err, text=True, encoding="utf-8", bufsize=1)

    def stop(self):
        if self.proc is None:
            return
        try:
            self.proc.stdin.close()
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()
        for handle in (self.log_in, self.log_out, self.err):
            handle.close()
        self.proc = None

    def _send(self, obj):
        line = json.dumps(obj, ensure_ascii=False)
        self.proc.stdin.write(line + "\n")
        self.proc.stdin.flush()
        self.log_in.write(line + "\n")
        self.log_in.flush()

    def _read(self):
        line = self.proc.stdout.readline()
        if not line:
            if self._timed_out:
                raise CodexError("таймаут ожидания %s с истёк, процесс убит"
                                 % self.turn_timeout)
            raise CodexError("app-server закрыл stdout, код: %s"
                             % self.proc.poll())
        self.log_out.write(line)
        self.log_out.flush()
        return json.loads(line)

    def _arm_watchdog(self):
        self._disarm_watchdog()
        self._timed_out = False

        def _expire():
            self._timed_out = True
            self.proc.kill()

        self._watchdog = threading.Timer(self.turn_timeout, _expire)
        self._watchdog.daemon = True
        self._watchdog.start()

    def _disarm_watchdog(self):
        if self._watchdog is not None:
            self._watchdog.cancel()
            self._watchdog = None

    def request(self, method, params):
        rid = self._next_id
        self._next_id += 1
        self._send({"id": rid, "method": method, "params": params})
        self._arm_watchdog()
        try:
            while True:
                message = self._read()
                if message.get("id") == rid:
                    if "error" in message:
                        raise CodexError("%s: %s" % (method, message["error"]))
                    return message.get("result", {})
                if "id" not in message:
                    self.events.append(message)
        finally:
            self._disarm_watchdog()

    def notify(self, method, params):
        self._send({"method": method, "params": params})

    def initialize(self, client_name):
        self.request("initialize", {
            "clientInfo": {"name": client_name, "version": "0.1.0"},
            "capabilities": {"experimentalApi": True},
        })
        self.notify("initialized", {})

    def start_thread(self, cwd, model, sandbox="read-only",
                     approval_policy="never"):
        result = self.request("thread/start", {
            "cwd": cwd, "model": model, "sandbox": sandbox,
            "approvalPolicy": approval_policy,
        })
        return (result.get("thread") or {}).get("id", "")

    def inject_items(self, thread_id, items):
        self.request("thread/inject_items",
                     {"threadId": thread_id, "items": items})

    def resume_thread(self, thread_id, exclude_turns=True):
        self.request("thread/resume", {
            "threadId": thread_id, "excludeTurns": exclude_turns})

    def read_thread(self, thread_id):
        return self.request("thread/read", {
            "threadId": thread_id, "includeTurns": True})

    def list_items(self, thread_id, limit=100):
        return self.request("thread/items/list", {
            "threadId": thread_id, "limit": limit})

    def _wait_turn_completed(self, thread_id):
        """Дождаться turn/completed потока, остальные события оставить."""
        self._arm_watchdog()
        try:
            while True:
                for index, message in enumerate(self.events):
                    if message.get("method") != "turn/completed":
                        continue
                    params = message.get("params") or {}
                    if params.get("threadId") in (None, thread_id):
                        return self.events.pop(index)
                self.events.append(self._read())
        finally:
            self._disarm_watchdog()

    def run_turn(self, thread_id, input_text, effort="low"):
        """turn/start и ожидание завершения: ответ модели в agentMessage.

        Возвращает словарь с текстом ответа, элементами item/completed,
        сырым turn/completed и флагом ошибки. Ошибка turn не поднимается
        исключением: отказ модели это тоже исход эксперимента, а не сбой
        сценария.
        """
        self.request("turn/start", {
            "threadId": thread_id,
            "input": [{"type": "text", "text": input_text}],
            "effort": effort,
        })
        completed = self._wait_turn_completed(thread_id)
        texts, items = [], []
        for message in self.events:
            if message.get("method") != "item/completed":
                continue
            item = (message.get("params") or {}).get("item") or {}
            if item.get("type") == "agentMessage":
                texts.append(item.get("text", ""))
            items.append(item)
        self.events = []
        params = completed.get("params") or {}
        # Живой сервер кладёт статус и ошибку в params.turn, плоская форма
        # params.status осталась запасным чтением.
        turn = params.get("turn") or {}
        return {
            "answer": "\n".join(texts).strip(),
            "items": items,
            "completed": completed,
            "status": turn.get("status") or params.get("status", ""),
            "error": turn.get("error", params.get("error")),
        }

    def compact_thread(self, thread_id):
        """compact/start; возвращает turn/completed и признак сжатия.

        Событие contextCompaction живой сервер присылает отдельным
        item/completed, а не внутри turn/completed, поэтому признак ищется в
        обоих местах.
        """
        self.request("thread/compact/start", {"threadId": thread_id})
        completed = self._wait_turn_completed(thread_id)
        seen = "contextCompaction" in json.dumps(completed, ensure_ascii=False)
        for message in self.events:
            if message.get("method") != "item/completed":
                continue
            item = (message.get("params") or {}).get("item") or {}
            if item.get("type") == "contextCompaction":
                seen = True
        self.events = []
        return {"completed": completed, "compaction_seen": seen}
