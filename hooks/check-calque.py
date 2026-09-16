#!/usr/bin/env python3
"""Сторож калек: слово из списка вне обратных кавычек получает замену.

Калька это слово, только что переведённое с английского имени: «сплайс» из
splice, «тестовый регион» из test region, «продакшн-часть» из production.
Читатель, который не видел оригинала, спотыкается и идёт спрашивать автора
(docs/tasks/DK-1022.md).

Признака по форме у сторожа нет. Латиница через дефис с кириллицей даёт по
.md репозитория 2250 совпадений, и в верхушке ни одной кальки: tmux-сессия,
git-хуки, headless-браузер. Границу между калькой и устоявшимся термином
держит список слов, а не регулярка: список лежит в конфиге (kit/prose.toml,
секции [calque] и [calque-words]), своих слов код не держит.

Статья списка несёт все написания слова через запятую и замену после «->»:
латиницу, транслит и составное через дефис. Звёздочка на конце написания это
любое окончание, без неё написание ищется целым словом. Слово в обратных
кавычках это код, и находкой оно не считается; слово из словаря проекта
(kit/skills/proofread/dictionary.md) это термин, и его сторож не трогает.

Режимы:
  check-calque.py <файл>...      находки по файлу со строками, выход 1 при
                                 режиме block
  check-calque.py --config       разбор конфига: путь, режим, статьи; выход 1,
                                 если конфига нет или он неполон. Этим режимом
                                 смотрит на конфиг доктор
  check-calque.py --hook [протокол]
                                 хук на запись файла: JSON события на stdin,
                                 смотрится записанный фрагмент, а не файл
                                 целиком. Разбор входа и канал ответа берутся
                                 по имени протокола из hookio.py, голый --hook
                                 это claude-code
"""
import os
import re
import sys

import hookio

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CONFIG_ENV = "DEVKIT_PROSE_CONFIG"
DEFAULT_CONFIG = os.path.join(ROOT, "kit", "prose.toml")
DICTIONARY = os.path.join(ROOT, "kit", "skills", "proofread", "dictionary.md")
SECTION = "calque"
WORDS = "calque-words"
MODES = ("warn", "block")

FRONT_RE = re.compile(r"\A---\n.*?\n---\n", re.S)
FENCE_RE = re.compile(r"\A\s*(```|~~~)")
INLINE_RE = re.compile(r"`[^`\n]*`")
LETTER = "0-9A-Za-zА-Яа-яЁё"
# Дефис границей считается: «production-часть» это то самое составное слово,
# ради которого список и заводился.
LEFT = "(?<![%s])" % LETTER
RIGHT = "(?![%s])" % LETTER
TAIL = "[А-Яа-яЁёA-Za-z]*"
# Первая колонка таблицы словаря: строка вида «| термин | значение |».
TERM_RE = re.compile(r"\A\|\s*([^|]+?)\s*\|")


class Article(object):
    """Статья списка: написания одного слова и замена ему."""

    def __init__(self, key, spellings, replace):
        self.key = key
        self.spellings = spellings
        self.replace = replace
        self.patterns = [(s, re.compile(pattern_of(s), re.I))
                         for s in spellings]


class Settings(object):
    def __init__(self, path, mode, suffixes, articles):
        self.path = path
        self.mode = mode
        self.suffixes = suffixes
        self.articles = articles


class Found(object):
    def __init__(self, article, text, line, source):
        self.article = article
        self.text = text
        self.line = line
        self.source = source


def pattern_of(spelling):
    """Регулярка написания: слова через пробел, звёздочка это окончание."""
    parts = []
    for word in spelling.split():
        star = word.endswith("*")
        body = re.escape(word[:-1] if star else word)
        parts.append(body + TAIL if star else body + RIGHT)
    return LEFT + r"[\s-]+".join(parts)


def config_path():
    return os.environ.get(CONFIG_ENV) or DEFAULT_CONFIG


def parse_article(key, value):
    """Статья из строки конфига либо (None, пробел)."""
    head, arrow, replace = value.partition("->")
    if not arrow or not replace.strip():
        return None, ("[%s] %s: жду написания, «->» и замену, лежит %s"
                      % (WORDS, key, value or "пусто"))
    spellings = [s.strip() for s in head.split(",") if s.strip()]
    if not spellings:
        return None, ("[%s] %s: до «->» нет ни одного написания" % (WORDS, key))
    return Article(key, spellings, replace.strip()), ""


def read_config(path=None):
    """Настройки из конфига парой (Settings, пробелы).

    Пробел это строка про то, чего в конфиге не хватает. Пробелы непустые
    значит, списка нет и искать нечего: своих слов код не держит.
    """
    path = path or config_path()
    doc, why = hookio.toml_at(path)
    if doc is None:
        return None, ["списка калек не прочесть, %s" % why]
    gaps = []
    mode = doc.str_of(SECTION, "mode")
    if mode not in MODES:
        gaps.append("[%s] mode: жду одно из %s, лежит %s"
                    % (SECTION, ", ".join(MODES), mode or "пусто"))
    suffixes = doc.arr_of(SECTION, "suffixes")
    if not suffixes:
        gaps.append("[%s] suffixes: жду массив расширений, которые считаются "
                    "прозой" % SECTION)
    table = doc.table(WORDS) or {}
    articles = []
    for key in table:
        art, gap = parse_article(key, doc.str_of(WORDS, key))
        if art is None:
            gaps.append(gap)
        else:
            articles.append(art)
    if not articles:
        gaps.append("[%s]: жду хотя бы одну статью вида "
                    "«написание, написание -> замена»" % WORDS)
    if gaps:
        return None, gaps
    return Settings(path, mode, tuple(suffixes), tuple(articles)), []


def dictionary_terms(path=None):
    """Термины словаря проекта: их сторож считает своими и не трогает."""
    terms = set()
    try:
        with open(path or DICTIONARY, encoding="utf-8") as f:
            body = f.read()
    except OSError:
        return terms
    for line in body.split("\n"):
        m = TERM_RE.match(line.strip())
        if not m:
            continue
        term = m.group(1).strip().strip("`").lower()
        if term and term not in ("термин", "---"):
            terms.add(term)
    return terms


def prose_lines(text):
    """Строки прозы парами (номер, текст): код из них вынут.

    Фронтматтер, блоки кода и код в обратных кавычках снимаются, а номер
    строки остаётся прежним: по нему автор находит место в файле.
    """
    text = FRONT_RE.sub(lambda m: "\n" * m.group(0).count("\n"), text)
    out = []
    fence = ""
    for i, line in enumerate(text.split("\n"), 1):
        m = FENCE_RE.match(line)
        if fence:
            if m and m.group(1) == fence:
                fence = ""
            continue
        if m:
            fence = m.group(1)
            continue
        out.append((i, INLINE_RE.sub(" ", line)))
    return out


def findings(text, conf, terms=None):
    """Кальки в куске текста, по строке на каждое найденное написание."""
    terms = dictionary_terms() if terms is None else terms
    out = []
    for line, body in prose_lines(text):
        for art in conf.articles:
            for spelling, rx in art.patterns:
                for m in rx.finditer(body):
                    hit = m.group(0)
                    if hit.lower() in terms:
                        continue
                    out.append(Found(art, hit, line, body.strip()))
    return out


def report(found, where):
    head = ("кальки в %s, слово переведено с английского имени и без "
            "оригинала непонятно (DK-1022):" % (where or "?"))
    body = []
    for f in found:
        body.append("%d: «%s», замена: %s" % (f.line, f.text, f.article.replace))
    how = ("как переписать: поставить замену либо ввести слово один раз с "
           "расшифровкой и статьёй в kit/skills/proofread/dictionary.md, имя "
           "флага и команды пишется в обратных кавычках")
    return "%s\n%s\n%s\n" % (head, "\n".join(body), how)


def is_prose(path, conf):
    # Образцы не смотрятся: в сторожевом корпусе вычитки плохие слова лежат
    # нарочно, и находка на них это не поломка текста.
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
        # Без списка сторож молчит, а про пропажу говорит `devkitctl doctor`.
        return 0
    if write is None or not is_prose(write.path, conf):
        return 0
    found = findings("\n".join(write.chunks), conf)
    if not found:
        return 0
    text = report(found, write.path)
    if conf.mode == "block":
        return hookio.reply(protocol).found(text)
    return hookio.context(protocol).say(text)


def run_files(paths):
    conf, gaps = read_config()
    if conf is None:
        for g in gaps:
            sys.stderr.write("check-calque: %s\n" % g)
        return 2
    terms = dictionary_terms()
    worst = 0
    for path in paths:
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                raw = f.read()
        except OSError as e:
            sys.stderr.write("check-calque: %s\n" % e)
            return 2
        found = findings(raw, conf, terms)
        if not found:
            print("%s: калек нет" % path)
            continue
        print(report(found, path))
        if conf.mode == "block":
            worst = 1
    return worst


def run_config():
    conf, gaps = read_config()
    if conf is None:
        print("список калек неполон:")
        for g in gaps:
            print("- %s" % g)
        return 1
    print("список калек: %s, режим %s, статей %d, проза %s"
          % (conf.path, conf.mode, len(conf.articles), " ".join(conf.suffixes)))
    return 0


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-calque: %s\n" % e)
            return 2
    if argv[:1] == ["--config"]:
        return run_config()
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    return run_files(argv)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
