#!/usr/bin/env python3
"""Парсер и валидатор профилей харнесов (подмножество TOML).

  python3 harness.py <файл>...
      напечатать отчёт по каждому файлу: канонический дамп разобранного и
      предупреждения, либо строку error с именем файла и ключа. Имена
      profile-*.toml и <харнес>.toml из kit/harness/ вдобавок валидируются сводом
      профиля, остальные только разбираются.

Формат описан в docs/lld/DK-033-universal-kit.md: секции одного уровня, ключи
key = value, значения это строки в двойных кавычках, целые, true/false и
массивы строк в одну строку, комментарии с решётки. tomllib взять нельзя, он
появился в python3.11, а devkitctl обещает голый python3.

Вторая реализация того же формата живёт в agentctl (toml.go, harness.go).
Тексты ошибок и канонический дамп у них общие и сверяются фикстурами
kit/harness/testdata: один и тот же вход обе стороны обязаны читать одинаково.
"""
import os
import sys

STR, INT, BOOL, ARR = "str", "int", "bool", "arr"

KIND_NAMES = {STR: "строку", INT: "целое", BOOL: "true/false", ARR: "массив строк"}

# Пять обязательных секций профиля и известные ключи каждой; порядок фиксирован,
# по нему идут проверки и сообщения.
PROFILE_SCHEMA = (
    ("detect", (("env", STR), ("value", STR), ("bin", STR))),
    ("rules", (("mode", STR), ("file", STR), ("import_line", STR), ("config", STR),
               ("dir", STR), ("global_file", STR))),
    ("delegate", (("mode", STR), ("command", ARR), ("agents_dir", STR),
                  ("agents_format", STR), ("map_mini", STR), ("map_base", STR),
                  ("map_pro", STR), ("map_max", STR))),
    ("hooks", (("protocol", STR), ("config", STR), ("events", ARR),
               ("memory_index", STR))),
    ("quota", (("snap", STR), ("script", STR), ("buckets", ARR), ("required", STR),
               ("spend_mini", ARR), ("spend_base", ARR), ("spend_pro", ARR),
               ("spend_max", ARR), ("budget_based", BOOL))),
)

# Шестая секция, ось скиллов (docs/lld/DK-100-context-tree.md, раздел «Харнесы
# без скиллов»). Обязательной её сделать нельзя: профиль, написанный до этой
# оси, обязан читаться и дальше, а отсутствие секции значит сегодняшнюю полную
# вклейку правил.
OPTIONAL_SCHEMA = (
    ("skills", (("dir", STR), ("format", STR), ("discovery", STR))),
)

DISCOVERY_VALUES = ("auto", "manual")

TIERS = ("mini", "base", "pro", "max")
# Префиксы имён бакетов, из которых берётся окно расчёта: week_ это 7 суток,
# month_ это 30. Длины окон живут в agentctl (quota.go), тут только перечень.
BUCKET_PREFIXES = ("week_", "month_")
KNOWN_EVENTS = ("write", "session-start", "notify", "subagent-done", "turn-done",
                "turn-failed", "prompt-submit", "tool-done")


class TomlError(Exception):
    pass


class Value(object):
    def __init__(self, kind, val, line=0):
        self.kind = kind
        self.val = val
        self.line = line

    def render(self):
        if self.kind == STR:
            return quote(self.val)
        if self.kind == INT:
            return str(self.val)
        if self.kind == BOOL:
            return "true" if self.val else "false"
        return "[" + ", ".join(quote(s) for s in self.val) + "]"


class Doc(object):
    def __init__(self, name):
        self.name = name
        self.tables = {"": {}}  # имя секции -> ключ -> Value, порядок вставки
        self.order = [""]

    def table(self, name):
        return self.tables.get(name)

    def get(self, section, key):
        return (self.tables.get(section) or {}).get(key)

    def str_of(self, section, key):
        v = self.get(section, key)
        return v.val if v is not None and v.kind == STR else ""

    def arr_of(self, section, key):
        v = self.get(section, key)
        return v.val if v is not None and v.kind == ARR else []

    def dump(self):
        out = []
        for name in self.order:
            t = self.tables[name]
            if name:
                out.append("[%s]" % name)
            elif not t:
                continue
            for k, v in t.items():
                out.append("%s = %s" % (k, v.render()))
        return "".join(s + "\n" for s in out)


def quote(s):
    # Единственный способ показать строку в сообщении: repr в Python и %q в Go
    # экранируют по-разному, а тексты ошибок у двух реализаций общие.
    s = s.replace("\\", "\\\\").replace('"', '\\"')
    return '"' + s.replace("\n", "\\n").replace("\t", "\\t") + '"'


def bare_key(s):
    if not s:
        return False
    for c in s:
        if not (c.isascii() and (c.isalnum() or c in "_-")):
            return False
    return True


def is_int(s):
    if s and s[0] in "+-":
        s = s[1:]
    return bool(s) and all("0" <= c <= "9" for c in s)


def scan_string(s):
    # s начинается с кавычки; возврат (значение, хвост).
    out = []
    i = 1
    while i < len(s):
        c = s[i]
        if c == "\\":
            if i + 1 >= len(s):
                raise TomlError("строка не закрыта")
            e = s[i + 1]
            if e in ('\\', '"'):
                out.append(e)
            elif e == "n":
                out.append("\n")
            elif e == "t":
                out.append("\t")
            else:
                raise TomlError("неизвестная escape-последовательность \\%s" % e)
            i += 2
            continue
        if c == '"':
            return "".join(out), s[i + 1:]
        out.append(c)
        i += 1
    raise TomlError("строка не закрыта")


def trailer(rest):
    rest = rest.strip()
    if rest == "" or rest.startswith("#"):
        return
    raise TomlError("после значения лишнее: %s" % quote(rest))


def parse_array(s):
    items = []
    rest = s[1:]
    while True:
        rest = rest.lstrip(" \t")
        if rest == "":
            raise TomlError("массив не закрыт: многострочные массивы вне подмножества")
        if rest[0] == "]":
            rest = rest[1:]
            break
        if rest[0] != '"':
            raise TomlError("в массиве допустимы только строки в кавычках, вижу %s" % quote(rest))
        v, rest = scan_string(rest)
        items.append(v)
        rest = rest.lstrip(" \t")
        if rest == "":
            raise TomlError("массив не закрыт: многострочные массивы вне подмножества")
        if rest[0] == ",":
            rest = rest[1:]
            continue
        if rest[0] == "]":
            rest = rest[1:]
            break
        raise TomlError("жду запятую или ] после элемента массива, вижу %s" % quote(rest))
    trailer(rest)
    return Value(ARR, items)


def parse_value(s):
    s = s.strip()
    if s == "":
        raise TomlError("нет значения")
    if s[0] == '"':
        v, rest = scan_string(s)
        trailer(rest)
        return Value(STR, v)
    if s[0] == "'":
        raise TomlError("literal-строки вне подмножества, значение пишется в двойных кавычках")
    if s[0] == "[":
        return parse_array(s)
    tok = s.split("#", 1)[0].strip()
    if tok == "true":
        return Value(BOOL, True)
    if tok == "false":
        return Value(BOOL, False)
    if is_int(tok):
        return Value(INT, int(tok))
    raise TomlError("значение %s вне подмножества: строка в кавычках, целое, true/false, массив строк" % quote(tok))


def parse(name, text):
    d = Doc(name)
    cur = d.tables[""]
    cur_name = ""
    for i, raw in enumerate(text.split("\n"), 1):
        s = raw.strip()
        if s == "" or s.startswith("#"):
            continue
        try:
            if s.startswith("["):
                if not s.endswith("]"):
                    raise TomlError("секция не закрыта: %s" % quote(s))
                sect = s[1:-1].strip()
                if not bare_key(sect):
                    raise TomlError("имя секции %s вне подмножества: вложенные таблицы и массивы таблиц не поддержаны" % quote(sect))
                if sect in d.tables:
                    raise TomlError("секция [%s] уже была" % sect)
                cur = {}
                cur_name = sect
                d.tables[sect] = cur
                d.order.append(sect)
                continue
            if "=" not in s:
                raise TomlError("строка %s не разобрана: жду key = value" % quote(s))
            key, _, rest = s.partition("=")
            key = key.strip()
            if not bare_key(key):
                raise TomlError("ключ %s вне подмножества: допустимы буквы, цифры, дефис и подчёркивание" % quote(key))
            if key in cur:
                where = "в секции [%s]" % cur_name if cur_name else "на верхнем уровне"
                raise TomlError("ключ %s %s уже был" % (key, where))
            try:
                v = parse_value(rest)
            except TomlError as e:
                raise TomlError("ключ %s: %s" % (key, e))
            v.line = i
            cur[key] = v
        except TomlError as e:
            raise TomlError("%s:%d: %s" % (name, i, e))
    return d


class ProfileError(Exception):
    pass


def check_types(name, section, table, keys):
    for key, kind in keys:
        v = table.get(key)
        if v is None or v.kind == kind:
            continue
        raise ProfileError("%s: [%s] %s: жду %s, вижу %s"
                           % (name, section, key, KIND_NAMES[kind], KIND_NAMES[v.kind]))


def require_key(name, section, table, key, why):
    if key not in table:
        raise ProfileError("%s: [%s] нет ключа %s (%s)" % (name, section, key, why))


def require_one_of(d, section, key, allowed):
    v = d.str_of(section, key)
    if v not in allowed:
        raise ProfileError("%s: [%s] %s = %s, допустимы %s"
                           % (d.name, section, key, quote(v), ", ".join(allowed)))


def unknown_warns(d, known):
    warns = []
    for sect in d.order:
        t = d.tables[sect]
        if sect not in known:
            if sect == "":
                warns += ["%s: незнакомый ключ %s на верхнем уровне, пропущен" % (d.name, k) for k in t]
                continue
            warns.append("%s: незнакомая секция [%s], пропущена" % (d.name, sect))
            continue
        names = [k for k, _ in known[sect]]
        warns += ["%s: [%s] незнакомый ключ %s, пропущен" % (d.name, sect, k)
                  for k in t if k not in names]
    return warns


def validate_profile(d):
    # Свод из раздела «Валидация»: пять секций на месте, обязательные ключи при
    # выбранных режимах, значения режимов из перечня, spend_* и required
    # ссылаются только на бакеты из buckets. Нарушение это жёсткая ошибка:
    # битый профиль хуже отсутствующего, он молча выключил бы ось.
    known = dict(PROFILE_SCHEMA + OPTIONAL_SCHEMA)
    for section, _ in PROFILE_SCHEMA:
        if section not in d.tables:
            raise ProfileError("%s: нет секции [%s], в профиле обязаны быть все пять "
                               "(detect, rules, delegate, hooks, quota)" % (d.name, section))
    for section, keys in PROFILE_SCHEMA + OPTIONAL_SCHEMA:
        if section in d.tables:
            check_types(d.name, section, d.tables[section], keys)
    validate_detect(d)
    validate_rules(d)
    validate_delegate(d)
    validate_hooks(d)
    validate_quota(d)
    validate_skills(d)
    warns = unknown_warns(d, known)
    for e in d.arr_of("hooks", "events"):
        if e not in KNOWN_EVENTS:
            warns.append("%s: [hooks] events: незнакомое событие %s, пропущено" % (d.name, quote(e)))
    return warns


def validate_detect(d):
    t = d.tables["detect"]
    if not t:
        return
    for key in ("env", "bin"):
        require_key(d.name, "detect", t, key, "секция непуста")


def validate_rules(d):
    t = d.tables["rules"]
    if not t:
        raise ProfileError("%s: секция [rules] пуста, а правила обязаны доезжать до каждого харнеса" % d.name)
    require_key(d.name, "rules", t, "mode", "обязателен всегда")
    require_one_of(d, "rules", "mode", ("import", "embed", "render"))
    mode = d.str_of("rules", "mode")
    if mode == "import":
        for key in ("file", "import_line"):
            require_key(d.name, "rules", t, key, 'при mode = "import"')
    elif mode == "render":
        require_key(d.name, "rules", t, "dir", 'при mode = "render"')


def validate_delegate(d):
    t = d.tables["delegate"]
    require_key(d.name, "delegate", t, "mode",
                "обязателен всегда, у инструмента без делегирования это none")
    require_one_of(d, "delegate", "mode", ("native", "cli", "none"))
    if d.str_of("delegate", "mode") == "cli":
        require_key(d.name, "delegate", t, "command", 'при mode = "cli"')


def validate_hooks(d):
    t = d.tables["hooks"]
    if t:
        require_key(d.name, "hooks", t, "protocol", "секция непуста")


def validate_quota(d):
    t = d.tables["quota"]
    if not t:
        return
    require_key(d.name, "quota", t, "snap", "секция непуста")
    require_one_of(d, "quota", "snap", ("usage-pane", "script"))
    if d.str_of("quota", "snap") == "script":
        require_key(d.name, "quota", t, "script", 'при snap = "script"')
    for key in ("buckets", "required"):
        require_key(d.name, "quota", t, key, "секция непуста")
    spend = ["spend_" + tier for tier in TIERS]
    for key in spend:
        require_key(d.name, "quota", t, key, "секция непуста, ярусы объявляются все четыре")
    buckets = d.arr_of("quota", "buckets")
    # Окно бакета берётся из префикса имени, и имя без известного префикса молча
    # считалось бы недельным: у месячного бюджета pace тогда врёт всемеро.
    for b in buckets:
        if not b.startswith(BUCKET_PREFIXES):
            raise ProfileError("%s: [quota] buckets: имя бакета %s без известного префикса, "
                               "из него берётся окно расчёта; годятся %s"
                               % (d.name, quote(b), ", ".join(BUCKET_PREFIXES)))
    req = d.str_of("quota", "required")
    if req not in buckets:
        raise ProfileError("%s: [quota] required = %s, такого бакета нет в buckets"
                           % (d.name, quote(req)))
    for key in spend:
        for b in d.arr_of("quota", key):
            if b not in buckets:
                raise ProfileError("%s: [quota] %s ссылается на бакет %s, которого нет в buckets"
                                   % (d.name, key, quote(b)))


def validate_skills(d):
    # Секции нет вовсе, значит про ось ещё не разбирались, и правила поедут
    # полным текстом; пустая секция это разобранное «скиллов у инструмента нет».
    # Исход у обоих случаев один, а смысл разный, и различает их генератор.
    t = d.tables.get("skills")
    if not t:
        return
    require_key(d.name, "skills", t, "discovery",
                "секция непуста, иначе непонятно, доезжают скиллы сами или по указателю")
    require_one_of(d, "skills", "discovery", DISCOVERY_VALUES)
    if d.str_of("skills", "discovery") == "auto":
        require_key(d.name, "skills", t, "dir", 'при discovery = "auto" скиллы надо куда-то раскладывать')


def report(name, text, validate=True):
    # Общий с agentctl отчёт по одному файлу: либо отказ, либо канонический дамп
    # с предупреждениями. Им сверяются фикстуры kit/harness/testdata.
    try:
        d = parse(name, text)
    except TomlError as e:
        return "error: %s\n" % e
    warns = []
    if validate:
        try:
            warns = validate_profile(d)
        except ProfileError as e:
            return "error: %s\n" % e
    return d.dump() + "".join("warn: %s\n" % w for w in warns)


def check_profiles(harness_dir):
    # Валидация всех профилей, не только активного: битое находится до того, как
    # кто-то переключится. Возврат это список находок для doctor.
    findings = []
    if not os.path.isdir(harness_dir):
        return ["нет директории профилей харнесов %s" % harness_dir]
    for fn in sorted(os.listdir(harness_dir)):
        if not fn.endswith(".toml"):
            continue
        path = os.path.join(harness_dir, fn)
        with open(path, encoding="utf-8") as f:
            text = f.read()
        try:
            d = parse(fn, text)
            for w in validate_profile(d):
                findings.append("профиль харнеса, предупреждение: %s" % w)
        except (TomlError, ProfileError, OSError) as e:
            findings.append("профиль харнеса битый: %s" % e)
    return findings


def main(argv):
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    for path in argv:
        name = os.path.basename(path)
        with open(path, encoding="utf-8") as f:
            text = f.read()
        sys.stdout.write(report(name, text, validate=not name.startswith("parse-")))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
