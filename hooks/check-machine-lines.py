#!/usr/bin/env python3
"""Рубеж машинных строк файла задачи (DK-1058): PostToolUse-хук на
Edit|Write|NotebookEdit сверяет `docs/tasks/<ID>.md` по сторонам события
записи (тот же приём, что решение 4 LLD DK-606 отвело под сторожа разметки:
`tool_response.originalFile` это «до», файл на диске по `filePath` это
«после», восстанавливать «после» из фрагмента правки нельзя, `replaceAll`
даёт не тот текст). Правка, убравшая или сломавшая строку, которую разбирает
код доски, отбивается сразу; новая строка того же формата и правка живой
прозы проходят молча.

Перечень форматов не свой: бОльшая часть регулярок взята без копирования из
`check-prose.py` (`TOP_LEVEL_MACHINE_RES`, `ACCEPT_OUTCOME_RE`, `FORK_SUB_RE`,
`REVIEW_LEVEL_HEAD_RE`, `REVIEW_VERDICT_HEAD_RE`) динамической загрузкой
модуля: оба рубежа читают один и тот же список, а не две копии, которые
расходятся сами по себе. Форматы, которых у сторожа прозы нет, потому что для
плотности прозы они не нужны, дописаны здесь по коду: судьба чужого MR
(`tools/taskctl/reviewpoll.go`, `reviewFateNote`/`appendReviewStage`), журнал
цели (`tools/agentctl/goal.go`, снимок квоты и виток) и ссылка чужого ревью
(`tools/trackctl/review.go`, `fillReviewFile`). `kit/skills/proofread/machine-lines.md`
ссылается на этот файл, а не держит перечень прозой второй раз.

Проверка считает совпадения каждого формата по обеим сторонам. Убыль счёта у
любого формата это находка: строку либо убрали, либо сломали так, что она
больше не узнаётся кодом. Пример из DK-460: обрезанная квота увела конец
строки этапа за пределы `STAGE_LINE_RE`. Пример из DK-1050: снятое двоеточие у
головы вердикта увело строку за пределы `REVIEW_VERDICT_HEAD_RE`. Новая
строка того же формата только прибавляет счёт, и находки не будет. Правка
живой прозы формат не задевает вовсе. Где сравнивать не с чем (`originalFile`
пуст: файл только что создан этим же вызовом), хук не блокирует ход, а
говорит об этом добавкой к контексту: молчание тут неотличимо от «правка
чистая».

Матчер `Edit|Write|NotebookEdit` не включает `MultiEdit` (`POST_MATCHER` в
`tools/devkitctl/devkitctl.py`), тем же порядком, что у `check-prose.py`, и
третья правка того же абзаца `MultiEdit`-ом мимо этого рубежа тоже проходит.
Правка вне харнеса (коллега редактирует файл текстовым редактором на другой
машине) сюда не входит: DoD задачи называет условием только правку руками
внутри Claude Code, мимо скилла `proofread`, а не вовсе без него.

Режимы:
  check-machine-lines.py --hook [протокол]
                   хук на запись файла: JSON события на stdin, разбор входа и
                   канал ответа по имени протокола из hookio.py, голый --hook
                   это claude-code
"""
import importlib.util
import os
import re
import sys

import hookio

HERE = os.path.dirname(os.path.abspath(__file__))
CHECK_PROSE_PATH = os.path.join(HERE, "check-prose.py")


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


# Дефис в имени файла не даёт обычный import, поэтому модуль грузится по пути
# (тот же приём, что в check_prose_test.py). Загрузка идёт при каждом запуске
# хука отдельным процессом: чужого состояния между вызовами нет.
check_prose = _load("check_prose_shared_dk1058", CHECK_PROSE_PATH)

# Файл задачи или файл цели: прямо внутри docs/tasks, не в drafts/ и не в
# archive/<год>/, там правка руками редка, а живой хук на неё не рассчитан.
TASK_FILE_RE = re.compile(r"(?:^|/)docs/tasks/[^/]+\.md$")

# «Ход работы»: судьба чужого MR дописывается reviewFateNote+appendReviewStage
# (tools/taskctl/reviewpoll.go): "- MR слит, <дата>.", "- MR закрыт, <дата>.",
# "- MR слит без апрува ревью, <дата>.".
MR_FATE_RE = re.compile(
    r"^-\s+MR (?:слит(?: без апрува ревью)?|закрыт),\s*\d{4}-\d{2}-\d{2}\.\s*$")

# «Журнал» файла цели (tools/agentctl/goal.go). Снимок квоты:
# "- снимок 2026-08-03T12:00: week_all 12%, week_max 4%". Виток, где маркер
# один из goalGoOn/goalStops:
# "- 2026-09-19 19:54-19:55, заметка[, цикл 3ч, витков 4]; continue".
GOAL_SNAP_RE = re.compile(r"^-\s+снимок\s+\S+:\s")
GOAL_LAP_RE = re.compile(
    r"^-\s+\d{4}-\d{2}-\d{2}\s\d{2}:\d{2}.*;\s"
    r"(?:continue|done|over|wait-human|stuck)\s*$")

# Задача чужого ревью (tools/trackctl/review.go, fillReviewFile) кладёт в
# «Что происходит» одноразовую строку без маркера списка: "MR: <адрес>".
REVIEW_LINK_RE = re.compile(r"^MR:\s")

# Регулярки check-prose.py, которые матчатся на line.strip() (см.
# is_machine_line там же): у настоящей записи отступа нет.
_STRIPPED = {
    "ход работы: этап": check_prose.STAGE_LINE_RE,
    "ранг": check_prose.RANK_LINE_RE,
    "выкат: слияние": check_prose.DEPLOY_MERGE_RE,
    "выкат: smoke": check_prose.DEPLOY_SMOKE_RE,
    "выкат: перевод отбит": check_prose.DEPLOY_PENDING_RE,
    "выкат: перевод доведён": check_prose.DEPLOY_MOVE_DONE_RE,
    "приёмка: вид": check_prose.ACCEPT_KIND_RE,
    "приёмка: барьер": check_prose.ACCEPT_BARRIER_RE,
    "развилка: голова": check_prose.FORK_HEAD_RE,
    "проверка: стенд": check_prose.STAND_RE,
    "проверка: обкатка": check_prose.REHEARSAL_RE,
    "ход работы: вычитка": check_prose.PROOFREAD_RE,
    "ход работы: сторож прозы": check_prose.PROSE_MARK_RE,
    "ход работы: исключение": check_prose.EXCEPTION_RE,
    "ход работы: возврат": check_prose.RETURN_RE,
    "ревью: уровень": check_prose.REVIEW_LEVEL_HEAD_RE,
    "ревью: вердикт": check_prose.REVIEW_VERDICT_HEAD_RE,
    "ход работы: судьба MR": MR_FATE_RE,
    "журнал: снимок": GOAL_SNAP_RE,
    "журнал: виток": GOAL_LAP_RE,
    "что происходит: ссылка ревью": REVIEW_LINK_RE,
}

# Регулярки, которым нужен неподрезанный отступ: check-prose.py матчит их на
# исходную строку без strip(), чтобы отличить вложенный пункт развилки или
# приёмки от такого же синтаксиса примером верхним уровнем (DK-550).
_RAW = {
    "приёмка: обход": check_prose.ACCEPT_OUTCOME_RE,
    "развилка: подстрока": check_prose.FORK_SUB_RE,
}


def counts(text):
    """Число строк каждого формата в тексте."""
    lines = (text or "").splitlines()
    result = {}
    for name, rx in _STRIPPED.items():
        result[name] = sum(1 for ln in lines if rx.match(ln.strip()))
    for name, rx in _RAW.items():
        result[name] = sum(1 for ln in lines if rx.match(ln))
    return result


def decreases(before, after):
    """Форматы, чей счёт стал меньше: правка либо убрала такую строку, либо
    испортила её так, что регулярка её больше не узнаёт."""
    b, a = counts(before), counts(after)
    return sorted((name, b[name], a[name]) for name in b if a[name] < b[name])


def is_task_file(path):
    return bool(path) and bool(TASK_FILE_RE.search(path.replace(os.sep, "/")))


def before_after(event):
    """(путь, «до», «после») либо None, если событие не про запись файла
    задачи или файла цели. «После» хук читает с диска по filePath, а не из
    фрагмента правки в событии: `hookio.write_event` отдаёт только записанный
    кусок, и при `replaceAll` сравнение куска с целым файлом давало бы ложную
    убыль на любой мелкой правке."""
    if hookio.text_of(event.get("hook_event_name")) != "PostToolUse":
        return None
    if hookio.text_of(event.get("tool_name")) not in ("Edit", "Write", "NotebookEdit"):
        return None
    ti = event.get("tool_input")
    tr = event.get("tool_response")
    if not isinstance(ti, dict) or not isinstance(tr, dict):
        return None
    path = ti.get("file_path") or ti.get("notebook_path")
    if not isinstance(path, str) or not is_task_file(path):
        return None
    before = tr.get("originalFile")
    if not isinstance(before, str):
        before = None
    try:
        with open(path, encoding="utf-8") as f:
            after = f.read()
    except OSError:
        return None
    return path, before, after


def hint(path, found):
    lines = ["машинная строка %s сломана или убрана правкой (рубеж DK-1058, "
             "перечень форматов hooks/check-machine-lines.py и "
             "kit/skills/proofread/machine-lines.md); строку разбирает код "
             "доски, править её посимвольно нельзя, а формат восстанавливать "
             "по образцу до правки" % path]
    for name, was, now in found:
        lines.append("  %s: было %d, стало %d" % (name, was, now))
    return "\n".join(lines) + "\n"


def run_hook(protocol):
    # Протокол сверяется до разбора события, иначе опечатка в settings.json
    # выключила бы рубеж насовсем и молча.
    hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    ba = before_after(event)
    if ba is None:
        return 0
    path, before, after = ba
    if before is None:
        return hookio.context(protocol).say(
            "машинные строки %s не с чем сверить: прежней версии нет (новый "
            "файл или пустой originalFile), рубеж DK-1058 сработает со "
            "следующей правки\n" % path)
    found = decreases(before, after)
    if not found:
        return 0
    return hookio.reply(protocol).found(hint(path, found))


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-machine-lines: %s\n" % e)
            return 2
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
