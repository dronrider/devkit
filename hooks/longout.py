#!/usr/bin/env python3
"""Рубеж длинного вывода (DK-499): PostToolUse-хук на Bash отдаёт модели выжимку
вместо полного вывода команды. Порог и формат выжимки живут в internal/frame
(docs/lld/DK-137-cmdout-wrapper.md), их python-часть не переизобретает: хук
кладёт вывод в подкоманду `cmdout digest` и подставляет её ответ каналом
подмены (hookSpecificOutput.updatedToolOutput, docs/lld/DK-136-live-chat.md).

Короткий вывод подкоманда оставляет в покое, и хук молчит же: команда доходит
до модели целиком, файл полного вывода не заводится. Так же хук молчит без
cmdout в PATH, вне git-дерева и на старом бинаре без подкоманды digest: рубеж,
который не может судить, дешевле молчит, чем портит чужую команду. Срабатывание
отличимо от бездействия по следу в транскрипте: удачный ответ хука пишится там
записью hook_success с JSON подмены, а молчание оставляет пустой след.

Граница канала: подмена стоит на событии PostToolUse, и харнес вызывает его
только на удачном ходе инструмента. Упавшая команда уходит в
PostToolUseFailure, где подмены нет: длинный вывод падения доходит до модели
как есть, а в контексте остаётся только то, что харнес и так положил в рамку
провала.

Режимы:
  longout.py --hook [протокол]
                   PostToolUse на Bash: JSON события на stdin, в ответе
                   подменённый tool_response. Голый --hook это claude-code
  longout.py --why [каталог]
                   чем рубеж судит прямо сейчас: есть ли cmdout, есть ли
                   подкоманда digest и лежит ли ход в git-дереве
"""
import json
import os
import shutil
import subprocess
import sys

import hookio

TOOL = "Bash"
CMDOUT = "cmdout"


def run_hook(protocol):
    # Протокол сверяется до разбора события: незнакомое имя не должно молчать
    # неотличимо от «команда короткая».
    hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    if hookio.text_of(event.get("hook_event_name")) != "PostToolUse":
        return 0
    if hookio.text_of(event.get("tool_name")) != TOOL:
        return 0
    response = event.get("tool_response")
    if not isinstance(response, dict) or response.get("isImage"):
        # Ответ не объектом или в картинке подменять нечего: это не текст
        # вывода Bash, и судить его порогом выжимки нельзя.
        return 0
    text = output_text(response)
    if not text:
        return 0
    binary = shutil.which(CMDOUT)
    if not binary:
        return 0
    argv = [binary, "digest"]
    slug = first_word(hookio.text_of((event.get("tool_input") or {}).get("command"))
                      if isinstance(event.get("tool_input"), dict) else "")
    if slug:
        argv.append(slug)
    cwd = hookio.text_of(event.get("cwd")) or None
    try:
        proc = subprocess.run(argv, input=text, capture_output=True, text=True,
                              encoding="utf-8", errors="replace", cwd=cwd)
    except (OSError, subprocess.SubprocessError):
        return 0
    if proc.returncode != 0 or not proc.stdout:
        # Ниже порога, вне git-дерева или на старом бинаре подкоманда молчит,
        # и молчание её означает «подменять нечего»: вывод доходит как есть.
        return 0
    # Подмена держит ровно четыре поля справки: копия всего tool_response
    # несла бы лишние (noOutputExpected и прочие), а схема клиента отбивает
    # несовпадение молча.
    substitute = {"stdout": proc.stdout,
                  "stderr": "",
                  "interrupted": bool(response.get("interrupted")),
                  "isImage": bool(response.get("isImage"))}
    print(json.dumps({"hookSpecificOutput": {"hookEventName": "PostToolUse",
                                             "updatedToolOutput": substitute}},
                     ensure_ascii=False))
    return 0


def output_text(response):
    """Полный вывод команды одной строкой: stdout и stderr лежат в ответе двумя
    полями, и в выжимку они идут вместе, иначе длинный stderr остался бы в
    контексте целым."""
    out = hookio.text_of(response.get("stdout"))
    err = hookio.text_of(response.get("stderr"))
    if out and err:
        return out + "\n" + err
    return out or err


def first_word(command):
    """Первая лексема команды: её cmdout берёт слогом имени каталога вывода."""
    parts = command.split()
    return parts[0] if parts else ""


def run_why(argv, out):
    """Что рубеж скажет про каталог прямо сейчас. Без этого режима человеку
    нечем отличить «вывод короткий, подмены не было» от «рубеж молчит, потому
    что судить нечем»."""
    where = argv[0] if argv else os.getcwd()
    binary = shutil.which(CMDOUT)
    if not binary:
        out.write("cmdout в PATH нет: рубеж молчит, длинный вывод льётся "
                  "в контекст целиком\n")
        return 0
    try:
        probe = subprocess.run([binary, "digest"], input="короткий\n",
                               capture_output=True, text=True,
                               encoding="utf-8", errors="replace", cwd=where)
    except (OSError, subprocess.SubprocessError) as e:
        out.write("подкоманда digest не спросилась (%s): рубеж молчит\n" % e)
        return 0
    if probe.returncode != 0 or probe.stdout:
        out.write("подкоманда digest в cmdout нет (бинарь старый): рубеж "
                  "молчит, обновить cmdout\n")
        return 0
    root = hookio.tree_root(where)
    if not root:
        out.write("%s вне git-дерева: полного файла быть не может, рубеж "
                  "молчит\n" % where)
        return 0
    out.write("всё готово: на пороге длинный вывод уйдёт выжимкой, файл "
              "ляжет в %s\n" % os.path.join(root, ".devkit", "cmdout"))
    return 0


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("longout: %s\n" % e)
            return 2
    if argv[:1] == ["--why"]:
        return run_why(argv[1:], sys.stdout)
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
