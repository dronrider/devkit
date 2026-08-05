#!/usr/bin/env python3
"""Разбор входа хуков: таблица разборщиков по имени протокола харнеса.

Тело проверки одно на все инструменты, меняется только форма события на stdin,
поэтому своего разбора проверки не носят, а зовут отсюда:

  hookio.write_event(protocol)     фрагменты записи: путь и записанные куски
  hookio.session_event(protocol)   событие сессии для уведомителя
  hookio.reply(protocol)           канал ответа: чем сказать о находках
  hookio.memory_index(protocol)    хвост пути индекса памяти из профиля

Имя протокола приходит аргументом `--hook <протокол>`; голый `--hook` это
claude-code, иначе команды, прописанные в settings.json на машинах, сломались
бы об обновление devkit. Протокол без образцов в hooks/testdata/<протокол>/ в
таблицу не принимается: форма события живёт на стороне инструмента, и снятый с
неё живьём JSON это единственное, чем разбор проверяется.
"""
import collections
import json
import os
import sys

DEFAULT = "claude-code"

# Оси событий сессии, имена те же, что в `[hooks] events` профиля.
NOTIFY = "notify"
TURN_DONE = "turn-done"
SUBAGENT_DONE = "subagent-done"
PROMPT_SUBMIT = "prompt-submit"

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Фрагменты записи: путь файла и куски текста, которые в него уехали. Проверки
# смотрят именно куски, а не файл целиком, поэтому чужой существующий текст не
# всплывает.
Write = collections.namedtuple("Write", "path chunks")
# Событие сессии в общих полях: ось, повод (у оси notify), сессия, рабочее
# дерево, транскрипт, текст события и роль субагента.
Session = collections.namedtuple("Session", "kind reason session cwd transcript message agent")


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
    # формы должно доехать до него, а не потеряться тут.
    return Session(kind=kind,
                   reason=event.get("notification_type") or "",
                   session=str(event.get("session_id") or "-")[:8],
                   cwd=text_of(event.get("cwd")),
                   transcript=text_of(event.get("transcript_path")),
                   message=message,
                   agent=event.get("agent_type"))


# Таблица разборщиков: протокол, разбор записи, разбор события сессии, канал
# ответа. Новый инструмент добавляется строкой сюда и директорией образцов.
PROTOCOLS = {
    "claude-code": (claude_code_write, claude_code_session, Reply(2)),
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
    return entry(name)[2]


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
    return entry(name)[0](event)


def parse_session(name, event):
    return entry(name)[1](event)


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


def harness_dir():
    # Своя директория профилей нужна тестам и нестандартной раскладке devkit.
    return os.environ.get("DEVKIT_HARNESS_DIR") or os.path.join(ROOT, "kit", "harness")


def profile(name, directory=None):
    """Профиль харнеса разобранным Doc, None если файла нет или он битый.
    Парсер один на весь devkit, второй копии формата тут не заводится."""
    path = os.path.join(directory or harness_dir(), "%s.toml" % name)
    sys.path.insert(0, os.path.join(ROOT, "tools", "devkitctl"))
    try:
        import harness
    except ImportError:
        return None
    finally:
        sys.path.pop(0)
    try:
        with open(path, encoding="utf-8") as f:
            return harness.parse(os.path.basename(path), f.read())
    except (OSError, harness.TomlError):
        return None


def memory_index(name, directory=None):
    """Хвост пути индекса памяти из `[hooks] memory_index`. Пусто значит, что
    памяти у инструмента нет и проверять нечего."""
    d = profile(name, directory)
    return d.str_of("hooks", "memory_index") if d else ""
