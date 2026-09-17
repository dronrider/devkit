#!/usr/bin/env python3
"""Сторож выборки prose (DK-1024): PreToolUse-хук на Write, Edit, MultiEdit и
NotebookEdit отбивает запись долгоживущего текста, пока в контексте сессии
нет следа выборки эталонов `prose.py sample`. Отказ называет команду выборки
с жанром по пути.

Долгоживущие пути и их жанр лежат секцией в kit/prose.toml (`[sample-task]`,
`[sample-lld]`, `[sample-readme]`, `[sample-skill]`, ключ `paths`), режим
`block|warn` в `[sample] mode`, умолчание `block`: стенд DK-1022 показал, что
подсказка после записи (тот же текст) вызова у haiku не даёт, 0 из 19.
Список `[sample] exclude` держит машинные файлы (доска, сгенерированная
карта) вне чужого жанра по совпадению суффикса. Путь без жанра из перечня
хук не трогает: лучше отсутствие рубежа на новом каталоге, чем случайный
жанр не в тему.

Рубеж не заменяет hooks/check-prose.py: тот меряет уже написанный кусок
текста от 120 слов шестью приметами и не видит ни абзац короче, ни само
отсутствие выборки, а этот хук стоит раньше записи и смотрит не на текст, а
на то, брался ли корпус (docs/tasks/DK-1024.md, развилка «block у
check-prose»).

След выборки ставит и гасит hooks/prose-mark.py: PostToolUse на Bash по
команде `prose.py sample` ставит его по контексту сессии, SessionStart с
source=compact гасит. Файл следа: ~/.devkit/prose/<контекст>.json, тот же
порядок контекста, что у hooks/check-reread.py (DK-608): у субагента контекст
свой и пустой, выборка диспетчера ему не засчитывается.

Режимы:
  check-prose-sample.py --hook [протокол]
  check-prose-sample.py --config
                   полнота конфига kit/prose.toml: жанры и режим читает сам
                   хук, второй список в devkitctl не заводится
  check-prose-sample.py --hook [протокол] --state <каталог>
                   каталог следа для тестов (умолчание ~/.devkit/prose)
"""
import collections
import fnmatch
import os
import sys

import hookio

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_CONFIG = os.path.join(ROOT, "kit", "prose.toml")
# Тот же ключ, что у check-prose.py и check-calque.py: все три сторожа читают
# один kit/prose.toml, и тесту достаточно подменить путь одной переменной.
CONFIG_ENV = "DEVKIT_PROSE_CONFIG"
STATE_NAME = "prose"

MODES = ("warn", "block")
# Жанры выборки: тот же список, что у kit/skills/prose/prose.py и раздела
# «Кто зовёт» kit/skills/prose/SKILL.md. Список фиксирован кодом, как у
# check-prose.py фиксирован список метрик, а пути под каждый жанр лежат в
# kit/prose.toml.
GENRES = ("task", "lld", "readme", "skill")

Settings = collections.namedtuple("Settings", "path mode exclude genres")


def text_of(value):
    return value if isinstance(value, str) else ""


def config_path():
    return os.environ.get(CONFIG_ENV) or DEFAULT_CONFIG


def read_config(path=None):
    """Настройки из конфига парой (Settings, пробелы), тем же устройством,
    что у check-prose.read_config: пробелы непустые значит, путей нет и
    сравнивать не с чем, своего списка код не выдумывает."""
    path = path or config_path()
    doc, why = hookio.toml_at(path)
    if doc is None:
        return None, ["конфиг путей не прочесть, %s" % why]
    gaps = []
    mode = doc.str_of("sample", "mode")
    if mode not in MODES:
        gaps.append("[sample] mode: жду одно из %s, лежит %s"
                    % (", ".join(MODES), mode or "пусто"))
    exclude = tuple(doc.arr_of("sample", "exclude"))
    genres = {}
    for g in GENRES:
        paths = doc.arr_of("sample-%s" % g, "paths")
        if not paths:
            gaps.append("[sample-%s] paths: жду непустой список путей жанра «%s»" % (g, g))
        else:
            genres[g] = tuple(paths)
    if gaps:
        return None, gaps
    return Settings(path, mode, exclude, genres), []


def relpath(path, root):
    """Путь файла относительно дерева работы, со слешем прямым и без ведущего
    слеша: паттерны в конфиге пишутся от корня проекта, и разбор их не должен
    зависеть от разделителя платформы."""
    if not path:
        return ""
    p = path.replace(os.sep, "/")
    r = (root or "").replace(os.sep, "/").rstrip("/")
    if r and p.startswith(r + "/"):
        return p[len(r) + 1:]
    return p.lstrip("/")


def genre_of(rel, conf):
    """Жанр по относительному пути, None значит, что путь не долгоживущий или
    в исключениях.

    `fnmatch` не считает «/» границей, и широкий паттерн жанра readme
    (`docs/*.md`) формально ловит и `docs/tasks/DK-1.md`, и `docs/lld/x.md`.
    Победитель среди нескольких совпавших паттернов не порядок жанров в
    `GENRES` (это случайно совпадало и держалось только перебором словаря), а
    длина самого паттерна: `docs/tasks/*.md` длиннее и уже, чем `docs/*.md`, и
    берёт верх над ним при любом порядке перечисления жанров в конфиге."""
    if not rel:
        return None
    if any(fnmatch.fnmatch(rel, p) for p in conf.exclude):
        return None
    best_genre, best_pattern = None, ""
    for genre, patterns in conf.genres.items():
        for p in patterns:
            if fnmatch.fnmatch(rel, p) and len(p) > len(best_pattern):
                best_genre, best_pattern = genre, p
    return best_genre


def marked(context, override=None):
    return os.path.exists(hookio.state_path(STATE_NAME, context, override))


def hint(path, genre):
    return ("запись долгоживущего текста без выборки эталонов прозы: возьми "
            "выборку `python3 %s/kit/skills/prose/prose.py sample --genre %s` "
            "и повтори запись; без корпуса проза идёт в шаблон (замер DK-446, "
            "сторож выборки DK-1024). путь: %s\n" % (ROOT, genre, path))


def run_hook(protocol, override=None):
    # Протокол сверяется до разбора события и конфига: незнакомое имя не
    # должно молчать неотличимо от «путь не долгоживущий».
    hookio.entry(protocol)
    conf, gaps = read_config()
    if conf is None:
        # Без путей и режима хук молчит: своего списка он не выдумывает, а
        # про пропавший конфиг говорит доктор находкой.
        return 0
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    write = hookio.parse_write(protocol, event)
    if write is None or not write.path:
        return 0
    session = text_of(event.get("session_id"))
    if not session:
        # Без session_id след ни поставить, ни проверить: пусть запись идёт
        # как есть, лучше пропуск, чем рубеж без адреса.
        return 0
    cwd = text_of(event.get("cwd"))
    root = hookio.tree_root(cwd) or cwd
    genre = genre_of(relpath(write.path, root), conf)
    if genre is None:
        return 0
    context = hookio.context_id(session, text_of(event.get("agent_id")))
    if marked(context, override):
        return 0
    text = hint(write.path, genre)
    if conf.mode == "block":
        return hookio.reply(protocol).found(text)
    return hookio.context(protocol).say(text)


def run_config():
    conf, gaps = read_config()
    if conf is None:
        print("конфиг путей выборки неполон:")
        for g in gaps:
            print("- %s" % g)
        return 1
    print("конфиг путей выборки: %s, режим %s, жанров %d"
          % (conf.path, conf.mode, len(conf.genres)))
    return 0


def main(argv):
    override = None
    args = list(argv)
    if "--state" in args:
        i = args.index("--state")
        if i + 1 < len(args):
            override = args[i + 1]
            del args[i:i + 2]
    if args[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(args[1:]), override)
        except hookio.Unknown as e:
            sys.stderr.write("check-prose-sample: %s\n" % e)
            return 2
    if args[:1] == ["--config"]:
        return run_config()
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
