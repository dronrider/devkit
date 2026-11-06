"""Запуск клиента Claude Code в потоковом режиме (DK-1624).

Клиент зовётся одной командой в режиме -p: история подаётся строками
stream-json в stdin, ответ приходит событиями stream-json в stdout, там же
последняя строка result с итогом запуска. Пустое значение --tools отключает
инструменты, поэтому исторические вызовы не исполняются повторно, а ответ
по истории возможен только из переданного контекста.
"""
import json
import os
import shlex
import signal
import subprocess


def build_argv(command, model=None, allowed_tools=None, max_turns=None):
    """Команда-обёртка клиента -> argv с потоковыми ключами режима -p."""
    argv = shlex.split(command) + [
        "-p", "--input-format", "stream-json",
        "--output-format", "stream-json", "--verbose",
    ]
    if model:
        argv += ["--model", model]
    if allowed_tools is None:
        argv += ["--tools", ""]
    else:
        argv += ["--allowedTools", allowed_tools]
    if max_turns:
        argv += ["--max-turns", str(max_turns)]
    return argv


def kill_group(proc):
    """Убить группу запущенного клиента со всеми его потомками.

    Команда клиента это обёртка подписки, под ней живёт настоящий клиент.
    Убийство только обёртки оставляет потомка держать PIPE, и последующее
    чтение ждёт без предела. Группа своя: клиент запускается в отдельной
    сессии, чужие процессы рубеж не задевает.
    """
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        proc.kill()


def drain(proc, wait=15):
    """Добрать вывод после остановки группы с ограниченным ожиданием."""
    try:
        return proc.communicate(timeout=wait)
    except subprocess.TimeoutExpired:
        for stream in (proc.stdin, proc.stdout, proc.stderr):
            if stream and not stream.closed:
                stream.close()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            pass
        return b"", b""


def run(argv, cwd, input_lines, timeout, stdout_log, stderr_log):
    """Прогон клиента с историей в stdin. Возвращает разбор его журнала.

    Таймаут не поднимается исключением: просроченный запуск это исход
    проверки, он попадает в отчёт тем же словарём, что и успешный. При
    таймауте останавливается вся группа клиента, вывод добирается с
    ограниченным ожиданием.
    """
    payload = "".join(line + "\n" for line in input_lines)
    timed_out = False
    with open(stdout_log, "wb") as out_log, open(stderr_log, "ab") as err_log:
        proc = subprocess.Popen(argv, cwd=cwd, stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE,
                                start_new_session=True)
        try:
            out, err = proc.communicate(payload.encode("utf-8"),
                                        timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            kill_group(proc)
            out, err = drain(proc)
        out_log.write(out)
        err_log.write(err)
    events, result = [], {}
    for raw in out.decode("utf-8", "replace").splitlines():
        line = raw.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except ValueError:
            events.append({"type": "unparsed", "raw": line})
            continue
        if event.get("type") == "result":
            result = event
        else:
            events.append(event)
    return {
        "exit": proc.returncode,
        "timed_out": timed_out,
        "events": events,
        "result": result,
        "stderr": err.decode("utf-8", "replace"),
    }


def event_texts(events, kind, role):
    """Тексты сообщений роли из событий журнала, в порядке журнала."""
    texts = []
    for event in events:
        if event.get("type") != kind:
            continue
        message = event.get("message") or {}
        if message.get("role") != role:
            continue
        content = message.get("content")
        if isinstance(content, str):
            content = [{"type": "text", "text": content}]
        for block in content or []:
            if isinstance(block, dict) and block.get("type") == "text":
                texts.append(block.get("text", ""))
    return texts


def tool_result_texts(events):
    """Все тексты результатов инструментов из журнала, склеенные по блокам."""
    texts = []
    for event in events:
        if event.get("type") != "user":
            continue
        content = (event.get("message") or {}).get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if isinstance(block, dict) and block.get("type") == "tool_result":
                inner = block.get("content")
                if isinstance(inner, str):
                    texts.append(inner)
                elif isinstance(inner, list):
                    texts.append("\n".join(
                        part.get("text", "") for part in inner
                        if isinstance(part, dict)))
    return texts
