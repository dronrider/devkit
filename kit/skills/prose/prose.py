#!/usr/bin/env python3
"""Корпус эталонов прозы: сборщик реплик пользователя и выборка фрагментов.

  prose.py collect [--journals DIR] [--min-words N] [--out DIR]
  prose.py sample [--genre ЖАНР] [--count N] [--words N] [--seed S] [--corpus DIR]
  prose.py repocheck [--repo DIR] [--corpus DIR] [--dump FILE]

`collect` режет журналы сессий на реплики роли user, отсеивает короткие,
служебные и перенесённые copy-paste черновики агента и складывает словарь.
Выгрузка не коммитится: это личные тексты, в корпус из них едет только
вычитанное пользователем.

`sample` читает корпус из `corpus/` и печатает случайный набор фрагментов. Его
зовёт скилл письма, поэтому набор на каждом заходе разный, а `--seed` делает
прогон воспроизводимым для теста.

`repocheck` сверяет фрагменты с текстами самого репозитория: скиллами,
определениями агентов, правилами, файлами задач и LLD. Ловит он перенос,
которого не видит отпечаток по журналам: текст, написанный агентом в файл, а
оттуда скопированный человеком в чат.

Выход 0 всё в порядке, 1 нечего показать (журналов нет, жанр пустой) либо
`repocheck` нашёл совпадение.
"""
import argparse
import importlib.util
import json
import os
import random
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# Служебные вставки харнеса и команды: текст с такой меткой писал не человек, а
# оболочка сессии, и в корпус он поедет мусором.
SERVICE_MARKS = (
    # Отчёт субагента приходит в ленту ролью user, и по объёму он перекрывает
    # всё остальное: без этой метки корпус собирается из агентской прозы, ради
    # ухода от которой всё и затевалось.
    "<task-notification>",
    "<task-id>",
    "<command-name>",
    "<command-message>",
    "<local-command-stdout>",
    "<local-command-stderr>",
    "<system-reminder>",
    "<user-prompt-submit-hook>",
    "<bash-input>",
    "<bash-stdout>",
    "<ide_opened_file>",
    "<ide_selection>",
    "Caveat: The messages below",
    "[Request interrupted by",
    "This session is being continued from a previous conversation",
    "API Error",
    "Please continue the conversation from where we left it off",
)

# Заготовки, которые сессии подают сами: промпт судьи со стенда obeycheck и
# запрос заголовка диалога от хука. Ловятся началом строки, потому что внутри
# них лежит правдоподобная русская проза, и по содержанию их от человека не
# отличить.
SERVICE_PREFIXES = (
    "ЗАДАНИЕ:",
    "[devkit-title]",
    "ДИАЛОГ:",
    "ВОПРОС ПОЛЬЗОВАТЕЛЯ:",
    "ОТВЕТ АССИСТЕНТА:",
    "Разговор завис",
)

# Слова, которые есть у всех и голоса не показывают. Список короткий нарочно:
# длинный стоп-лист выбрасывает и характерное тоже, а отсев частотой у нас уже
# есть.
STOP_WORDS = {
    "быть", "было", "если", "есть", "ещё", "надо", "нужно", "тогда", "чтобы",
    "когда", "который", "которая", "которое", "которые", "может", "можно",
    "просто", "только", "потом", "после", "перед", "через", "этот", "эта",
    "это", "этого", "этом", "эти", "там", "тут", "тоже", "также", "уже",
    "вообще", "давай", "давайте", "сделай", "сделать", "хорошо", "ладно",
    "пожалуйста", "спасибо", "теперь", "почему", "зачем", "какой", "какая",
    "какие", "всё", "все", "всех", "него", "неё", "них", "она", "оно", "они",
    "мне", "меня", "тебя", "себя", "свой", "своё", "свои", "своих", "будет",
    "будут", "были", "стало", "стал", "стала",
}

# Приписки харнеса к реплике человека: их дописывает хук на отправку, они
# повторяются от реплики к реплике и в словаре поднимают своё «веди», «пиши»,
# «массивом» выше всего живого. Реплика при этом настоящая, поэтому хвост
# режется, а начало остаётся.
TAIL_MARKS = (
    "Веди план работ файлом",
    "Долгие дела (поиск по диску",
)

WORD_RE = re.compile(r"[а-яёА-ЯЁ]+")
# В слове отпечатка буквы и цифры, латиница наравне с кириллицей. Длина
# отпечатка в словах стоит здесь же, и тест ссылается на неё через это имя.
NORM_RE = re.compile(r"[а-яёa-z0-9]+")
SIGN_WORDS = 8
# Бюджет слов на набор выборки. Четыре коротких фрагмента укладываются в
# него целиком, а один длинный съедает его почти весь, и это то поведение,
# ради которого бюджет заведён.
WORD_BUDGET = 400
FENCE_RE = re.compile(r"```.*?```", re.S)
CYRILLIC_RE = re.compile(r"[а-яёА-ЯЁ]")
LETTER_RE = re.compile(r"[а-яёА-ЯЁa-zA-Z]")


def journals(root):
    """Пути журналов сессий. Берётся только верхний уровень проекта: в
    `<сессия>/subagents/*.jsonl` роль user занята промптами, которые писал
    диспетчер, а не человек, и подмешивать их в эталон значит опять учить
    агента у агента."""
    out = []
    if not os.path.isdir(root):
        return out
    for project in sorted(os.listdir(root)):
        path = os.path.join(root, project)
        if not os.path.isdir(path):
            continue
        for name in sorted(os.listdir(path)):
            if name.endswith(".jsonl"):
                out.append(os.path.join(path, name))
    return out


def records(path):
    """Записи журнала по одной. Битая строка пропускается. Журнал живой сессии
    дописывается на ходу, и хвост бывает обрезан."""
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if isinstance(rec, dict):
                yield rec


def reply_text(rec):
    """Текст реплики человека или None, если запись не его.

    В журнале ролью user помечены и ответы инструментов, и вставки харнеса, и
    сжатие истории. Отличает их не роль, а форма записи: у результата
    инструмента content списком с блоками tool_result, у служебной вставки
    поднят isMeta."""
    if rec.get("type") != "user" or rec.get("isMeta") or rec.get("isCompactSummary"):
        return None
    message = rec.get("message")
    if not isinstance(message, dict) or message.get("role") != "user":
        return None
    content = message.get("content")
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return None
    parts = []
    for block in content:
        if not isinstance(block, dict):
            return None
        if block.get("type") != "text":
            return None
        parts.append(block.get("text", ""))
    return "\n".join(parts) if parts else None


def assistant_text(rec):
    """Текст ответа ассистента или None. Блоки не-текста (вызовы инструментов,
    рассуждение) пропускаются, остальное склеивается в один кусок."""
    if rec.get("type") != "assistant":
        return None
    message = rec.get("message")
    if not isinstance(message, dict) or message.get("role") != "assistant":
        return None
    content = message.get("content")
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return None
    parts = []
    for block in content:
        if isinstance(block, dict) and block.get("type") == "text":
            parts.append(block.get("text", ""))
    return "\n".join(parts) if parts else None


def signature(text):
    """Отпечаток начала текста это восемь первых слов, приведённых к нижнему
    регистру и очищенных от знаков. Знаки снимаются потому, что текст едет
    copy-paste, а обратные кавычки и тире по дороге теряются. Восьми слов
    хватает, чтобы два разных абзаца не совпали, и мало, чтобы его не задела
    правка хвоста."""
    ws = NORM_RE.findall(text.lower())
    if len(ws) < SIGN_WORDS:
        return None
    return " ".join(ws[:SIGN_WORDS])


# Третье сито: шаблон. Диспетчер собирает промпт по образцу и раздаёт его в
# новые окна, человек только копирует. В журнале такой текст лежит ролью user,
# словами ассистента он нигде не звучал, и первые два сита его пропускают.
# Отличает шаблон повтор: один и тот же текст лежит в двух десятках журналов, и
# меняется в нём только ID задачи. Находка приёмки DK-522 на фрагменте про
# груминг, который стоял в кандидатах жанра `skill`.
TEMPLATE_JOURNALS = 3


def template_key(text, size=SIGN_WORDS):
    """Ключ шаблона: первые слова без чисел, приведённые к нижнему регистру.

    Числа выброшены нарочно. У раздачи одного промпта по задачам различается
    только номер, и с ним отпечаток у каждой копии свой."""
    ws = [w for w in NORM_RE.findall(text.lower()) if not w.isdigit()]
    if len(ws) < size:
        return None
    return " ".join(ws[:size])


# Четвёртое сито: типографика. У человека в текстах нет ни длинного тире, ни
# лапок, ни многоточия одним символом, ни Unicode-стрелок. Правила проекта их
# запрещают, и клавиатурная привычка тоже, а модель ставит их сама. Символ
# внутри реплики роли user это след вставки из чужого ответа. Сито не
# отсеивает, а помечает: знак мог приехать и из скопированного пути, и решать
# тут человеку. Находка приёмки DK-522 на смешанном фрагменте, где рамка была
# своя, а список практик пришёл copy-paste.
TYPO_MARKS = (
    ("\u2014", "длинное тире"),
    ("\u2013", "короткое тире"),
    ("\u201c", "лапки"),
    ("\u201d", "лапки"),
    ("\u201e", "лапки"),
    ("\u2018", "лапки"),
    ("\u2019", "лапки"),
    ("\u2026", "многоточие одним символом"),
    ("\u2192", "стрелка"),
    ("\u2190", "стрелка"),
    ("\u21d2", "стрелка"),
    ("\u2194", "стрелка"),
)


def typo_marks(text):
    """Знаки чужой типографики в тексте, по имени и без повторов."""
    out = []
    for знак, имя in TYPO_MARKS:
        if знак in text and имя not in out:
            out.append(имя)
    return out


# Пятое сито: текст, который наш сторож прозы считает агентским. Первые четыре
# смотрят, откуда текст приехал, и на трёх находках подряд оказались слепы:
# человек ловил агентскую фразу глазами там, где журналы, репозиторий, повтор
# и типографика молчали (последняя находка это `skill` #4, приёмка DK-522).
# Зацепка тут не в происхождении, а в самом тексте: эталон человеческой прозы
# не может быть тем, что `hooks/check-prose.py` заворачивает как агентское.
#
# Считает приметы сам хук, своих счётчиков сито не заводит. Взяты три метрики,
# по которым замер цели DK-446 развёл колонки дальше всего, и порог у каждой
# это блокирующий порог конфига, то есть агентская колонка замера.
GUARD_METRICS = ("argued", "colon_mid", "but_not_tail")
# Помечает не одна примета, а две. Одна метрика выше агентской колонки на
# коротком фрагменте это частота, скачущая от одной фразы: в живом корпусе
# таких шесть, и все шесть тексты человека. Полная форма «не X, а Y» это
# лексика пользователя, и на ней одной фрагмент не падает (`task` #21, хвост
# 11,0 при пороге 5). У отвергнутого `skill` #4 приметы все три разом.
GUARD_MARKS = 2
_guard = []


def guard():
    """Возврат (модуль сторожа, пороги) или (None, None), когда его нет.

    Хук лежит в `hooks/check-prose.py`, имя с дефисом обычным импортом не
    берётся. Пороги приезжают из `kit/prose.toml` тем же чтением, что у самого
    хука: разъехавшись, они перестали бы значить агентскую колонку замера."""
    if _guard:
        return _guard[0]
    root = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
    hooks = os.path.join(root, "hooks")
    path = os.path.join(hooks, "check-prose.py")
    try:
        if hooks not in sys.path:
            sys.path.insert(0, hooks)
        spec = importlib.util.spec_from_file_location("check_prose", path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        conf, gaps = module.read_config()
        if conf is None:
            raise RuntimeError("; ".join(gaps))
        limits = {k: conf.block[k] for k in GUARD_METRICS}
    except (OSError, ImportError, AttributeError, RuntimeError):
        _guard.append((None, None))
        return _guard[0]
    _guard.append((module, limits))
    return _guard[0]


def agent_marks(text):
    """Приметы агентской прозы в тексте: список (название, значение, порог).

    Пустой список значит, что сторож смолчал. None значит, что сторожа нет и
    сито не работало: молчание сита и его отсутствие это разные вещи."""
    module, limits = guard()
    if module is None:
        return None
    _, values = module.measure(text)
    out = []
    for key in GUARD_METRICS:
        if values[key] > limits[key]:
            out.append((module.BY_KEY[key].title, values[key], limits[key]))
    return out


def agent_prose(text):
    """Правда, когда сторож насчитал две приметы из трёх."""
    marks = agent_marks(text)
    return bool(marks) and len(marks) >= GUARD_MARKS


def marks_line(marks):
    """Строка про приметы для выгрузки и отчёта."""
    return ", ".join("%s %s при пороге %d"
                     % (имя, ("%.1f" % v).replace(".", ","), порог)
                     for имя, v, порог in marks)


def borrowed(root, stamps):
    """Подписи реплик, которые ассистент написал раньше человека.

    Пользователь переносит черновик агента из окна в окно copy-paste, и в
    журнале второго окна текст оседает ролью user (находка ревью DK-522:
    промпт «Берём вариант 1 (DK-237 вперёд)» сочинён ассистентом в 16:08 и
    вставлен человеком в 16:09). Такой текст в эталон брать нельзя. Ради
    ухода от него и заведена цель DK-446.

    Сравнение идёт по времени. Обратный случай выглядит так же. Агент
    цитирует реплику человека в отчёте или в ревью, а запись ассистента
    тогда идёт позже, и кандидата она не трогает.
    """
    out = set()
    for path in journals(root):
        for rec in records(path):
            text = assistant_text(rec)
            if not text:
                continue
            ts = rec.get("timestamp") or ""
            for chunk in text.split("\n"):
                sign = signature(chunk)
                if sign is None:
                    continue
                first = stamps.get(sign)
                if first is not None and ts < first:
                    out.add(sign)
    return out


# Второе сито происхождения. Агент пишет текст в файл репозитория, человек
# копирует его оттуда в чат, и в журнале текст лежит ролью user. Отпечаток по
# журналам такой перенос не видит: словами ассистента в ленте он не звучал.
# Дыру нашла приёмка DK-522 на фрагменте про груминг, где слог был прямо из
# наших скиллов.
SHINGLE_WORDS = 7
# Каталоги и файлы, которые пишет агент. Скилл `prose` из них исключён целиком.
# Корпус сам состоит из проверяемых фрагментов, и каждый совпал бы с собой, а
# словарь рядом собран из тех же реплик и даёт находку на каждую вторую.
REPO_DIRS = ("kit/skills", "kit/agents", "docs")
REPO_FILES = ("RULES.md", "RULES.core.md", "RULES.board.md", "RULES.board.core.md",
              "AGENTS.md", "TASKFORM.md", "RANKING.md", "ACCEPTANCE.md", "README.md")
CORPUS_MARK = os.path.join("skills", "prose")


def repo_texts(root):
    """Пути текстов репозитория, которые пишет агент."""
    out = []
    for name in REPO_FILES:
        path = os.path.join(root, name)
        if os.path.isfile(path):
            out.append(path)
    for d in REPO_DIRS:
        for base, _, files in os.walk(os.path.join(root, d)):
            if CORPUS_MARK in base:
                continue
            for name in sorted(files):
                if name.endswith(".md"):
                    out.append(os.path.join(base, name))
    return sorted(out)


def shingles(text, size=SHINGLE_WORDS):
    """Скользящие цепочки из size слов, приведённых к нижнему регистру.

    Семь слов это порог, ниже которого совпадают общие места («в этом случае
    можно будет потом»), а выше не ловится короткая вставка."""
    ws = NORM_RE.findall(text.lower())
    return [tuple(ws[i:i + size]) for i in range(len(ws) - size + 1)]


def repo_index(root, size=SHINGLE_WORDS):
    """Цепочка слов -> путь файла, где она встретилась первой."""
    index = {}
    for path in repo_texts(root):
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                text = f.read()
        except OSError:
            continue
        rel = os.path.relpath(path, root)
        for chain in shingles(text, size):
            index.setdefault(chain, rel)
    return index


def borrowed_from_repo(index, text, size=SHINGLE_WORDS):
    """Совпадения текста с текстами репозитория: список (фраза, файл).

    Соседние цепочки склеиваются в одну фразу, иначе одно длинное совпадение
    печатается десятком строк."""
    ws = NORM_RE.findall(text.lower())
    hits = []
    for i, chain in enumerate(shingles(text, size)):
        path = index.get(chain)
        if path:
            hits.append((i, path))
    out = []
    start = prev = None
    path = None
    for i, p in hits:
        if path == p and prev is not None and i == prev + 1:
            prev = i
            continue
        if path is not None:
            out.append((" ".join(ws[start:prev + size]), path))
        start = prev = i
        path = p
    if path is not None:
        out.append((" ".join(ws[start:prev + size]), path))
    out.sort(key=lambda pair: -len(pair[0]))
    return out


def raw_phrase(text, chain):
    """Кусок исходного текста, отвечающий цепочке слов, или пустая строка.

    Нужен, чтобы спросить у git дату самой фразы, а не всего файла. Отпечаток
    цепочки нормализован, а `git log -S` ищет литерал, поэтому цепочку
    приходится разворачивать обратно в текст файла со всеми знаками."""
    lowered = text.lower()
    spans = [m.span() for m in NORM_RE.finditer(lowered)]
    words = [lowered[a:b] for a, b in spans]
    n = len(chain)
    for i in range(len(words) - n + 1):
        if tuple(words[i:i + n]) == chain:
            return text[spans[i][0]:spans[i + n - 1][1]]
    return ""


def phrase_date(root, path, raw):
    """Дата коммита, которым фраза появилась в файле, в виде ГГГГ-ММ-ДД.

    Дата файла для вердикта слишком груба: файл цели заведён раньше реплики, а
    абзац с ответами человека дописан в него позже. Пустая строка, если фразу
    по истории не нашли, тогда решает дата файла."""
    if not raw:
        return ""
    try:
        out = subprocess.run(
            ["git", "-C", root, "log", "--format=%as", "-S", raw, "--", path],
            capture_output=True, text=True, timeout=60)
    except (OSError, subprocess.SubprocessError):
        return ""
    dates = [line.strip() for line in out.stdout.split("\n") if line.strip()]
    return dates[-1] if dates else ""


def file_dates(root, path):
    """Даты первого и последнего коммита файла в виде ГГГГ-ММ-ДД."""
    try:
        out = subprocess.run(
            ["git", "-C", root, "log", "--format=%as", "--", path],
            capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        return "", ""
    dates = [line.strip() for line in out.stdout.split("\n") if line.strip()]
    if not dates:
        return "", ""
    return dates[-1], dates[0]


def taken_from_repo(added, said):
    """Файл был заведён раньше реплики, значит текст мог уехать из него в чат.

    Обратный случай выглядит так же по словам: агент цитирует человека в файле
    задачи, и совпадение то же самое. Различает их дата, и только она."""
    if not added or not said:
        return None
    if added < said:
        return True
    if added > said:
        return False
    return None


def clean(text):
    """Реплика без оград кода и без хвостов вставок. Ограды режутся, потому что
    в корпус едет проза, а не листинг, но реплику с кодом целиком не выбрасываем:
    вокруг листинга обычно и лежит нужный абзац.

    Команда в обратных кавычках остаётся как есть. Раньше она менялась на слово
    CODE, и в кандидатах оседали фразы вроде «вопросы задавай командой CODE»:
    имя команды это часть речи человека, а не листинг."""
    for mark in TAIL_MARKS:
        cut = text.find(mark)
        if cut > 0:
            text = text[:cut]
    text = FENCE_RE.sub(" ", text)
    return text.strip()


def words(text):
    return WORD_RE.findall(text)


def is_service(text):
    if not text.strip():
        return True
    for mark in SERVICE_MARKS:
        if mark in text:
            return True
    stripped = text.strip()
    for mark in SERVICE_PREFIXES:
        if stripped.startswith(mark):
            return True
    # Слэш-команда и голая ссылка это не проза, а обращение к харнесу.
    if stripped.startswith("/") and "\n" not in stripped:
        return True
    if stripped.startswith("http://") or stripped.startswith("https://"):
        return True
    return False


def is_prose(text, min_words):
    """Реплика годится в кандидаты, если это связный русский текст нужной
    длины. Порог кириллицы отсекает вставки логов и путей: там буквы латинские,
    а русских слов почти нет."""
    ws = words(text)
    if len(ws) < min_words:
        return False
    letters = LETTER_RE.findall(text)
    if not letters:
        return False
    if len(CYRILLIC_RE.findall(text)) / len(letters) < 0.6:
        return False
    return True


def collect(root, min_words):
    """Возврат (кандидаты, статистика). Кандидат это (журнал, дата, текст).

    Журналы читаются дважды. Первый проход набирает реплики человека и
    запоминает, когда каждая из них сказана впервые. Второй ищет тот же
    текст словами ассистента раньше этого времени. Одним проходом не
    обойтись. Черновик агента лежит в соседнем журнале, а какие подписи
    искать, известно только после первого прохода."""
    stat = {"journals": 0, "replies": 0, "service": 0, "short": 0,
            "borrowed": 0, "template": 0, "typo": 0, "agent": 0, "kept": 0,
            "guard": agent_marks("") is not None}
    seen = set()
    rows = []
    stamps = {}
    templates = {}
    for path in journals(root):
        stat["journals"] += 1
        for rec in records(path):
            raw = reply_text(rec)
            if raw is None:
                continue
            stat["replies"] += 1
            if is_service(raw):
                stat["service"] += 1
                continue
            text = clean(raw)
            if not is_prose(text, min_words):
                stat["short"] += 1
                continue
            # Ключ шаблона считается до отсева повторов: раздача одного
            # промпта по задачам даёт разные тексты, и в `seen` они не
            # схлопываются.
            шаблон = template_key(text)
            if шаблон is not None:
                templates.setdefault(шаблон, set()).add(os.path.basename(path))
            key = text[:400]
            if key in seen:
                continue
            seen.add(key)
            stamp = rec.get("timestamp") or ""
            # Подпись снимается с исходной реплики, а не с очищенной. В
            # черновике ассистента ограды кода стоят на месте.
            sign = signature(raw)
            if sign is not None and (sign not in stamps or stamp < stamps[sign]):
                stamps[sign] = stamp
            rows.append((os.path.basename(path), stamp[:10], text, sign))
    taken = borrowed(root, stamps) if stamps else set()
    out = []
    for name, date, text, sign in rows:
        if sign is not None and sign in taken:
            stat["borrowed"] += 1
            continue
        шаблон = template_key(text)
        if шаблон is not None and len(templates.get(шаблон, ())) >= TEMPLATE_JOURNALS:
            stat["template"] += 1
            continue
        stat["kept"] += 1
        if typo_marks(text):
            stat["typo"] += 1
        if agent_prose(text):
            stat["agent"] += 1
        out.append((name, date, text))
    return out, stat


def dictionary(candidates, min_hits):
    """Частотный словарь по кандидатам: слово и в скольких репликах оно
    встретилось. Счёт идёт по репликам, а не по вхождениям, иначе одна длинная
    реплика про поезд поднимает «поезд» в верх списка."""
    hits = {}
    for _, _, text in candidates:
        for word in {w.lower() for w in words(text)}:
            if len(word) < 4 or word in STOP_WORDS:
                continue
            hits[word] = hits.get(word, 0) + 1
    pairs = [(w, n) for w, n in hits.items() if n >= min_hits]
    pairs.sort(key=lambda p: (-p[1], p[0]))
    return pairs


# Заголовок кандидата в выгрузке опознаётся целиком, вместе с именем журнала.
# В длинной реплике человека попадаются свои заголовки markdown («## 2. Что
# делать»), и по одному «## N» выгрузка режется посреди текста.
DUMP_HEAD_RE = re.compile(r"(?m)^## (\d+)\. (\S+\.jsonl), (.+)$")


def read_dump(path):
    """Кандидаты из выгрузки сборщика: список (номер, журнал, дата, текст)."""
    with open(path, encoding="utf-8") as f:
        text = f.read()
    heads = list(DUMP_HEAD_RE.finditer(text))
    out = []
    for i, m in enumerate(heads):
        end = heads[i + 1].start() if i + 1 < len(heads) else len(text)
        lines = text[m.end():end].split("\n")
        # Пометка типографики стоит сразу под заголовком, в тело она не едет.
        while lines and not lines[0].strip():
            lines.pop(0)
        if lines and lines[0].startswith("типографика:"):
            lines.pop(0)
        body = "\n".join(lines).strip()
        out.append((int(m.group(1)), m.group(2), m.group(3).strip(), body))
    return out


def write_dump(out_dir, candidates, words_top):
    os.makedirs(out_dir, exist_ok=True)
    replies = os.path.join(out_dir, "replies.md")
    with open(replies, "w", encoding="utf-8") as f:
        f.write("# Кандидаты в корпус\n\n")
        f.write("Выгрузка сборщика, не коммитится. Отбирает пользователь.\n\n")
        for i, (journal, date, text) in enumerate(candidates, 1):
            head = "## %d. %s, %s\n" % (i, journal, date or "без даты")
            знаки = typo_marks(text)
            if знаки:
                head += "типографика: %s\n" % ", ".join(знаки)
            приметы = agent_marks(text)
            if приметы and len(приметы) >= GUARD_MARKS:
                head += "проза: %s\n" % marks_line(приметы)
            f.write(head + "\n" + text + "\n\n")
    words_file = os.path.join(out_dir, "dictionary.md")
    with open(words_file, "w", encoding="utf-8") as f:
        f.write("# Словарь по репликам\n\n")
        f.write("Слово и число реплик, где оно встретилось.\n\n")
        for word, n in words_top:
            f.write("- %s %d\n" % (word, n))
    return replies, words_file


# Фрагмент корпуса: заголовок «## N», сразу под ним поля «ключ: значение», за
# первой пустой строкой тело. Пустая строка кончает шапку без оговорок, иначе
# двоеточие в первой фразе тела уехало бы в поле.
HEAD_RE = re.compile(r"^##\s+(.+)$")
KEY_RE = re.compile(r"^([А-Яа-яЁёA-Za-z][А-Яа-яЁёA-Za-z -]*):\s*(.*)$")


def parse_genre(path):
    """Возврат (имя жанра, фрагменты). Фрагмент это словарь с полями шапки и
    ключом body."""
    with open(path, encoding="utf-8") as f:
        lines = f.read().split("\n")
    name = ""
    fragments = []
    current = None
    body = []
    in_head = False

    def flush():
        if current is not None:
            current["body"] = "\n".join(body).strip()
            fragments.append(current)

    for line in lines:
        if line.startswith("# ") and not name:
            name = line[2:].strip()
            continue
        m = HEAD_RE.match(line)
        if m:
            flush()
            current = {"номер": m.group(1).strip()}
            body = []
            in_head = True
            continue
        if current is None:
            continue
        if in_head:
            if not line.strip():
                in_head = False
                continue
            key = KEY_RE.match(line)
            if key:
                current[key.group(1).strip()] = key.group(2).strip()
                continue
            in_head = False
        body.append(line)
    flush()
    return name, [f for f in fragments if f.get("body")]


def read_corpus(corpus_dir):
    """Возврат словаря: идентификатор жанра -> (имя, фрагменты)."""
    out = {}
    if not os.path.isdir(corpus_dir):
        return out
    for name in sorted(os.listdir(corpus_dir)):
        if not name.endswith(".md"):
            continue
        genre = name[:-3]
        title, fragments = parse_genre(os.path.join(corpus_dir, name))
        if fragments:
            out[genre] = (title or genre, fragments)
    return out


def sample(corpus_dir, genre, count, seed, budget=WORD_BUDGET):
    """Возврат списка (жанр, имя жанра, фрагмент).

    Фрагменты в корпусе разной длины, от двадцати пяти слов до четырёхсот, и
    четыре длинных подряд кладут в контекст письма простыню. Поэтому набор
    держит бюджет слов: берётся, пока хватает бюджета, а `count` остаётся
    потолком числа фрагментов. Один длинный фрагмент вытесняет три коротких.
    Первый фрагмент едет всегда, даже когда он один длиннее бюджета."""
    corpus = read_corpus(corpus_dir)
    if genre:
        if genre not in corpus:
            return []
        pool = [(genre, corpus[genre][0], f) for f in corpus[genre][1]]
    else:
        pool = []
        for key, (title, fragments) in corpus.items():
            pool.extend((key, title, f) for f in fragments)
    rnd = random.Random(seed)
    rnd.shuffle(pool)
    picked = []
    spent = 0
    for item in pool:
        if len(picked) >= count:
            break
        size = len(words(item[2]["body"]))
        if picked and budget and spent + size > budget:
            continue
        picked.append(item)
        spent += size
    return picked


def render(picked):
    """Выборка текстом, шапкой едут источник, роль и пометка.

    Пометка это оговорка вычитки, вроде «резкость оценки, лексику не
    копировать». Без неё резкий фрагмент попадает в контекст письма как
    образец целиком, вместе с бранью, ради которой его как раз и оставили
    резким (находка ревью DK-522)."""
    out = []
    for genre, title, fragment in picked:
        head = "## %s (%s)" % (title, genre)
        for field in ("источник", "роль", "пометка"):
            value = fragment.get(field, "")
            if value:
                head += "\n%s: %s" % (field, value)
        out.append(head + "\n\n" + fragment["body"])
    return "\n\n".join(out)


def cmd_collect(args):
    root = os.path.expanduser(args.journals)
    candidates, stat = collect(root, args.min_words)
    print("журналов: %d" % stat["journals"])
    print("реплик роли user: %d" % stat["replies"])
    print("служебных отсеяно: %d" % stat["service"])
    print("коротких и нерусских отсеяно: %d" % stat["short"])
    print("переносов чужого черновика отсеяно: %d" % stat["borrowed"])
    print("шаблонных раздач отсеяно: %d" % stat["template"])
    print("с чужой типографикой, помечено: %d" % stat["typo"])
    if stat["guard"]:
        print("с агентской прозой, помечено: %d" % stat["agent"])
    else:
        print("сторож прозы не прочёлся, сито агентской прозы не работало")
    print("кандидатов: %d" % stat["kept"])
    top = dictionary(candidates, args.min_hits)
    print("словарь: %d слов от %d реплик" % (len(top), args.min_hits))
    if args.out:
        replies, words_file = write_dump(args.out, candidates, top)
        print("выгрузка: %s, %s" % (replies, words_file))
    if not stat["journals"]:
        print("журналов не нашлось: проверь путь %s" % root, file=sys.stderr)
        return 1
    return 0


def check_texts(index, root, items):
    """Находки второго сита по списку (имя, дата, текст).

    Возврат списка словарей с полями находки. Дату файла спрашиваем у git, и
    только она отличает перенос от цитаты человека в агентском файле."""
    out = []
    for name, said, text in items:
        for phrase, path in borrowed_from_repo(index, text):
            chain = tuple(phrase.split()[:SHINGLE_WORDS])
            raw = ""
            try:
                with open(os.path.join(root, path), encoding="utf-8",
                          errors="replace") as f:
                    raw = raw_phrase(f.read(), chain)
            except OSError:
                pass
            added, last = file_dates(root, path)
            появилась = phrase_date(root, path, raw) or added
            out.append({"кандидат": name, "сказано": said, "файл": path,
                        "фраза": phrase, "заведён": added, "правлен": last,
                        "появилась": появилась,
                        "перенос": taken_from_repo(появилась, said)})
    return out


def items_of(args):
    """Список (имя, дата, текст) по выгрузке сборщика или по корпусу.

    Оба сита, что смотрят на готовый текст, ходят по одному и тому же
    материалу: либо кандидаты из выгрузки, либо фрагменты корпуса."""
    items = []
    if args.dump:
        for num, journal, date, text in read_dump(args.dump):
            items.append(("выгрузка #%d, %s" % (num, journal), date, text))
        return items
    corpus = read_corpus(args.corpus)
    for genre in sorted(corpus):
        _, fragments = corpus[genre]
        for i, f in enumerate(fragments, 1):
            said = ""
            m = re.search(r"(\d{4}-\d{2}-\d{2})", f.get("источник", ""))
            if m:
                said = m.group(1)
            items.append(("%s #%d (%s)" % (genre, i, f.get("источник", "")),
                          said, f["body"]))
    return items


def cmd_prosecheck(args):
    """Пятое сито по готовому тексту, командой рядом с `repocheck`.

    Сито помечает, а не режет: решает человек, как и с типографикой. Возврат 1
    при находках, чтобы прогон было видно и в скрипте."""
    module, limits = guard()
    if module is None:
        print("сторож прозы не прочёлся: жду hooks/check-prose.py и "
              "kit/prose.toml рядом с ним", file=sys.stderr)
        return 2
    items = items_of(args)
    print("пороги агентской колонки: %s"
          % ", ".join("%s %d" % (module.BY_KEY[k].title, limits[k])
                      for k in GUARD_METRICS))
    помечено = 0
    for имя, _, text in items:
        приметы = agent_marks(text)
        if len(приметы) < GUARD_MARKS:
            continue
        помечено += 1
        print("\n%s\n  примет %d из %d: %s"
              % (имя, len(приметы), len(GUARD_METRICS), marks_line(приметы)))
    print("\nпроверено: %d, помечено: %d" % (len(items), помечено))
    return 1 if помечено else 0


def cmd_repocheck(args):
    root = os.path.abspath(os.path.expanduser(args.repo))
    index = repo_index(root)
    items = items_of(args)
    print("текстов репозитория: %d, цепочек: %d" % (len(repo_texts(root)), len(index)))
    hits = check_texts(index, root, items)
    for hit in hits:
        if hit["перенос"] is True:
            вердикт = "файл раньше реплики, разобрать"
        elif hit["перенос"] is False:
            вердикт = "файл позже реплики, цитата человека"
        elif hit["появилась"] and hit["сказано"]:
            вердикт = "тот же день, разобрать"
        else:
            вердикт = "даты нет, разобрать глазами"
        print("\n%s\n  файл: %s (фраза в файле с %s, файл заведён %s), %s"
              "\n  фраза: «%s»"
              % (hit["кандидат"], hit["файл"], hit["появилась"] or "?",
                 hit["заведён"] or "?", вердикт, hit["фраза"]))
    print("\nпроверено: %d, находок: %d" % (len(items), len(hits)))
    return 1 if hits else 0


def cmd_sample(args):
    picked = sample(args.corpus, args.genre, args.count, args.seed, args.words)
    if not picked:
        print("в корпусе нечего показать: %s" % args.corpus, file=sys.stderr)
        return 1
    print(render(picked))
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="корпус эталонов прозы")
    sub = parser.add_subparsers(dest="cmd")

    p = sub.add_parser("collect", help="реплики пользователя из журналов сессий")
    p.add_argument("--journals", default="~/.claude/projects")
    p.add_argument("--min-words", type=int, default=25)
    p.add_argument("--min-hits", type=int, default=8)
    p.add_argument("--out", default="")
    p.set_defaults(func=cmd_collect)

    p = sub.add_parser("repocheck", help="сверка фрагментов с текстами репозитория")
    p.add_argument("--repo", default=os.path.join(HERE, "..", "..", ".."))
    p.add_argument("--corpus", default=os.path.join(HERE, "corpus"))
    p.add_argument("--dump", default="")
    p.set_defaults(func=cmd_repocheck)

    p = sub.add_parser("prosecheck",
                       help="сверка фрагментов со сторожем прозы")
    p.add_argument("--corpus", default=os.path.join(HERE, "corpus"))
    p.add_argument("--dump", default="")
    p.set_defaults(func=cmd_prosecheck)

    p = sub.add_parser("sample", help="случайный набор фрагментов корпуса")
    p.add_argument("--corpus", default=os.path.join(HERE, "corpus"))
    p.add_argument("--genre", default="")
    p.add_argument("--count", type=int, default=4)
    p.add_argument("--words", type=int, default=WORD_BUDGET)
    p.add_argument("--seed", type=int, default=None)
    p.set_defaults(func=cmd_sample)

    args = parser.parse_args(argv)
    if not getattr(args, "func", None):
        parser.print_help()
        return 1
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
