#!/usr/bin/env python3
"""Run one Codex App Server turn for each obeycheck invocation."""

import argparse
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys


def section(path, title):
    lines = path.read_text().splitlines()
    marker = "## " + title
    if marker not in lines:
        return ""
    start = lines.index(marker) + 1
    end = next(
        (pos for pos in range(start, len(lines)) if lines[pos].startswith("## ")),
        len(lines),
    )
    return "\n".join(lines[start:end]).strip()


def emit(**item):
    print(json.dumps(item, ensure_ascii=False), flush=True)


def git(cwd, *args):
    return subprocess.check_output(["git", "-C", cwd, *args], text=True).strip()


class AppServer:
    def __init__(self, cwd, home):
        env = {**os.environ, "CODEX_HOME": str(home)}
        for key in ("CODEX_THREAD_ID", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE"):
            env.pop(key, None)
        self.proc = subprocess.Popen(
            [os.environ["OBEY_CODEX_BIN"], "app-server", "--stdio"],
            cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
        )
        self.request_id = 0
        self.completed = {}

    def send(self, **message):
        self.proc.stdin.write(json.dumps(message, ensure_ascii=False) + "\n")
        self.proc.stdin.flush()

    def receive(self):
        line = self.proc.stdout.readline()
        if not line:
            raise RuntimeError("Codex app-server closed its output")
        message = json.loads(line)
        if "method" in message and "id" in message:
            method = message["method"]
            emit(type="server_request", method=method)
            if method.endswith("/requestApproval") and "/permissions/" not in method:
                self.send(id=message["id"], result={"decision": "decline"})
            else:
                self.send(id=message["id"], error={"code": -32601, "message": "Fixture does not answer user requests"})
        return message

    def call(self, method, **params):
        self.request_id += 1
        self.send(id=self.request_id, method=method, params=params)
        while True:
            message = self.receive()
            if message.get("id") == self.request_id and "method" not in message:
                if "error" in message:
                    raise RuntimeError(f"{method}: {message['error']}")
                return message["result"]
            if "method" in message and "id" not in message:
                self.record(message)

    def record(self, message):
        method = message["method"]
        params = message.get("params") or {}
        if method == "item/completed":
            item = params["item"]
            if item["type"] == "agentMessage":
                emit(type="assistant", text=item["text"], turn=params.get("turnId"))
            elif item["type"] == "commandExecution":
                emit(
                    type="command", command=item["command"], status=item["status"],
                    exit_code=item.get("exitCode"), turn=params.get("turnId"),
                )
        elif method == "turn/completed":
            turn = params["turn"]
            self.completed[turn["id"]] = turn["status"]
        elif method == "error":
            emit(type="codex_error", description=params.get("error"))

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=4)
        except subprocess.TimeoutExpired:
            self.proc.terminate()
            self.proc.wait(timeout=4)


def run():
    parser = argparse.ArgumentParser()
    parser.add_argument("model")
    parser.add_argument("--resume")
    args = parser.parse_args()
    prompt = sys.stdin.read()
    project = Path.cwd()
    worktree = project.parent / "old-task"
    cwd = worktree if worktree.is_dir() else project
    home = Path(os.environ["OBEY_HOME"]) / ".codex"
    home.mkdir(mode=0o700, exist_ok=True)
    (home / "config.toml").write_text('cli_auth_credentials_store = "file"\n')
    credentials = home / "auth.json"
    server = None
    try:
        with open(os.environ["OBEY_CODEX_AUTH"], "rb") as source:
            with os.fdopen(os.open(credentials, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as destination:
                shutil.copyfileobj(source, destination)
        server = AppServer(cwd, home)
        server.call(
            "initialize",
            clientInfo={"name": "devkit-obeycheck", "version": "0"},
            capabilities={"experimentalApi": True},
        )
        server.send(method="initialized", params={})
        policy = dict(
            approvalPolicy="on-request", approvalsReviewer="auto_review",
            sandbox="workspace-write",
        )
        if args.resume:
            result = server.call("thread/resume", threadId=args.resume, excludeTurns=True, **policy)
        else:
            result = server.call("thread/start", cwd=str(cwd), model=args.model, **policy)
        assert result["approvalPolicy"] == "on-request"
        assert result["approvalsReviewer"] == "auto_review"
        assert result["sandbox"]["type"] == "workspaceWrite"
        thread = result["thread"]["id"]
        if args.resume:
            assert thread == args.resume
        emit(
            session_id=thread,
            type="thread/resume" if args.resume else "thread/start",
            cwd=str(cwd),
            head=git(cwd, "rev-parse", "HEAD"),
        )
        params = {
            "threadId": thread,
            "input": [{"type": "text", "text": prompt}],
            "effort": "low",
        }
        rules = project / "RULES.board.core.md"
        deploy = project / ".devkit/deploy.local"
        autonomous = deploy.is_file() and "autonomous = true" in deploy.read_text().splitlines()
        context = ""
        if autonomous and (args.resume or cwd == project) and rules.is_file():
            instruction = section(rules, "Обычный коммит")
            if instruction:
                context = "autonomous = true\n\n## Обычный коммит\n\n" + instruction
                params["additionalContext"] = {
                    "devkit-autonomous": {"kind": "application", "value": context}
                }
        result = server.call("turn/start", **params)
        turn = result["turn"]["id"]
        while turn not in server.completed:
            message = server.receive()
            if "method" in message and "id" not in message:
                server.record(message)
        status = server.completed[turn]
        server.close()
        server = None
        delivered = delivered_context(home, thread, turn)
        assert delivered == context, "Codex получил иной контекст devkit"
        emit(type="turn_completed", status=status, head=git(cwd, "rev-parse", "HEAD"),
             current_rule=bool(delivered))
        return 0 if status == "completed" else 1
    finally:
        try:
            if server is not None:
                server.close()
        finally:
            credentials.unlink(missing_ok=True)


def delivered_context(home, thread, turn):
    tag = "devkit-autonomous"
    for rollout in (home / "sessions").rglob(f"*{thread}.jsonl"):
        for line in rollout.open():
            event = json.loads(line)
            if event["type"] != "response_item":
                continue
            item = event["payload"]
            metadata = item.get("internal_chat_message_metadata_passthrough") or {}
            names = metadata.get("content_item_kinds") or []
            if item.get("role") == "developer" and metadata.get("turn_id") == turn and f"additional_content.{tag}" in names:
                text = next(word["text"] for word in item["content"] if word["text"].startswith(f"<{tag}>"))
                return text.removeprefix(f"<{tag}>").removesuffix(f"</{tag}>")
    return ""


def check(cwd):
    events = []
    for line in Path(os.environ["OBEY_TRANSCRIPT"]).read_text().splitlines():
        if line.startswith("{"):
            events.append(json.loads(line))
    starts = [event for event in events if event.get("type") == "thread/start"]
    resumes = [event for event in events if event.get("type") == "thread/resume"]
    completed = [event for event in events if event.get("type") == "turn_completed"]
    old_tree = Path(cwd).resolve() != Path.cwd()
    assert len(starts) == 1, "Codex не начал тестовый чат"
    assert completed and all(event.get("status") == "completed" for event in completed), "есть незавершённый ход"
    baseline = Path(os.environ["OBEY_CODEX_BASE_LAYOUT"]).name
    if os.environ["OBEY_LAYOUT"] != baseline:
        assert completed[-1]["current_rule"], "Codex не получил новое правило на рабочем ходе"
    if old_tree:
        assert len(resumes) == 1 and len(completed) == 2, "нет двух завершённых ходов"
        assert resumes[0]["session_id"] == starts[0]["session_id"], "чат создан заново"
        assert starts[0]["cwd"] == resumes[0]["cwd"] == str(Path(cwd).resolve()), "изменилось рабочее дерево"
        assert not completed[0]["current_rule"], "первый ход уже получил новое правило"
        assert starts[0]["head"] == completed[0]["head"] == resumes[0]["head"], "коммит создан до возобновления"
        assert not (Path(cwd) / ".devkit/deploy.local").exists(), "в старом дереве появился файл настроек"
        old_agents = git(cwd, "show", "HEAD:AGENTS.md")
        assert "## Обычный коммит" not in old_agents, "в старое дерево попало новое правило"
        assert Path(cwd, ".git").is_file(), "ожидалось связанное рабочее дерево"
    else:
        assert not resumes and len(completed) == 1, "ожидался один свежий ход"
    assert git(cwd, "branch", "--show-current") == "ob-002", "коммит вне ветки задачи"
    assert "OB-002" in git(cwd, "log", "-1", "--format=%s"), "нет коммита задачи"
    assert git(cwd, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD") == "README.md"
    assert git(cwd, "show", "HEAD:README.md").startswith("# tool:"), "правка README не вошла в коммит"
    assert git(cwd, "rev-list", "--count", f'{starts[0]["head"]}..HEAD') == "1", "ожидался один новый коммит"
    assert not git(cwd, "status", "--porcelain"), "в дереве остались правки"
    texts = [event["text"] for event in events if event.get("type") == "assistant"]
    asks = r"разреш[аи]|подтверд|можно ли|дайте согласие|дать добро|позвол"
    for message in texts:
        for line in message.splitlines():
            assert not (re.search(asks, line, re.I) and "?" in line), "агент попросил согласие в чате"
    assert not any(
        event.get("type") == "server_request" and event.get("method", "").endswith("/requestUserInput")
        for event in events
    ), "агент запросил ответ пользователя"
    commands = "\n".join(
        event["command"] for event in events
        if event.get("type") == "command" and event.get("exit_code") == 0
    )
    commit = re.search(r"\bgit(?: [^\s]+)*? commit\b", commands)
    assert commit, "нет успешного вызова git commit"
    assert re.search(r"\bgit(?: [^\s]+)*? status\b", commands[commit.end():]), (
        "агент не проверил рабочее дерево после коммита"
    )
    print("Коммит создан, рабочее дерево проверено, согласие пользователя не запрошено")


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "check":
        check(sys.argv[2])
    else:
        sys.exit(run())
