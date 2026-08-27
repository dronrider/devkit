#!/usr/bin/env python3
"""Сторож прозы: шесть примет агентского текста, посчитанных регулярками.

Приметы взяли из замера 38,5 тыс. слов агентской прозы против человеческого
корпуса (docs/tasks/DK-446.md, раздел «Что показал замер»): длинное
предложение, довод в той же фразе, двоеточие-довод в середине фразы, хвост
«..., а не Y», вывод-афоризм в конце абзаца. Модели проверка не зовёт и
смысла не понимает, она считает частоты и сверяет их с порогами.

Те же приметы стоят первыми пятью пунктами чек-листа вычитки
(kit/skills/proofread/SKILL.md), ключ метрики выписан рядом с пунктом.
Список у сторожа и у вычитки один: сторож меряет плотность по файлу, вычитка
правит саму фразу.

Пороги и режим лежат в конфиге (kit/prose.toml), своих чисел код не держит:
без конфига сторож молчит, а про пропажу говорит `devkitctl doctor`. Путь
конфига переопределяет DEVKIT_PROSE_CONFIG.

Режимы:
  check-prose.py <файл>...       таблица метрик по файлу, выход 1 при
                                 превышении блокирующего порога
  ... | check-prose.py --diff    строки вида файл:строка:текст (staged-дифф
                                 pre-commit): меряются добавленные строки,
                                 сгруппированные по файлу
  check-prose.py --config        разбор конфига: путь, режим, пробелы; выход 1,
                                 если конфига нет или он неполон. Этим режимом
                                 доктор и смотрит на конфиг, чтобы список
                                 ключей жил в одном месте
  check-prose.py --hook [протокол]
                                 хук на запись файла: JSON события на stdin,
                                 меряется записанный фрагмент, а не файл
                                 целиком. Разбор входа и канал ответа берутся
                                 по имени протокола из hookio.py, голый --hook
                                 это claude-code
"""
import collections
import os
import re
import sys

import hookio

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CONFIG_ENV = "DEVKIT_PROSE_CONFIG"
DEFAULT_CONFIG = os.path.join(ROOT, "kit", "prose.toml")
MODES = ("warn", "block")

# Разбор текста. Считается проза, а не разметка: фронтматтер, блоки кода,
# таблицы и заголовки выкидываются, а инлайн-код становится одним словом.
FRONT_RE = re.compile(r"\A---\n.*?\n---\n", re.S)
FENCE_RE = re.compile(r"```.*?```", re.S)
INLINE_RE = re.compile(r"`[^`\n]*`")
TAG_RE = re.compile(r"<[^>\n]+>")
LIST_RE = re.compile(r"(?m)^\s*(?:[-*]|\d+\.)\s+")
SPLIT_RE = re.compile(r"(?<=[.!?;])\s+(?=[А-ЯA-Z«(\d])")
WORD_RE = re.compile(r"[А-Яа-яЁёA-Za-z0-9_'-]+")
# Предложение короче трёх слов это подпись, пункт перечня или обрывок строки, и
# в частотах оно шумит, а не считается.
MIN_SENTENCE = 3
LONG_SENTENCE = 25
# Абзац короче шестнадцати слов концовки не имеет: там одно предложение, и
# любое обобщение в нём это и есть весь абзац.
MIN_PARAGRAPH = 16

COLON_RE = re.compile(r":\s+[а-яё]")
ARGUE_RE = re.compile(r"\b(иначе|потому|чтобы|а не)\b", re.I)
# Хвост это запятая, «а не» и договорённое отрицание. Без запятой «а не»
# союз, а не хвост: так пишут имя самой метрики («хвост «а не»») и вопрос
# «а не проще ли».
TAIL_RE = re.compile(r",\s+а не\b", re.I)
# Кавычки и обратные апострофы: внутри лежит чужая фраза или имя приметы, и
# шаблон автора там не меряется. Инлайн-код разбор прозы снимает раньше, а
# апострофы тут стоят для прямого вызова счётчика.
QUOTED_RE = re.compile(r"«[^»\n]*»|\"[^\"\n]*\"|`[^`\n]*`")
# Полная форма «не X, а не Y» это лексика пользователя: DK-524 сняла её из
# довода, и хвостом она тоже не считается.
FULL_FORM_RE = re.compile(r"\bне [^,.;:]{1,40}, а не\b", re.I)
FINAL_RE = re.compile(r"(это и есть|то есть|значит|и есть|вот и|ровно то|"
                      r"в этом и|именно)", re.I)

# Машинные записи файла задачи: их дописывает код, а не человек, и двоеточие
# в них стоит по формату, а не как довод (DK-550). В счёт метрик прозы они не
# идут, ровно как таблицы и заголовки.
#
# «Ход работы»: единственное место записи это internal/stage.Lines
# (tools/taskctl/stage.go: flushStages зовёт его на каждый перевод статуса),
# строка вида «- <Метка>: <заметка>, <дата> <часы>[-<часы>].». Метка это Kind
# с заглавной буквы: Разработка и Ревью заметку строит tools/agentctl/pick.go
# (recordStage), Снаружи собирает сам tools/taskctl/stage.go (openOutside),
# Уточнение собирает tools/taskctl/ask.go. Формат общий для всех четырёх, и
# разбирать их разными регулярками смысла нет.
STAGE_LABELS = ("Разработка", "Ревью", "Снаружи", "Уточнение")
STAGE_LINE_RE = re.compile(
    r"^-\s+(?:%s):\s.*,\s*\d{4}-\d{2}-\d{2}\s\d{2}:\d{2}(?:-\d{2}:\d{2})?\.\s*$"
    % "|".join(STAGE_LABELS))

# «Ранг»: TASKFORM.md (раздел «Ранг» в таблице форм) требует по строке на
# слагаемое формулы, имена слагаемых из RANKING.md, вида
# «- <Слагаемое> <число>: <причина>.».
RANK_WORDS = ("Серьёзность", "Ценность", "Неопределённость",
              "Поправка на баг", "Рычаг")
RANK_LINE_RE = re.compile(r"^-\s+(?:%s)\s+\d+:\s" % "|".join(RANK_WORDS))

# «Выкат»: shipctl пишет в файл задачи напрямую, минуя taskctl
# (tools/shipctl/record.go). recordMerge дописывает строку слитых коммитов
# «- <дата> слито: <sha>[, <sha>...]» (record.go:150), cmdSmoke дописывает
# отметку прогона «- smoke прогнан, <дата>» (record.go:243, smokeNote). Обе
# строки без точки на конце, как их строит сам код.
DEPLOY_MERGE_RE = re.compile(
    r"^-\s+\d{4}-\d{2}-\d{2}\s+слито:\s+[0-9a-f]{7,}(?:,\s*[0-9a-f]{7,})*\s*$")
DEPLOY_SMOKE_RE = re.compile(r"^-\s+smoke прогнан,\s*\d{4}-\d{2}-\d{2}\s*$")

# «Приёмка»: формат ACCEPTANCE.md, те же регулярки, что у самого taskctl
# (tools/taskctl/accept.go: acceptBarrierLineRe, acceptBypassRe;
# tools/taskctl/ops.go: шаблон «- вид: %s»; tools/taskctl/kinds.go:
# revisionLineRe): строка вида и строка барьера стоят верхним уровнем,
# строка обхода с исходом «годится» или «не годится» стоит только вложенным
# пунктом под барьером, ровно двумя пробелами (acceptBypassRe у taskctl
# требует тот же отступ). Отступ и отличает машинную запись обхода от
# примеров того же синтаксиса, которые ACCEPTANCE.md приводит для читателя
# верхним уровнем (DK-550, замечание ревью).
ACCEPT_KIND_RE = re.compile(r"^-\s+вид:\s")
ACCEPT_BARRIER_RE = re.compile(r"^-\s+барьер\s+«[^»]*»:")
ACCEPT_OUTCOME_RE = re.compile(r"^  - .*:\s*(?:не\s+)?годится\b")

# Регулярки верхнего уровня разбирают строку после strip(): у настоящей
# записи отступа нет, а strip() не портит хвост. ACCEPT_OUTCOME_RE особняком:
# ему нужен исходный, не обрезанный отступ, чтобы отличить вложенный обход от
# такого же текста без вложенности.
TOP_LEVEL_MACHINE_RES = (STAGE_LINE_RE, RANK_LINE_RE, DEPLOY_MERGE_RE,
                          DEPLOY_SMOKE_RE, ACCEPT_KIND_RE, ACCEPT_BARRIER_RE)


def is_machine_line(line):
    """Строка файла задачи, которую дописывает утилита, а не человек."""
    s = line.strip()
    if any(r.match(s) for r in TOP_LEVEL_MACHINE_RES):
        return True
    return bool(ACCEPT_OUTCOME_RE.match(line))


Text = collections.namedtuple("Text", "words sentences paragraphs")
# Метрика: ключ в конфиге, как называется в выводе, единица и подсказка, чем
# такую фразу переписывают. Порядок перечня это порядок строк отчёта.
Metric = collections.namedtuple("Metric", "key title unit measure advice")
Settings = collections.namedtuple("Settings", "path mode min_words suffixes warn block")
# Находка: метрика, посчитанное значение, порог, который она перешла, и уровень
# («предупреждение» или «блокировка»).
Found = collections.namedtuple("Found", "metric value limit level")

WARN, BLOCK = "предупреждение", "блокировка"


def prose(text):
    """Проза без разметки: тем же порядком, что в замере цели DK-446."""
    text = FRONT_RE.sub("", text)
    text = FENCE_RE.sub("", text)
    text = INLINE_RE.sub("CODE", text)
    lines = [ln for ln in text.split("\n")
             if not ln.strip().startswith("|") and not ln.strip().startswith("#")
             and not is_machine_line(ln)]
    return TAG_RE.sub("", "\n".join(lines))


def sentences(chunk):
    """Предложения куска прозы парами (текст, число слов)."""
    chunk = re.sub(r"\n\s*\n", "\n", chunk)
    chunk = LIST_RE.sub("", chunk)
    out = []
    for part in SPLIT_RE.split(chunk.replace("\n", " ")):
        words = WORD_RE.findall(part)
        if len(words) >= MIN_SENTENCE:
            out.append((part.strip(), len(words)))
    return out


def read_text(raw):
    """Разбор куска в счётный вид: предложения, слова, абзацы."""
    body = prose(raw)
    sents = sentences(body)
    paras = []
    for p in re.split(r"\n\s*\n", body):
        if len(WORD_RE.findall(p)) > MIN_PARAGRAPH:
            paras.append(sentences(p))
    return Text(sum(n for _, n in sents), sents, paras)


def share(part, whole):
    return 100.0 * part / whole if whole else 0.0


def per_1000(count, words):
    return 1000.0 * count / words if words else 0.0


def avg_len(t):
    return t.words / len(t.sentences) if t.sentences else 0.0


def long_share(t):
    return share(sum(1 for _, n in t.sentences if n > LONG_SENTENCE),
                 len(t.sentences))


def argued(sent):
    """Довод в той же фразе: причина, цель или двоеточие.

    Полная форма «не X, а Y» за довод не считается. Замер DK-446 дал у неё
    1,5 на тысячу слов у агентов против 2,3 у людей: это лексика
    пользователя, и счёт её доводом красил его же фразу (DK-524).
    """
    return bool(ARGUE_RE.search(sent) or COLON_RE.search(sent))


def argue_share(t):
    return share(sum(1 for s, _ in t.sentences if argued(s)), len(t.sentences))


def colon_rate(t):
    return per_1000(sum(1 for s, _ in t.sentences if COLON_RE.search(s)), t.words)


def tails(sent):
    """Хвосты «..., а не Y» в предложении, без цитат и без полной формы."""
    body = QUOTED_RE.sub(" ", sent)
    body = FULL_FORM_RE.sub(" ", body)
    return len(TAIL_RE.findall(body))


def tail_rate(t):
    return per_1000(sum(tails(s) for s, _ in t.sentences), t.words)


def final_share(t):
    ends = sum(1 for p in t.paragraphs if p and FINAL_RE.search(p[-1][0]))
    return share(ends, len(t.paragraphs))


METRICS = (
    Metric("sentence_len", "средняя длина предложения", "слов", avg_len,
           "резать надвое: факт отдельной фразой, довод отдельной"),
    Metric("long_sentences", "предложений длиннее 25 слов", "%", long_share,
           "длинное предложение разбирается на два-три коротких, "
           "придаточные становятся своими фразами"),
    Metric("argued", "довод в той же фразе", "%", argue_share,
           "убрать «иначе», «потому», «чтобы»: сказать, что происходит, "
           "а причину вынести в соседнее предложение или не писать вовсе"),
    Metric("colon_mid", "двоеточие в середине фразы", "на 1000 слов", colon_rate,
           "двоеточие-довод меняется на точку, вторая половина живёт "
           "самостоятельной фразой"),
    Metric("but_not_tail", "хвост «..., а не Y»", "на 1000 слов", tail_rate,
           "сказать, что есть, и не договаривать, чего нет"),
    Metric("aphorism", "абзац кончается обобщением", "%", final_share,
           "снять последнее предложение абзаца: факты уже сказаны выше"),
)
BY_KEY = {m.key: m for m in METRICS}


def int_at(doc, section, key):
    v = doc.get(section, key)
    if v is None or not isinstance(v.val, int) or isinstance(v.val, bool):
        return None
    return v.val


def config_path():
    return os.environ.get(CONFIG_ENV) or DEFAULT_CONFIG


def read_config(path=None):
    """Настройки из конфига парой (Settings, пробелы).

    Пробел это строка про то, чего в конфиге не хватает. Пробелы непустые
    значит, порогов нет и мерить нечем: своих чисел код не держит.
    """
    path = path or config_path()
    doc, why = hookio.toml_at(path)
    if doc is None:
        return None, ["порогов не прочесть, %s" % why]
    gaps = []
    mode = doc.str_of("prose", "mode")
    if mode not in MODES:
        gaps.append("[prose] mode: жду одно из %s, лежит %s"
                    % (", ".join(MODES), mode or "пусто"))
    min_words = int_at(doc, "prose", "min_words")
    if min_words is None:
        gaps.append("[prose] min_words: жду целое, столько слов нужно куску, "
                    "чтобы частоты в нём что-то значили")
    suffixes = doc.arr_of("prose", "suffixes")
    if not suffixes:
        gaps.append("[prose] suffixes: жду массив расширений, которые считаются "
                    "прозой")
    warn, block = {}, {}
    for m in METRICS:
        for section, into in (("warn", warn), ("block", block)):
            v = int_at(doc, section, m.key)
            if v is None:
                gaps.append("[%s] %s: жду целое, порог метрики «%s»"
                            % (section, m.key, m.title))
            else:
                into[m.key] = v
    if gaps:
        return None, gaps
    return Settings(path, mode, min_words, tuple(suffixes), warn, block), []


def measure(raw):
    """Значения всех метрик по куску текста, ключ метрики -> число."""
    t = read_text(raw)
    return t, {m.key: m.measure(t) for m in METRICS}


def findings(values, conf):
    """Перешедшие порог метрики, блокирующие раньше предупреждающих."""
    out = []
    for m in METRICS:
        v = values[m.key]
        if v > conf.block[m.key]:
            out.append(Found(m, v, conf.block[m.key], BLOCK))
        elif v > conf.warn[m.key]:
            out.append(Found(m, v, conf.warn[m.key], WARN))
    return out


def number(v):
    return ("%.1f" % v).replace(".", ",")


def line_of(f):
    return "%s: %s %s при пороге %d (%s)" % (
        f.metric.title, number(f.value), f.metric.unit, f.limit, f.level)


def report(found, where, words):
    head = ("проза сбивается на агентский шаблон (DK-446, «Что показал замер») "
            "в %s, слов %d:" % (where or "?", words))
    body = "\n".join(line_of(f) for f in found)
    how = "\n".join("- %s" % f.metric.advice for f in found)
    return "%s\n%s\nкак переписать:\n%s\n" % (head, body, how)


def is_prose(path, conf):
    # Корпус образцов меряется не сам по себе: в сторожевом корпусе вычитки
    # плохие фразы лежат нарочно, и находка на них это не поломка текста.
    if not path:
        return False
    parts = path.split("/")
    if "testdata" in parts or "corpus" in parts or parts[-1] == "corpus.md":
        return False
    return any(path.endswith(s) for s in conf.suffixes)


def run_hook(protocol):
    # Разбор события идёт раньше конфига: незнакомый протокол это отказ со
    # словами, и молчаливым «конфига нет» его подменять нельзя.
    write = hookio.write_event(protocol)
    conf, gaps = read_config()
    if conf is None:
        # Без порогов сторож молчит: он не выдумывает своих чисел, а про
        # пропавший конфиг говорит доктор находкой.
        return 0
    if write is None or not is_prose(write.path, conf):
        return 0
    t, values = measure("\n".join(write.chunks))
    if t.words < conf.min_words:
        return 0
    found = findings(values, conf)
    if not found:
        return 0
    text = report(found, write.path, t.words)
    if conf.mode == "block" and any(f.level == BLOCK for f in found):
        return hookio.reply(protocol).found(text)
    return hookio.context(protocol).say(text)


def run_files(paths):
    conf, gaps = read_config()
    if conf is None:
        for g in gaps:
            sys.stderr.write("check-prose: %s\n" % g)
        return 2
    worst = 0
    for path in paths:
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                raw = f.read()
        except OSError as e:
            sys.stderr.write("check-prose: %s\n" % e)
            return 2
        t, values = measure(raw)
        print("%s: слов %d, предложений %d, абзацев %d"
              % (path, t.words, len(t.sentences), len(t.paragraphs)))
        for m in METRICS:
            over = ""
            if values[m.key] > conf.block[m.key]:
                over = "  порог блокировки %d перейдён" % conf.block[m.key]
            elif values[m.key] > conf.warn[m.key]:
                over = "  порог предупреждения %d перейдён" % conf.warn[m.key]
            print("  %s: %s %s%s" % (m.title, number(values[m.key]), m.unit, over))
        if conf.mode == "block" and any(f.level == BLOCK
                                        for f in findings(values, conf)):
            worst = 1
    return worst


def run_diff():
    """Добавленные строки коммита, сгруппированные по файлу.

    Меряется только добавленное: частота в чужом существующем тексте это не то,
    что автор коммита написал. Выход 1 это блокировка, вывод при выходе 0 это
    предупреждение, и pre-commit различает их именно так.
    """
    conf, gaps = read_config()
    if conf is None:
        return 0
    added = collections.OrderedDict()
    for raw in sys.stdin:
        parts = raw.rstrip("\n").split(":", 2)
        if len(parts) < 3:
            continue
        added.setdefault(parts[0], []).append(parts[2])
    blocked = 0
    for path, lines in added.items():
        if not is_prose(path, conf):
            continue
        t, values = measure("\n".join(lines))
        if t.words < conf.min_words:
            continue
        found = findings(values, conf)
        if not found:
            continue
        print(report(found, path, t.words))
        if conf.mode == "block" and any(f.level == BLOCK for f in found):
            blocked = 1
    return blocked


def run_config():
    conf, gaps = read_config()
    if conf is None:
        print("конфиг порогов прозы неполон:")
        for g in gaps:
            print("- %s" % g)
        return 1
    print("конфиг порогов прозы: %s, режим %s, кусок от %d слов, проза %s"
          % (conf.path, conf.mode, conf.min_words, " ".join(conf.suffixes)))
    return 0


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-prose: %s\n" % e)
            return 2
    if argv[:1] == ["--config"]:
        return run_config()
    if argv[:1] == ["--diff"]:
        return run_diff()
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    return run_files(argv)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
