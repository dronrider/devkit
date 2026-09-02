#!/usr/bin/env python3
"""Рубеж повторных чтений: PreToolUse-хук на Read срабатывает на файл, уже
прочитанный в этой сессии и не менявшийся с тех пор, и отвечает подсказкой
вместо содержимого (задача DK-146, цель DK-206). Первый Read и чтение после правки
файла проходят нетронутыми. Чтение другого куска того же файла (другие
offset/limit/pages) это не повтор: окно у записи своё, и рубить его нельзя.

История чтений своя, а не транскрипт сессии: журналы растут на десятки
мегабайт, и каждое чтение гонять их через парсер накладнее самой пользы.
Состояние лежит в ~/.devkit/reread/<контекст>.json, по окну на запись. Контекст
это не сессия: субагент делит session_id с диспетчером, а контекст у него свой
и пустой, и подсказка про «уже прочитанное» резала бы ему первое чтение
(DK-608). Различает их поле agent_id, которое харнес кладёт в событие только
под субагентом, поэтому у диспетчера имя файла это session_id, а у субагента
session_id с приписанным agent_id.
Неизменность файла проверяется парой (mtime_ns, size) из одного stat: любая
правка файла (Edit, Write, git checkout, echo >) меняет mtime, а size
подстрахует на файловых системах с грубым разрешением времени. mtime без
правки меняет только touch, и держать этот случай за рубежом нечем, но и
вред от него узкий: один пропуск повторного чтения после холостого touch.

Каркас рассчитан на соседнюю проверку: DK-147 добавит рубеж «чтение длинного
файла целиком» на это же событие, и две записи в HOOK_LAYOUT на одном матчере
Read живут рядом, как три PostToolUse-проверки на Edit|Write|NotebookEdit.
Каждая блокирует Read своим выходом 2, харнес скармливает агенту обе
подсказки как фидбек.

Режимы:
  check-reread.py --hook [протокол]
                   хук на PreToolUse Read: JSON события на stdin, разбор
                   входа и канал ответа по имени протокола из hookio.py,
                   голый --hook это claude-code, ответ выходом 2 с подсказкой
                   на stderr
  check-reread.py --state <каталог>
                   перебивает каталог состояния (для тестов; по умолчанию
                   ~/.devkit/reread)
"""
import json
import os
import sys
import time

import hookio

# Брошенное состояние старше порога убирается тем же проходом, что и читает:
# отдельной уборки заводить незачем. Сессия без обращений столько дней уже
# брошенная, а файл, правленный за это время снаружи, всё равно не совпадёт по
# mtime, и пропуск не страшен.
STALE = 7 * 24 * 3600


def text_of(value):
    return value if isinstance(value, str) else ""


def num_or_none(value):
    # bool это подкласс int, и pages = true без этой проверки ушёл бы в ключ
    # окна числом. limit/offset/pages у Read либо целое, либо отсутствует.
    if isinstance(value, bool):
        return None
    return value if isinstance(value, (int, float)) else None


def state_dir(override=None):
    where = override or os.path.join(os.path.expanduser("~"), ".devkit", "reread")
    try:
        os.makedirs(where, exist_ok=True)
    except OSError:
        pass
    return where


def safe_name(value):
    # Имя файла собирается из значений события, и чужой символ в нём уводил бы
    # запись из каталога состояния. Настоящие session_id и agent_id это UUID и
    # hex, под фильтр они проходят как есть.
    return "".join(c if c.isalnum() or c in "-_" else "_" for c in value)


def context_id(session, agent):
    """Имя контекста чтения. У диспетчера это сессия целиком, без обрезки:
    каталог один на все сессии машины. У субагента к ней приписан agent_id, и
    свой контекст он получает даже там, где session_id общий с диспетчером.
    Разделителем стоит точка: в session_id (UUID) и agent_id (hex) её нет, и
    пара разбирается на части однозначно."""
    if not agent:
        return safe_name(session)
    return safe_name(session) + "." + safe_name(agent)


def state_path(context, override=None):
    return os.path.join(state_dir(override), context + ".json")


def sweep(directory, now):
    # Сессии кончаются, их файлы состояния остаются; брошенное старше порога
    # убирается тем же проходом, отдельной уборки заводить незачем.
    try:
        names = os.listdir(directory)
    except OSError:
        return
    for name in names:
        if not name.endswith(".json"):
            continue
        f = os.path.join(directory, name)
        try:
            if now - os.path.getmtime(f) > STALE:
                os.remove(f)
        except OSError:
            pass


def load_state(context, override=None):
    path = state_path(context, override)
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError):
        # Файла нет или он битый: читаем как пустую историю. Битый перетирается
        # первой же записью, и поднимать его на глаз не нужно.
        return {}
    if not isinstance(data, dict):
        return {}
    return data


def save_state(context, state, override=None):
    path = state_path(context, override)
    tmp = path + ".tmp"
    # Атомарная запись через rename: параллельный PreToolUse на Read у
    # субагента не портит файл частичной записью. Состояния записей теряться
    # могут (оба прочитали пустое, оба записали своё), но вред от этого узкий:
    # один пропуск подсказки, не порча.
    try:
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(state, f, ensure_ascii=False, separators=(",", ":"))
        os.replace(tmp, path)
    except OSError:
        pass


def window_key(path, offset, limit, pages):
    # Окно это (путь, offset, limit, pages). Запись в состоянии по нему: то же
    # окно того же файла, что не менялся с прошлого чтения, это повтор. Чтение
    # другим окном того же файла (limit без offset, полный файл против куска)
    # проходит как первое.
    return json.dumps([path, offset, limit, pages],
                      ensure_ascii=False, separators=(",", ":"))


def hint(path):
    return ("файл уже прочитан в этой сессии и не менялся с тех пор, "
            "содержимое осталось в контексте; перечитывать то же окно не нужно, "
            "а для другого куска того же файла позови Read с другими "
            "offset/limit/pages (рубеж DK-146, цель DK-206). путь: %s\n"
            % path)


def read_window(event):
    """Параметры окна чтения из события. None значит, что это не Read, и
    смотреть нечего: хук стоит на матчере Read, но плохой вход не должен ронять
    хук, и здесь он уходит нулём."""
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return None
    path = text_of(ti.get("file_path"))
    if not path:
        return None
    offset = num_or_none(ti.get("offset"))
    limit = num_or_none(ti.get("limit"))
    pages = text_of(ti.get("pages")) or None
    return path, offset, limit, pages


def run_hook(protocol, override=None):
    # Протокол сверяется до разбора события: опечатка в settings.json иначе
    # выключила бы рубеж насовсем. В check-read-secret проверка протокола
    # выезжала сама: канал ответа там звался на каждом пути с находкой. Тут
    # большинство чтений проходят без подсказки, и проверять протокол надо явно.
    hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    session = text_of(event.get("session_id"))
    if not session:
        # Без session_id историю не привязать ни к какой сессии, и рубить
        # нельзя: пусть Read проходит как есть.
        return 0
    # agent_id есть в событии только под субагентом, у хода самой сессии этого
    # поля нет вовсе (снято живым прогоном, docs/tasks/DK-608.md). Транскрипт
    # на эту роль не годится: у субагента он тот же, что у диспетчера.
    context = context_id(session, text_of(event.get("agent_id")))
    window = read_window(event)
    if window is None:
        return 0
    path, offset, limit, pages = window
    try:
        st = os.stat(path)
    except OSError:
        # Файл недоступен или его нет. Read сам скажет, и подсказка тут лишняя.
        return 0
    state = load_state(context, override)
    key = window_key(path, offset, limit, pages)
    prev = state.get(key)
    if prev is not None and prev.get("m") == st.st_mtime_ns \
            and prev.get("s") == st.st_size:
        return hookio.reply(protocol).found(hint(path))
    state[key] = {"m": st.st_mtime_ns, "s": st.st_size}
    save_state(context, state, override)
    # Уборка идёт после записи, а не до: новое состояние не должно попасть под
    # нож, если порог подобран неудачно, и видно это по свежему файлу.
    sweep(state_dir(override), time.time())
    return 0


def main(argv):
    override = None
    args = list(argv)
    # Каталог состояния для тестов: --state <путь>. Стоит после --hook и
    # одиночным флагом, чтобы не путать разбор протокола в hookio.protocol.
    if "--state" in args:
        i = args.index("--state")
        if i + 1 < len(args):
            override = args[i + 1]
            del args[i:i + 2]
    if args[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(args[1:]), override)
        except hookio.Unknown as e:
            sys.stderr.write("check-reread: %s\n" % e)
            return 2
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
