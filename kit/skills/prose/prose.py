#!/usr/bin/env python3
"""Корпус эталонов прозы: сборщик реплик пользователя и выборка фрагментов.

  prose.py collect [--journals DIR] [--min-words N] [--out DIR]
  prose.py sample [--genre ЖАНР] [--count N] [--seed S] [--corpus DIR]

`collect` режет журналы сессий на реплики роли user, отсеивает короткие и
служебные и складывает словарь. Выгрузка не коммитится: это личные тексты, в
корпус из них едет только вычитанное пользователем.

`sample` читает корпус из `corpus/` и печатает случайный набор фрагментов. Его
зовёт скилл письма, поэтому набор на каждом заходе разный, а `--seed` делает
прогон воспроизводимым для теста.

Выход 0 всё в порядке, 1 нечего показать (журналов нет, жанр пустой).
"""
import argparse
import json
import os
import random
import re
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


def clean(text):
    """Реплика без оград кода и без хвостов вставок. Ограды режутся, потому что
    в корпус едет проза, а не листинг, но реплику с кодом целиком не выбрасываем:
    вокруг листинга обычно и лежит нужный абзац."""
    for mark in TAIL_MARKS:
        cut = text.find(mark)
        if cut > 0:
            text = text[:cut]
    text = FENCE_RE.sub(" ", text)
    text = re.sub(r"`[^`\n]*`", "CODE", text)
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
    """Возврат (кандидаты, статистика). Кандидат это (журнал, дата, текст)."""
    stat = {"journals": 0, "replies": 0, "service": 0, "short": 0, "kept": 0}
    seen = set()
    out = []
    for path in journals(root):
        stat["journals"] += 1
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                if not isinstance(rec, dict):
                    continue
                text = reply_text(rec)
                if text is None:
                    continue
                stat["replies"] += 1
                if is_service(text):
                    stat["service"] += 1
                    continue
                text = clean(text)
                if not is_prose(text, min_words):
                    stat["short"] += 1
                    continue
                key = text[:400]
                if key in seen:
                    continue
                seen.add(key)
                stat["kept"] += 1
                out.append((os.path.basename(path), (rec.get("timestamp") or "")[:10], text))
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


def write_dump(out_dir, candidates, words_top):
    os.makedirs(out_dir, exist_ok=True)
    replies = os.path.join(out_dir, "replies.md")
    with open(replies, "w", encoding="utf-8") as f:
        f.write("# Кандидаты в корпус\n\n")
        f.write("Выгрузка сборщика, не коммитится. Отбирает пользователь.\n\n")
        for i, (journal, date, text) in enumerate(candidates, 1):
            f.write("## %d. %s, %s\n\n%s\n\n" % (i, journal, date or "без даты", text))
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


def sample(corpus_dir, genre, count, seed):
    """Возврат списка (жанр, имя жанра, фрагмент)."""
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
    if count >= len(pool):
        rnd.shuffle(pool)
        return pool
    return rnd.sample(pool, count)


def render(picked):
    out = []
    for genre, title, fragment in picked:
        head = "## %s (%s)" % (title, genre)
        source = fragment.get("источник", "")
        role = fragment.get("роль", "")
        if source:
            head += "\nисточник: " + source
        if role:
            head += "\nроль: " + role
        out.append(head + "\n\n" + fragment["body"])
    return "\n\n".join(out)


def cmd_collect(args):
    root = os.path.expanduser(args.journals)
    candidates, stat = collect(root, args.min_words)
    print("журналов: %d" % stat["journals"])
    print("реплик роли user: %d" % stat["replies"])
    print("служебных отсеяно: %d" % stat["service"])
    print("коротких и нерусских отсеяно: %d" % stat["short"])
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


def cmd_sample(args):
    picked = sample(args.corpus, args.genre, args.count, args.seed)
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

    p = sub.add_parser("sample", help="случайный набор фрагментов корпуса")
    p.add_argument("--corpus", default=os.path.join(HERE, "corpus"))
    p.add_argument("--genre", default="")
    p.add_argument("--count", type=int, default=4)
    p.add_argument("--seed", type=int, default=None)
    p.set_defaults(func=cmd_sample)

    args = parser.parse_args(argv)
    if not getattr(args, "func", None):
        parser.print_help()
        return 1
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
