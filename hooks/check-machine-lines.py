#!/usr/bin/env python3
"""Рубеж машинных строк файла задачи (DK-1058): PostToolUse-хук на
Edit|Write|NotebookEdit сверяет `docs/tasks/<ID>.md` по сторонам события
записи (тот же приём, что решение 4 LLD DK-606 отвело под сторожа разметки:
`tool_response.originalFile` это «до», файл на диске по `filePath` это
«после», восстанавливать «после» из фрагмента правки нельзя, `replaceAll`
даёт не тот текст). Правка, убравшая или сломавшая строку, которую разбирает
код доски, отбивается сразу и цитирует пропавшую строку; новая строка того же
формата, перестановка старых строк и правка живой прозы проходят молча.

Сверка идёт по точному тексту строки, а не по счёту совпадений с регуляркой.
Первая редакция (ревью нашло это находкой) сравнивала только число строк
каждого формата, и содержательная порча проходила молча всюду, где регулярка
матчится по обеим сторонам: смена числа в квоте строки этапа при целом
хвосте, подмена sha и текста после головы вердикта, подмена коммита и числа
шагов в строке обкатки, переписанная суть замечания ревью, замена одной даты
на другую в строке судьбы MR при счёте один к одному. Регулярка здесь только
классифицирует строку («это формат такой-то»), а сравниваются множества
точных строк каждого формата по обеим сторонам с учётом кратности: строка,
которая была и дословно пропала, это находка, новая строка и перестановка
прежних находкой не считаются.

Перечень форматов не свой: бОльшая часть регулярок взята без копирования из
`check-prose.py` (`TOP_LEVEL_MACHINE_RES`, `ACCEPT_OUTCOME_RE`, `FORK_SUB_RE`,
`REVIEW_LEVEL_HEAD_RE`, `REVIEW_VERDICT_HEAD_RE`) динамической загрузкой
модуля: оба рубежа читают один и тот же список, а не две копии, которые
расходятся сами по себе. Форматы, которых у сторожа прозы нет, потому что для
плотности прозы они не нужны, дописаны здесь по коду: судьба чужого MR
(`tools/taskctl/reviewpoll.go`, `reviewFateNote`/`appendReviewStage`), журнал
цели (`tools/agentctl/goal.go`, снимок квоты и виток), ссылка чужого ревью
(`tools/trackctl/review.go`, `fillReviewFile`) и строка замечания ревью
(`tools/taskctl/review.go`, `cmdReviewAdd`; `tools/taskctl/retclass.go`,
`returnNoteMark`): голова с ярусом («блокирует:»/«не блокирует:», термин
самого скилла review) и необязательной пометкой класса возврата, дальше суть
и исход (`: исправлено` либо `: отклонено, причина`), строка неприкосновенна
целиком, тем же порядком, что голова вердикта и уровня.
`kit/skills/proofread/machine-lines.md` ссылается на этот файл, а не держит
перечень прозой второй раз.

Загрузка `check-prose.py` обёрнута: файла нет или он не грузится (например,
битым состоянием на середине чужой правки), рубеж не падает трассировкой, а
говорит об этом строкой добавки к контексту и не проверяет запись в этом
заходе. Молчание тут было бы хуже находки: агент решил бы, что правка чистая,
хотя её никто не смотрел.

Матчер `Edit|Write|NotebookEdit` не включает `MultiEdit` (`POST_MATCHER` в
`tools/devkitctl/devkitctl.py`), тем же порядком, что у `check-prose.py`, и
третья правка того же абзаца `MultiEdit`-ом мимо этого рубежа тоже проходит.
Правка вне харнеса (коллега редактирует файл текстовым редактором на другой
машине) сюда не входит: DoD задачи называет условием только правку руками
внутри Claude Code, мимо скилла `proofread`, а не вовсе без него.

Строку правит только утилита доски (taskctl, agentctl, shipctl) внешним
процессом, и её запись не создаёт события Edit|Write|NotebookEdit вовсе:
рубеж эту запись физически не видит, поэтому легальный пересчёт квоты, новый
круг ревью с другим sha или новая дата у судьбы MR не долетают до сравнения
как «до» и «после» одного и того же вызова. Найденную пропажу внутри одного
Edit|Write|NotebookEdit вызова агента чинит только возврат строки дословно
(цитата в находке) и утилита для содержательной смены, а не правка руками.

Режимы:
  check-machine-lines.py --hook [протокол]
                   хук на запись файла: JSON события на stdin, разбор входа и
                   канал ответа по имени протокола из hookio.py, голый --hook
                   это claude-code
"""
import collections
import importlib.util
import os
import re
import sys

import hookio

HERE = os.path.dirname(os.path.abspath(__file__))
CHECK_PROSE_PATH = os.path.join(HERE, "check-prose.py")


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise ImportError("%s не найден" % path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


# Дефис в имени файла не даёт обычный import, поэтому модуль грузится по пути
# (тот же приём, что в check_prose_test.py). Загрузка идёт при каждом запуске
# хука отдельным процессом: чужого состояния между вызовами нет. Ошибка любого
# рода (файла нет, синтаксис битый на середине чужой правки, исключение внутри
# модуля) не роняет этот хук: она откладывается до run_hook, где превращается
# в строку добавки к контексту, а не в трассировку.
try:
    check_prose = _load("check_prose_shared_dk1058", CHECK_PROSE_PATH)
    CHECK_PROSE_ERROR = None
except Exception as e:  # noqa: BLE001, любая причина загрузки идёт находкой, не трассировкой
    check_prose = None
    CHECK_PROSE_ERROR = "%s: %s" % (type(e).__name__, e)

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

# Строка замечания ревью (tools/taskctl/review.go: cmdReviewAdd вставляет
# "- " + returnNoteMark(class, now) + note; tools/taskctl/retclass.go:
# returnNoteMark кладёт "[возврат: <класс>, <дата>] "). Ярус, «блокирует:»
# либо «не блокирует:», это слово самого текста замечания, не код (SKILL.md
# скилла review, раздел про ярус), но реальные записи несут его всегда, и без
# слова-яруса строка от обычного пункта DoD не отличается вовсе. Исход
# (": исправлено" либо ": отклонено, причина") дописывает resolve в хвост той
# же строки и в классификацию не входит: хвост часть той же дословной строки.
REVIEW_FINDING_RE = re.compile(
    r"^-\s+(?:\[возврат:[^\]]*\]\s*)?(?:не\s+)?блокирует:\s")


# Категория несёт классификатор (какая строка сюда попадает) и ключ
# множества (чем строка отличается от другой строки того же формата).
# Классификатор для «сырых» категорий (ACCEPT_OUTCOME_RE, FORK_SUB_RE) матчит
# исходную строку без strip(), потому что отступ слева отличает вложенный
# пункт развилки или приёмки от такого же синтаксиса примером верхним уровнем
# (DK-550), и ключ там тоже исходная строка целиком: подрезать пробел справа
# незачем, отступ и есть предмет проверки. У остальных категорий классификатор
# матчит `ln.strip()`, а ключ это `ln.rstrip()`: хвостовой пробел или перенос
# `\r`, который редактор или модель добавляют или снимают отдельно от правки
# содержимого, не должен выглядеть как убранная и невесть откуда взявшаяся
# строка (замечание ревью второго круга).
Category = collections.namedtuple("Category", "test key")


def _stripped(rx):
    return Category(lambda ln: bool(rx.match(ln.strip())), str.rstrip)


def _raw(rx):
    return Category(lambda ln: bool(rx.match(ln)), lambda ln: ln)


def _own_categories():
    """Форматы, которых у check-prose.py нет: не зависят от его загрузки."""
    return {
        "ход работы: судьба MR": _stripped(MR_FATE_RE),
        "журнал: снимок": _stripped(GOAL_SNAP_RE),
        "журнал: виток": _stripped(GOAL_LAP_RE),
        "что происходит: ссылка ревью": _stripped(REVIEW_LINK_RE),
        "ревью: замечание": _stripped(REVIEW_FINDING_RE),
    }


def _shared_categories(cp):
    """Форматы, взятые импортом check-prose.py. cp это загруженный модуль."""
    stripped = {
        "ход работы: этап": cp.STAGE_LINE_RE,
        "ранг": cp.RANK_LINE_RE,
        "выкат: слияние": cp.DEPLOY_MERGE_RE,
        "выкат: smoke": cp.DEPLOY_SMOKE_RE,
        "выкат: перевод отбит": cp.DEPLOY_PENDING_RE,
        "выкат: перевод доведён": cp.DEPLOY_MOVE_DONE_RE,
        "приёмка: вид": cp.ACCEPT_KIND_RE,
        "приёмка: барьер": cp.ACCEPT_BARRIER_RE,
        "развилка: голова": cp.FORK_HEAD_RE,
        "проверка: стенд": cp.STAND_RE,
        "проверка: обкатка": cp.REHEARSAL_RE,
        "ход работы: вычитка": cp.PROOFREAD_RE,
        "ход работы: сторож прозы": cp.PROSE_MARK_RE,
        "ход работы: исключение": cp.EXCEPTION_RE,
        "ход работы: возврат": cp.RETURN_RE,
        "ревью: уровень": cp.REVIEW_LEVEL_HEAD_RE,
        "ревью: вердикт": cp.REVIEW_VERDICT_HEAD_RE,
    }
    raw = {
        "приёмка: обход": cp.ACCEPT_OUTCOME_RE,
        "развилка: подстрока": cp.FORK_SUB_RE,
    }
    out = {name: _stripped(rx) for name, rx in stripped.items()}
    out.update((name, _raw(rx)) for name, rx in raw.items())
    return out


def category_tests():
    """Имя формата -> Category(test, key). check-prose.py недоступен, тогда
    возвращает только собственные форматы, а вызывающий сам решает, что
    сверка неполна (см. CHECK_PROSE_ERROR)."""
    tests = dict(_own_categories())
    if check_prose is not None:
        tests.update(_shared_categories(check_prose))
    return tests


def line_counts(text, category):
    return collections.Counter(category.key(ln) for ln in (text or "").splitlines()
                               if category.test(ln))


def missing(before, after):
    """Строки прежней версии, которых дословно (с учётом кратности и ключа
    формата) нет в новой, по каждому формату. Возврат: список (формат,
    строка, было, осталось), осталось всегда меньше было."""
    out = []
    for name, category in category_tests().items():
        b = line_counts(before, category)
        a = line_counts(after, category)
        for line, was in b.items():
            still = a.get(line, 0)
            if still < was:
                out.append((name, line, was, still))
    out.sort()
    return out


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
             "kit/skills/proofread/machine-lines.md). Строку разбирает код "
             "доски, посимвольная правка не годится: верни её дословно "
             "(цитата ниже), а нужную смену делай утилитой доски (taskctl, "
             "agentctl или shipctl), не правкой руками" % path]
    for name, line, was, still in found:
        lines.append("  %s: строка «%s» была %d раз, осталась %d"
                     % (name, line, was, still))
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
    if CHECK_PROSE_ERROR is not None:
        return hookio.context(protocol).say(
            "сверка машинных строк %s не выполнена: hooks/check-prose.py не "
            "загрузился (%s), рубеж DK-1058 эту запись не проверил\n"
            % (path, CHECK_PROSE_ERROR))
    if before is None:
        return hookio.context(protocol).say(
            "машинные строки %s не с чем сверить: прежней версии нет (новый "
            "файл или пустой originalFile), рубеж DK-1058 сработает со "
            "следующей правки\n" % path)
    found = missing(before, after)
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
