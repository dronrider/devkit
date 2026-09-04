#!/usr/bin/env python3
"""Хук PreToolUse на AskUserQuestion (DK-715): в сессии, поднятой панелью
дашборда, перехватывает вопрос агента и перекладывает его в файл признака
`.devkit/chat/<разговор>.ask`, вместо диалога харнеса, который панель не
показывает и который пропадает через восемь минут снимком tmux.

Агент везде спрашивает штатным AskUserQuestion, как в обычном терминале. Этот
хук ловит вызов только в сессии, которую подняла панель: у неё задан
DEVKIT_TMUX именем chat-*, goal-* или task-*, тем же, что пишет подъёмщик
(tools/dashboard/chats.go, launchEnv). Обычный терминал переменную не ставит
никто, и там диалог харнеса работает как раньше.

Хук не пишет признак сам: формат живёт в go-пакете internal/chat, и
дублировать его на python незачем. Вместо этого он зовёт внутренний писатель
`taskctl ask <ID> --session <sid>` с пачкой вопросов на stdin: тот же путь,
каким признак кладёт ручной вызов, разница только в том, что вызывает его
теперь хук, а не агент. Писатель кладёт файл без срока, паркует строку задачи
и зовёт уведомитель; хук отбивает исходный вызов инструмента (exit 2) и
подсказывает агенту закончить ход текстом вопроса.

Хуки PreToolUse срабатывают и у субагентов-исполнителей: DEVKIT_TMUX и
DEVKIT_TASK они наследуют вместе с остальным окружением сессии, которая их
подняла, и правки exec-агентов ради этого не нужно.

ID задачи хук называет по трём источникам в порядке точности: переменная
DEVKIT_TASK (заказ подъёмщика), реестр чатов ~/.devkit/sessions.log по ID
сессии, хвост имени рабочего дерева (devkit-dk-715 -> DK-715). Не нашлось ни
одного, значит разговор без задачи (личный чат), парковать и писать признак
некому, и диалог остаётся диалогом харнеса.

Заголовок вопроса (`header`) харнес несёт отдельным полем, а формат признака
(internal/chat.Question) его не заводит: варианты и текст вопроса важнее
короткой подписи таба, и заголовок в признак не едет.

Режим один:
  ask-panel.py --hook [протокол]   хук на PreToolUse AskUserQuestion: JSON
                                   события на stdin, разбор и канал ответа по
                                   имени протокола из hookio.py, голый --hook
                                   это claude-code, ответ exit 2
"""
import json
import os
import re
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

TASK_ENV = "DEVKIT_TASK"
TMUX_ENV = "DEVKIT_TMUX"

# Форма ID задачи в заказе, та же, что у session-task.py.
TASK_RE = re.compile(r"^[A-Z]{2,10}-[0-9]{1,6}$")
# Хвост имени бокового дерева: devkit-dk-431 и голое dk-431.
TREE_RE = re.compile(r"(?:^|-)([A-Za-z]{2,10}-[0-9]{1,6})$")
# Сессия поднята панелью: chat-<ID>-<n>, goal-<ID>, task-<ID>-<n>. Плейн-
# терминал эту переменную не ставит никто, и диалог там остаётся диалогом.
PANEL_TMUX_RE = re.compile(r"^(chat|goal|task)-")

SESS_LOG = os.path.join(".devkit", "sessions.log")
BIND_KEYS = ("сессия", "задача", "проект", "дерево", "транскрипт",
             "источник", "повод", "tmux", "родитель")

TASKCTL_TIMEOUT = 20


def ordered_task(env):
    """Задача из заказа подъёмщика. Пустая строка значит, что заказа не было
    или он не похож на ID: врать про чужую задачу дороже, чем промолчать."""
    task = (env.get(TASK_ENV) or "").strip().upper()
    return task if TASK_RE.match(task) else ""


def tree_task(root):
    """Задача по хвосту имени рабочего дерева."""
    if not root:
        return ""
    m = TREE_RE.search(os.path.basename(root.rstrip(os.sep)))
    return m.group(1).upper() if m else ""


def bind_line(line):
    """Сессия и её задача из строки реестра чатов либо (None, None). Разбор
    тот же, что в internal/sessions и hooks/chat-in.py: строка общая, и третья
    копия разбора не заводится ради одного поля."""
    f = line.strip().split()
    if len(f) < 3 or f[1] != "сессия":
        return None, None
    vals, key = {}, ""
    for tok in f[1:]:
        if tok in BIND_KEYS and vals.get(key):
            key = tok
            continue
        if not key:
            key = tok
            continue
        vals[key] = (vals.get(key, "") + " " + tok).strip()
    sid = vals.get("сессия", "").strip()
    if sid in ("", "-"):
        return None, None
    task = vals.get("задача", "").strip()
    return sid, "" if task == "-" else task.upper()


def registry_task(session, home):
    """Задача сессии по реестру чатов. Выигрывает последняя запись сессии, как
    и у остальных читателей журнала: перепривязка это обычная строка, а не
    правка файла."""
    if not session:
        return ""
    try:
        with open(os.path.join(home, SESS_LOG), encoding="utf-8", errors="replace") as f:
            rows = f.read().split("\n")
    except OSError:
        return ""
    task = ""
    for row in rows:
        sid, got = bind_line(row)
        if sid == session:
            task = got
    return task


def resolve_task(env, session, root, home):
    """Задача разговора по трём источникам от точного к запасному: заказ,
    реестр чатов, хвост дерева. Пустая строка значит, что разговор без
    задачи: личному чату признак ожидания и парковку заводить некому."""
    task = ordered_task(env)
    if task:
        return task
    task = registry_task(session, home)
    if task:
        return task
    return tree_task(root)


def pack_of(questions):
    """Пачка вопросов харнеса в формате признака ожидания (internal/chat.Pack).
    Заголовок вопроса (header) сюда не едет: у признака его поля нет, а текст
    вопроса и варианты важнее короткой подписи таба."""
    out = []
    for q in questions or []:
        if not isinstance(q, dict):
            continue
        text = " ".join((q.get("question") or "").split())
        if not text:
            continue
        item = {"text": text}
        if q.get("multiSelect"):
            item["multi"] = True
        opts = []
        for o in q.get("options") or []:
            if not isinstance(o, dict):
                continue
            label = " ".join((o.get("label") or "").split())
            if not label:
                continue
            opt = {"label": label}
            note = " ".join((o.get("description") or "").split())
            if note:
                opt["note"] = note
            opts.append(opt)
        if opts:
            item["options"] = opts
        out.append(item)
    return out


def write_ask(root, task, session, questions, env):
    """Зовёт внутренний писатель признака: `taskctl ask` кладёт файл без
    срока, паркует строку и зовёт уведомитель. Возврат (сообщение, провал):
    провал значит, что writer не отработал, и агенту об этом сказано прямо, а
    не молчанием."""
    pack = json.dumps({"questions": questions}, ensure_ascii=False)
    try:
        out = subprocess.run(
            ["taskctl", "-C", root, "ask", task, "--session", session],
            input=pack, capture_output=True, text=True, timeout=TASKCTL_TIMEOUT, env=env)
    except (OSError, subprocess.SubprocessError) as e:
        return "писатель признака не позвался: %s" % e, True
    if out.returncode != 0:
        return "писатель признака ответил отказом: %s" % (out.stderr or out.stdout).strip(), True
    return out.stdout.strip(), False


def report(task, msg, failed):
    """Текст отказа исходному вызову: он отбивает диалог харнеса и подсказывает
    агенту закончить ход текстом вопроса вместо ожидания в диалоге, который
    панель не покажет."""
    lines = []
    if failed:
        lines.append(
            "Признак вопроса %s не лёгся (%s), но диалог AskUserQuestion панель дашборда "
            "всё равно не покажет: свой снимок она читает только в обычном терминале." % (task, msg))
    elif msg:
        lines.append(msg)
    lines.append(
        "Диалог AskUserQuestion в этой сессии не покажется: заверши ход текстом вопроса и "
        "вариантами, а дальше жди реплику человека, вопрос уже ушёл панели.")
    return "\n\n".join(lines) + "\n"


def run_hook(protocol, env=None):
    env = dict(os.environ if env is None else env)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    if event.get("hook_event_name") != "PreToolUse" or event.get("tool_name") != "AskUserQuestion":
        return 0
    if not PANEL_TMUX_RE.match((env.get(TMUX_ENV) or "").strip()):
        # Обычный терминал эту переменную не ставит никто (session-task.py,
        # window_name): диалог харнеса тут работает как раньше.
        return 0
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return 0
    questions = pack_of(ti.get("questions"))
    if not questions:
        return 0
    session = hookio.text_of(event.get("session_id"))
    cwd = hookio.text_of(event.get("cwd"))
    root = hookio.tree_root(cwd) or cwd
    home = os.path.expanduser(env.get("HOME") or "~")
    task = resolve_task(env, session, root, home)
    if not task:
        # Разговор без задачи (личный чат): парковать и признавать некому, и
        # своей дороги вопроса у него нет.
        return 0
    msg, failed = write_ask(root, task, session, questions, env)
    return hookio.reply(protocol).found(report(task, msg, failed))


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("ask-panel: %s\n" % e)
            return 2
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
