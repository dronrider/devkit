#!/usr/bin/env python3
"""Разбор входа хуков: таблица разборщиков по имени протокола харнеса.

Тело проверки одно на все инструменты, меняется только форма события на stdin,
поэтому своего разбора проверки не носят, а зовут отсюда:

  hookio.write_event(protocol)     фрагменты записи: путь и записанные куски
  hookio.session_event(protocol)   событие сессии для уведомителя
  hookio.tool_event(protocol)      завершённый ход инструмента для подхвата реплики
  hookio.start_event(protocol)     старт сессии для реестра чатов
  hookio.agent_event(protocol)     фоновый субагент для сторожа завершений
  hookio.reply(protocol)           канал находки: чем сказать, что что-то не так
  hookio.context(protocol)         канал добавки: чем сказать без рамки провала
  hookio.memory_index(protocol)    хвост пути индекса памяти из профиля
  hookio.toml_at(path)             конфиг проверки разобранным Doc и причина
  hookio.append_capped(path, line) строка в машинный журнал хука с обрезкой
  hookio.tree_root(cwd)            дерево работы: ближайший предок с .git

Имя протокола приходит аргументом `--hook <протокол>`; голый `--hook` это
claude-code, иначе команды, прописанные в settings.json на машинах, сломались
бы об обновление devkit. Протокол без образцов в hooks/testdata/<протокол>/ в
таблицу не принимается: форма события живёт на стороне инструмента, и снятый с
неё живьём JSON это единственное, чем разбор проверяется.
"""
import collections
import json
import os
import re
import sys

DEFAULT = "claude-code"

# Машинный журнал хука (~/.devkit/notify.log у уведомителя, ~/.devkit/chat-in.log
# у подхвата) растёт от каждой сессии на машине, и обрезка у него одна на
# всех: файл больше предела режется до последних строк.
LOG_LIMIT = 100 * 1024
LOG_KEEP = 500

# Оси событий сессии, имена те же, что в `[hooks] events` профиля.
NOTIFY = "notify"
TURN_DONE = "turn-done"
# Ось хода, кончившегося API-ошибкой, а не штатным стопом: часть сетевых
# обрывов харнес ретраит сам, недокументированно и без участия devkit (DK-172,
# docs/tasks/DK-172.md), и сюда доезжает только ход, у которого эти попытки
# исчерпаны или ошибка неретраибельная. В повод кладётся тип ошибки из события
# (server_error, rate_limit и прочие значения матчера StopFailure).
TURN_FAILED = "turn-failed"
SUBAGENT_DONE = "subagent-done"
PROMPT_SUBMIT = "prompt-submit"
# Ось завершённого хода инструмента, ею живёт доставка реплики в идущий виток.
TOOL_DONE = "tool-done"
# Ось рождения сессии, ею живёт реестр чатов задачи.
SESSION_START = "session-start"
# Ось запуска фонового субагента: ход инструмента делегирования, вернувший
# запущенную работу вместо отчёта. Своей строки в профиле харнеса у неё нет, она
# снимается с той же оси tool-done, что и остальные ходы инструментов.
AGENT_LAUNCHED = "agent-launched"
# Имя инструмента делегирования и признак фонового запуска в его ответе.
AGENT_TOOL = "Agent"
AGENT_ASYNC = "async_launched"

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Фрагменты записи: путь файла и куски текста, которые в него уехали. Проверки
# смотрят именно куски, а не файл целиком, поэтому чужой существующий текст не
# всплывает.
Write = collections.namedtuple("Write", "path chunks")
# Событие сессии в общих полях: ось, повод (у оси notify), сессия, рабочее
# дерево, транскрипт, текст события и роль субагента.
Session = collections.namedtuple("Session", "kind reason session cwd transcript message agent")
# Завершённый ход инструмента: сессия целиком, дерево хода, имя инструмента и
# роль субагента, если ход шёл под ним. ID тут не режется до восьми символов, в
# отличие от события сессии: уведомителю он нужен подписью в баннере, а
# подхвату сверкой с именем витка, и половина UUID такой сверки не выдержит.
Tool = collections.namedtuple("Tool", "session cwd tool agent")
# Рождение сессии: ID целиком, дерево старта, транскрипт и повод старта
# (startup, resume, clear, compact). ID тут тоже не режется: реестр чатов
# сводит запись с файлом транскрипта, а тот назван полным ID.
Start = collections.namedtuple("Start", "session cwd transcript source")
# Фоновая работа сессии из перечня события: субагент или команда оболочки. Пока
# работа в перечне, харнес считает её незакрытой.
Job = collections.namedtuple("Job", "id kind status description")
# Фоновый субагент глазами сторожа завершений: ось события, сессия, дерево,
# транскрипт, ID работы, роль, чем названа, куда сложен отчёт, последняя реплика
# и перечень фоновых работ сессии на этот момент. Признак active это отметка
# харнеса о том, что ход уже продолжен стоп-хуком. Поле, которого событие не
# несёт, приходит пустым: у конца хода нет ни ID работы, ни роли.
Agent = collections.namedtuple(
    "Agent", "kind session cwd transcript agent_id agent_type description output message jobs active")


class Unknown(Exception):
    """Протокола нет в таблице: разбирать вход нечем."""


class BadEvent(Exception):
    """Вход не похож на событие. Хук на этом не падает, а уходит нулём."""


class Reply(object):
    """Канал ответа проверки. У claude-code это stderr с выходом 2: такой ответ
    харнес отдаёт агенту фидбеком, и тот переписывает сам."""

    def __init__(self, code, stream=None):
        self.code = code
        self.stream = stream

    def found(self, text):
        (self.stream or sys.stderr).write(text)
        return self.code


class Context(object):
    """Канал добавки контекста. Хук печатает на stdout запись JSON и выходит
    нулём, а харнес привешивает текст к ходу инструмента без рамки провала: ход
    остаётся удачным, и повторять его агент не идёт. Тем и отличается от
    находки, у которой рамка «поправь то, что сделал» стоит на любом ходе, хоть
    на записи файла, хоть на `git push`."""

    def __init__(self, event, stream=None):
        self.code = 0
        self.event = event
        self.stream = stream

    def say(self, text):
        out = self.stream or sys.stdout
        json.dump({"hookSpecificOutput": {"hookEventName": self.event,
                                          "additionalContext": text}},
                  out, ensure_ascii=False)
        out.write("\n")
        return self.code


def text_of(value):
    return value if isinstance(value, str) else ""


def claude_code_write(event):
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return None
    path = text_of(ti.get("file_path")) or text_of(ti.get("notebook_path"))
    chunks = [text_of(ti.get(k)) for k in ("new_string", "content", "new_source")]
    for e in ti.get("edits") or []:
        if isinstance(e, dict):
            chunks.append(text_of(e.get("new_string")))
    return Write(path, [c for c in chunks if c])


CLAUDE_CODE_KINDS = {
    "Notification": NOTIFY,
    "Stop": TURN_DONE,
    "StopFailure": TURN_FAILED,
    "SubagentStop": SUBAGENT_DONE,
    "UserPromptSubmit": PROMPT_SUBMIT,
}


def claude_code_session(event):
    kind = CLAUDE_CODE_KINDS.get(text_of(event.get("hook_event_name")))
    if not kind:
        return None
    # Текст события лежит в разных полях: у Notification это message, у
    # остальных ответ модели последней репликой.
    message = event.get("message") if kind == NOTIFY else event.get("last_assistant_message")
    # Повод остаётся как пришёл: разбирается он уже уведомителем, и поле не той
    # формы должно доехать до него, а не потеряться тут. У упавшего хода поводом
    # лежит тип API-ошибки, у остальных осей повод несёт notification_type.
    reason = event.get("error") if kind == TURN_FAILED else event.get("notification_type")
    return Session(kind=kind,
                   reason=reason or "",
                   session=str(event.get("session_id") or "-")[:8],
                   cwd=text_of(event.get("cwd")),
                   transcript=text_of(event.get("transcript_path")),
                   message=message,
                   agent=event.get("agent_type"))


def claude_code_tool(event):
    """Завершённый ход инструмента. Роль лежит в событии только у хода под
    субагентом, у хода самой сессии её нет вовсе, и пустое место тут значит
    именно это, а не потерянное поле."""
    if text_of(event.get("hook_event_name")) != "PostToolUse":
        return None
    return Tool(session=text_of(event.get("session_id")),
                cwd=text_of(event.get("cwd")),
                tool=text_of(event.get("tool_name")),
                agent=event.get("agent_type"))


def claude_code_start(event):
    """Рождение сессии. None значит, что событие не про старт: хук реестра
    стоит на своём событии, но чужой JSON до него всё равно доходит на стенде
    и в ручной проверке."""
    if text_of(event.get("hook_event_name")) != "SessionStart":
        return None
    return Start(session=text_of(event.get("session_id")),
                 cwd=text_of(event.get("cwd")),
                 transcript=text_of(event.get("transcript_path")),
                 source=text_of(event.get("source")))


# Поле ответа инструмента делегирования. Ответ приходит не объектом, а строкой
# с питоньим репром словаря ('agentId': '...'), и разобрать его json'ом нельзя:
# там одинарные кавычки и True. Форма эта на стороне харнеса, поэтому нужное
# снимается регуляркой, а объект, если он однажды придёт объектом, читается
# ключом.
def response_field(response, key):
    if isinstance(response, dict):
        return text_of(response.get(key))
    if not isinstance(response, str):
        return ""
    m = re.search(r"'%s':\s*'([^']*)'" % re.escape(key), response)
    return m.group(1) if m else ""


def claude_code_jobs(event):
    """Фоновые работы сессии из события: субагенты и команды оболочки, которые
    харнес считает незакрытыми."""
    out = []
    for item in event.get("background_tasks") or []:
        if isinstance(item, dict):
            out.append(Job(text_of(item.get("id")), text_of(item.get("type")),
                           text_of(item.get("status")), text_of(item.get("description"))))
    return tuple(out)


def claude_code_agent(event):
    """Событие про фонового субагента: его запуск, его конец либо конец хода
    сессии с перечнем незакрытых работ. None значит, что сторожу тут смотреть
    нечего, и хук на таком входе уходит нулём."""
    name = text_of(event.get("hook_event_name"))
    kind = {"SubagentStop": SUBAGENT_DONE, "Stop": TURN_DONE}.get(name)
    if name == "PostToolUse":
        if text_of(event.get("tool_name")) != AGENT_TOOL:
            return None
        if response_field(event.get("tool_response"), "status") != AGENT_ASYNC:
            # Синхронный субагент отчитывается ходом инструмента, и терять его
            # сессии негде: сторожить тут нечего.
            return None
        kind = AGENT_LAUNCHED
    elif kind is None:
        return None
    ti = event.get("tool_input") if isinstance(event.get("tool_input"), dict) else {}
    response = event.get("tool_response")
    return Agent(kind=kind,
                 session=text_of(event.get("session_id")),
                 cwd=text_of(event.get("cwd")),
                 transcript=text_of(event.get("transcript_path")),
                 agent_id=text_of(event.get("agent_id")) or response_field(response, "agentId"),
                 agent_type=text_of(event.get("agent_type")) or text_of(ti.get("subagent_type")),
                 description=text_of(ti.get("description")) or response_field(response, "description"),
                 output=response_field(response, "outputFile"),
                 message=text_of(event.get("last_assistant_message")),
                 jobs=claude_code_jobs(event),
                 active=bool(event.get("stop_hook_active")))


# Таблица разборщиков: протокол, разбор записи, разбор события сессии, разбор
# хода инструмента, разбор старта сессии, разбор события про фонового субагента
# и два канала ответа. Новый инструмент добавляется строкой сюда и директорией
# образцов.
Protocol = collections.namedtuple("Protocol", "write session tool start agent reply context")

PROTOCOLS = {
    "claude-code": Protocol(claude_code_write, claude_code_session,
                            claude_code_tool, claude_code_start,
                            claude_code_agent,
                            Reply(2), Context("PostToolUse")),
}


def protocol(argv):
    """Имя протокола из хвоста argv после `--hook`."""
    return argv[0] if argv and argv[0] else DEFAULT


def known(name):
    return name in PROTOCOLS


def entry(name):
    if name not in PROTOCOLS:
        raise Unknown("разбор входа протокола %s не заведён, известны: %s"
                      % (name, ", ".join(sorted(PROTOCOLS))))
    return PROTOCOLS[name]


def reply(name):
    return entry(name).reply


def context(name):
    return entry(name).context


def load(stream=None):
    """Событие со stdin. Кривой вход это BadEvent с причиной: хук стоит в каждой
    сессии и падать traceback'ом ему нельзя."""
    stream = sys.stdin if stream is None else stream
    try:
        event = json.load(stream)
    except (json.JSONDecodeError, UnicodeDecodeError):
        raise BadEvent("не json")
    if not isinstance(event, dict):
        raise BadEvent("json не объектом")
    return event


def parse_write(name, event):
    return entry(name).write(event)


def parse_session(name, event):
    return entry(name).session(event)


def parse_tool(name, event):
    return entry(name).tool(event)


def parse_start(name, event):
    return entry(name).start(event)


def parse_agent(name, event):
    return entry(name).agent(event)


def write_event(name, stream=None):
    """Фрагменты записи из события на stdin. None значит смотреть нечего."""
    try:
        return parse_write(name, load(stream))
    except BadEvent:
        return None


def session_event(name, stream=None):
    """Событие сессии со stdin. None значит ось не наша (BadEvent зовущий ловит
    сам: уведомителю причина нужна для журнала)."""
    return parse_session(name, load(stream))


def start_event(name, stream=None):
    """Старт сессии со stdin. None значит, что событие не про старт: реестр на
    таком входе молчит и уходит нулём."""
    try:
        return parse_start(name, load(stream))
    except BadEvent:
        return None


def agent_event(name, stream=None):
    """Событие про фонового субагента со stdin. None значит, что событие не про
    него: сторож на таком входе молчит и уходит нулём."""
    try:
        return parse_agent(name, load(stream))
    except BadEvent:
        return None


def tool_event(name, stream=None):
    """Ход инструмента со stdin. None значит, что событие не про ход, и хук на
    нём уходит нулём: подхват стоит в чужой работе и ронять её не вправе."""
    try:
        return parse_tool(name, load(stream))
    except BadEvent:
        return None


def append_capped(path, line, limit=LOG_LIMIT, keep=LOG_KEEP):
    """Дописать строку в журнал хука, обрезав разросшийся файл до последних
    строк. Провал записи тихий: журнал это след работы, и ронять из-за него
    сессию нельзя."""
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        if os.path.exists(path) and os.path.getsize(path) > limit:
            with open(path, encoding="utf-8", errors="replace") as f:
                tail = f.readlines()[-keep:]
            with open(path, "w", encoding="utf-8") as f:
                f.writelines(tail)
        with open(path, "a", encoding="utf-8") as f:
            f.write(line)
    except OSError:
        pass


def tree_root(cwd):
    """Дерево работы: ближайший предок cwd с .git. У бокового дерева задачи это
    файл со ссылкой на общий каталог, и граница git здесь не мелочь: по .devkit
    сессия дерева задачи поднялась бы к основному чекауту и взяла чужой
    разговор. None значит, что ход идёт вне git-дерева: разговоров таким
    деревьям не заводят, и в реестре чатов у такой сессии пустое дерево."""
    cur = (cwd or "").rstrip(os.sep)
    while cur:
        if os.path.exists(os.path.join(cur, ".git")):
            return cur
        parent = os.path.dirname(cur)
        if parent == cur:
            return None
        cur = parent
    return None


def harness_dir():
    # Своя директория профилей нужна тестам и нестандартной раскладке devkit.
    return os.environ.get("DEVKIT_HARNESS_DIR") or os.path.join(ROOT, "kit", "harness")


def toml_at(path):
    """Файл формата профилей разобранным Doc и причина, если Doc пустой.
    Парсер один на весь devkit, второй копии формата тут не заводится.

    Кроме профилей харнеса этим же форматом живут конфиги проверок
    (kit/prose.toml у сторожа прозы), и причина им нужна текстом: доктор
    печатает её находкой, а «файла нет» и «файл битый» это разные починки."""
    sys.path.insert(0, os.path.join(ROOT, "tools", "devkitctl"))
    try:
        import harness
    except ImportError:
        return None, "парсер формата не найден рядом с devkit"
    finally:
        sys.path.pop(0)
    try:
        with open(path, encoding="utf-8") as f:
            return harness.parse(os.path.basename(path), f.read()), ""
    except OSError:
        return None, "файла нет: %s" % path
    except harness.TomlError as e:
        return None, "%s" % e


def profile(name, directory=None):
    """Профиль харнеса разобранным Doc, None если файла нет или он битый."""
    path = os.path.join(directory or harness_dir(), "%s.toml" % name)
    return toml_at(path)[0]


def memory_index(name, directory=None):
    """Хвост пути индекса памяти из `[hooks] memory_index`. Пусто значит, что
    памяти у инструмента нет и проверять нечего."""
    d = profile(name, directory)
    return d.str_of("hooks", "memory_index") if d else ""
