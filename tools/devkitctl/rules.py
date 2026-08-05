#!/usr/bin/env python3
"""Генерация файлов правил проекта: рукописный AGENTS.md, остальное генерится.

  python3 rules.py <директория проекта>
      напечатать эталон для проекта: включённые харнесы, содержимое тонких
      файлов и решение по вклейке. Полезно глазом, когда doctor говорит, что
      файл устарел, а чем именно, из находки не видно.

  python3 rules.py --layout <глубина> <куда> [директория проекта]
      собрать раскладку правил заданной глубины (full, core, pointers) для
      стенда послушания obeycheck: файлы правил, тонкий файл харнеса и
      глобальная точка в home/. Стенду раскладка это вход, и собирает её
      генератор, чтобы стенд сравнивал то, что реально доезжает до сессии, а
      не собранное руками.

Инвариант из docs/lld/DK-033-universal-kit.md, раздел «Правила: инвариант одной
копии»: рукописный файл в проекте один, AGENTS.md. Инструменту, который умеет
импорты, генерится тонкий файл со ссылкой на AGENTS.md и на правила devkit;
инструменту, который импортов не понимает, текст правил вклеивается между
маркерами в тот же AGENTS.md, и тогда тонкие файлы на devkit больше не ходят,
иначе правила приехали бы в контекст дважды.
"""
import hashlib
import os
import re
import sys
from pathlib import Path

import harness

AGENTS_FILE = "AGENTS.md"
MACHINE_CONFIG = "~/.devkit/harness.local"
PROJECT_CONFIG = ".devkit/harness.local"
# Машинного конфига нет, значит включён claude-code: то же умолчание, по
# которому резолвит харнес agentctl (harness.go, mergeLayers), и оно же
# сегодняшнее поведение до первого прогона мастера настройки.
DEFAULT_ENABLED = ("claude-code",)

# Глубина правил: сколько их текста доезжает до харнеса. Выводится из оси
# скиллов его профиля (docs/lld/DK-100-context-tree.md, раздел «Харнесы без
# скиллов»): скиллы инструмент подхватывает сам, значит ему хватает ядра;
# читает по указателю, значит к ядру прикладывается таблица указателей; оси нет
# вовсе, значит едет полный текст, как ехал всегда.
DEPTH_CORE = "core"
DEPTH_POINTERS = "pointers"
DEPTH_FULL = "full"
DEPTH_TITLES = {
    DEPTH_CORE: "ядро",
    DEPTH_POINTERS: "ядро с указателями на скиллы",
    DEPTH_FULL: "полный текст",
}
# Ядро текста правил лежит рядом с самим текстом: RULES.md -> RULES.core.md.
CORE_SUFFIX = ".core.md"

GEN_RE = re.compile(r"^<!-- devkit:generated (?:depth=(?P<depth>[a-z]+) )?body=(?P<body>[0-9a-f]{12}) -->$")
BEGIN_RE = re.compile(r"^<!-- devkit:rules begin (?:depth=[a-z]+ )?src=([0-9a-f]{12}) body=([0-9a-f]{12}) -->$")
END_LINE = "<!-- devkit:rules end -->"
IMPORT_RE = re.compile(r"^@\S+$")
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")


class BrokenMarkers(Exception):
    pass


def digest(text):
    # Первые 12 hex sha256, как в дизайне: хеш тут не защита, а способ отличить
    # свой текст от тронутого руками.
    return hashlib.sha256(text.encode("utf-8")).hexdigest()[:12]


def read_text(path):
    return Path(path).read_text(encoding="utf-8", errors="replace")


def narrow_warns(d, path, machine_names):
    # Что проектный слой понимает, а что нет. Тексты те же, что у Go-стороны
    # (tools/agentctl/harness.go, narrowByProject): один и тот же конфиг обе
    # реализации обязаны разбирать одинаково, включая то, о чём говорят вслух.
    warns = []
    for k in d.tables[""]:
        if k != "enabled":
            warns.append("%s: ключ %s проектному слою не положен, понимается только enabled"
                         % (path, k))
    for sect in d.order:
        if sect:
            warns.append("%s: секция [%s] проектному слою не положена, маппинг ярусов машинный"
                         % (path, sect))
    for name in d.arr_of("", "enabled"):
        if name not in machine_names:
            warns.append("%s: %s сужением не включить, в машинном слое его нет, пропущен"
                         % (path, name))
    return warns


def enabled_harnesses(root, profiles_dir, machine_path=None):
    # Включённые харнесы с их профилями и находки по дороге. Слои те же, что у
    # agentctl: машинный конфиг включает, проектный только сужает. root=None
    # значит «проектного слоя тут нет вовсе»: так читает глобальную точку
    # check_global, ей проект, из которого запущен doctor, не указ.
    findings = []
    machine = Path(os.path.expanduser(machine_path or MACHINE_CONFIG))
    names = list(DEFAULT_ENABLED)
    if machine.exists():
        try:
            d = harness.parse(str(machine), read_text(machine))
        except harness.TomlError as e:
            return [], ["машинный конфиг харнесов не разобран: %s" % e]
        names = list(d.arr_of("", "enabled"))
    if root is not None:
        proj = Path(root) / PROJECT_CONFIG
        if proj.exists():
            try:
                d = harness.parse(str(proj), read_text(proj))
            except harness.TomlError as e:
                return [], ["проектный конфиг харнесов не разобран: %s" % e]
            findings += narrow_warns(d, proj, names)
            if d.get("", "enabled") is not None:
                kept = [n for n in d.arr_of("", "enabled") if n in names]
                findings += ["%s сужен проектным слоем %s" % (n, proj) for n in names if n not in kept]
                names = kept
    out = []
    for name in names:
        path = Path(profiles_dir) / ("%s.toml" % name)
        if not path.exists():
            findings.append("харнес %s включён, а профиля %s нет: правила ему не сгенерировать"
                            % (name, path))
            continue
        try:
            d = harness.parse(path.name, read_text(path))
            harness.validate_profile(d)
        except (harness.TomlError, harness.ProfileError):
            # Сам битый профиль называет отдельная проверка доктора, тут только
            # следствие: генерировать по нему нечего.
            findings.append("харнес %s включён, а его профиль не проходит валидацию: "
                            "файлы правил по нему не генерятся" % name)
            continue
        out.append((name, d))
    return out, findings


def mode_of(profile):
    return profile.str_of("rules", "mode")


def declared_depth(profile):
    # Глубина, которую харнес объявил, и чем именно. Секции нет вовсе, значит про
    # ось скиллов у этого инструмента ещё не разбирались; пустая секция это
    # разобранное «скиллов у него нет». Глубина у обоих случаев полная, а сказать
    # о них надо разное, иначе неразобранное не отличить от разобранного.
    t = profile.tables.get("skills")
    if t is None:
        return DEPTH_FULL, "оси скиллов профиль не объявил"
    if not t:
        return DEPTH_FULL, "секция [skills] пуста, скиллов у инструмента нет"
    how = profile.str_of("skills", "discovery")
    if how == "auto":
        return DEPTH_CORE, 'discovery = "auto", скиллы инструмент подхватывает сам'
    if how == "manual":
        return DEPTH_POINTERS, 'discovery = "manual", скиллы читаются по указателю'
    return DEPTH_FULL, "значение discovery незнакомо, глубину по нему не вывести"


def core_of(path):
    return Path(str(path)[:-len(".md")] + CORE_SUFFIX)


def rule_sources(devkit, root, board, depth=DEPTH_FULL):
    # Файлы правил, которые проект тянет из devkit. Своё ядро devkit себе не
    # импортирует: RULES.md и есть содержимое этого репозитория, а в сессию оно
    # приезжает глобальным подключением (об этом сказано в его AGENTS.md).
    src = []
    if Path(root).resolve() != Path(devkit).resolve():
        src.append(Path(devkit) / "RULES.md")
    if board:
        src.append(Path(devkit) / "RULES.board.md")
    if depth == DEPTH_FULL:
        return src
    return [core_of(p) if core_of(p).exists() else p for p in src]


def actual_depth(devkit, root, board, depth):
    # Глубина, которая доехала на самом деле. Ядро режется отдельной задачей, и
    # пока текст не нарезан, объявленная глубина остаётся обещанием: доезжает
    # всё тот же полный текст, и признак в файле показывает его, а не обещание.
    # Указателям ядро нужно не меньше: они заменяют процедуры, уехавшие в
    # скиллы, а рядом с полным текстом стали бы второй копией того же самого.
    if depth == DEPTH_FULL:
        return DEPTH_FULL
    src = rule_sources(devkit, root, board, DEPTH_FULL)
    if src and all(core_of(p).exists() for p in src):
        return depth
    return DEPTH_FULL


def skill_meta(path):
    # Имя и описание скилла из frontmatter. Описание тут не украшение: по нему
    # читатель понимает, когда скилл нужен, и в указателе оно и есть повод.
    lines = read_text(path).split("\n")
    if not lines or lines[0].strip() != "---":
        return None
    name, desc = "", ""
    for ln in lines[1:]:
        if ln.strip() == "---":
            break
        key, _, val = ln.partition(":")
        if key.strip() == "name":
            name = val.strip()
        elif key.strip() == "description":
            desc = val.strip()
    return (name, desc) if name else None


def pointers_text(devkit, root):
    # Таблица указателей для инструмента, который скиллы сам не подхватывает.
    # Автоматики тут нет, но и пустого места тоже: указатель говорит, что
    # процедура есть, где она лежит и когда её читать.
    rows = []
    sdir = Path(devkit) / "kit" / "skills"
    for d in sorted(sdir.iterdir()) if sdir.is_dir() else []:
        f = d / "SKILL.md"
        meta = skill_meta(f) if f.exists() else None
        if meta:
            rows.append((meta[0], meta[1], Path(os.path.relpath(f, root)).as_posix()))
    if not rows:
        return ""
    out = ["## Процедуры devkit отдельными файлами", "",
           "Скиллы этот инструмент сам не подхватывает, поэтому процедура доезжает",
           "указателем: описание говорит, когда она нужна, а файл читается целиком",
           "в тот момент, когда дошло до дела.", ""]
    out += ["- %s: %s Файл: `%s`" % row for row in rows]
    return "".join(s + "\n" for s in out)


def gen_marker(depth, body):
    # Признак глубины стоит в маркере только тогда, когда глубина не полная:
    # полная это то, как было всегда, и приписка к ней перегенерила бы каждый
    # тонкий файл на машине, ничего не сказав нового.
    tag = "" if depth == DEPTH_FULL else "depth=%s " % depth
    return "<!-- devkit:generated %sbody=%s -->" % (tag, digest(body))


def thin_text(profile, root, devkit, board, embed, depth=DEPTH_FULL, sources=None,
              agents_root=None):
    # Тонкий файл харнеса: строка-маркер с глубиной и хешем тела, дальше импорты.
    # При вклейке остаётся один импорт AGENTS.md, в нём правила уже лежат.
    # Готовые источники передаёт замер резидента: он собирает раскладку проекта
    # в своей директории, а список файлов и их глубина остаются проектными.
    # agents_root это корп-контур: файл харнеса обязан лежать в корне клона, а
    # AGENTS.md со всем текстом живёт в боковой директории, и первым импортом
    # выписывается путь туда.
    tpl = profile.str_of("rules", "import_line") or "@{path}"
    if embed:
        # Правил тонкий файл тогда не везёт вовсе, они лежат во вклейке, и
        # глубина это про неё: признак тут только гонял бы перегенерацию.
        depth = DEPTH_FULL
    paths = [AGENTS_FILE if agents_root is None
             else Path(os.path.relpath(Path(agents_root) / AGENTS_FILE, root)).as_posix()]
    if not embed:
        paths += [Path(os.path.relpath(p, root)).as_posix()
                  for p in (rule_sources(devkit, root, board, depth)
                            if sources is None else sources)]
    body = "".join(tpl.replace("{path}", p) + "\n" for p in paths)
    if not embed and depth == DEPTH_POINTERS:
        ptr = pointers_text(devkit, root)
        if ptr:
            body += "\n" + ptr
    return "%s\n%s" % (gen_marker(depth, body), body)


def generated_parts(text):
    # (хеш из маркера, тело файла) для генерённого файла, (None, None) для
    # рукописного: файл без маркера генератор считает чужим и не трогает.
    lines = text.split("\n")
    m = GEN_RE.match(lines[0].strip()) if lines else None
    if not m:
        return None, None
    return m.group("body"), "\n".join(lines[1:])


def rules_body(sources):
    return "\n".join(read_text(p).strip("\n") + "\n" for p in sources)


def sources_hash(sources, extra=""):
    # Протухание вклейки против devkit меряется по конкатенации источников в
    # порядке вклейки, а не по тому, что лежит между маркерами. Таблица
    # указателей идёт туда же: скилл добавили, а вклейка про него молчит, и
    # заметить это по хешам источников иначе нечем.
    return digest("".join(read_text(p) for p in sources) + extra)


def block_text(src_hash, body, depth=DEPTH_FULL):
    tag = "" if depth == DEPTH_FULL else "depth=%s " % depth
    return "<!-- devkit:rules begin %ssrc=%s body=%s -->\n%s%s\n" % (
        tag, src_hash, digest(body), body, END_LINE)


DEPTH_ORDER = (DEPTH_CORE, DEPTH_POINTERS, DEPTH_FULL)


def embed_depth(depths):
    # Вклейка в AGENTS.md одна на всех, поэтому глубина у неё самая полная из
    # запрошенных: инструменту, которому нужен весь текст, урезанного не хватит.
    return max(depths, key=DEPTH_ORDER.index) if depths else DEPTH_FULL


def fenced(text):
    # Номера строк (с нуля), лежащих в блоках кода, вместе с самими заборами.
    # Маркер и строка импорта внутри забора это пример из документации, а не
    # вклейка и не импорт: дизайн этой задачи показывает и то и другое.
    out, fence, start = set(), "", 0
    for i, ln in enumerate(text.split("\n")):
        fm = FENCE_RE.match(ln)
        if fm and not fence:
            fence, start = fm.group(1), i
            continue
        if fm and fence and fm.group(1)[0] == fence[0] \
                and len(fm.group(1)) >= len(fence) and not ln[fm.end():].strip():
            out.update(range(start, i + 1))
            fence = ""
            continue
        if fence:
            out.add(i)
    return out


def find_block(text):
    # Вклейка в тексте: (до, src, body_hash, тело, после) либо None. Неполные
    # маркеры это BrokenMarkers: в такой файл генератор не пишет, там уже
    # что-то не так.
    lines = text.split("\n")
    skip = fenced(text)
    begin = -1
    for i, ln in enumerate(lines):
        if i not in skip and BEGIN_RE.match(ln.strip()):
            begin = i
            break
    if begin < 0:
        for i, ln in enumerate(lines):
            if i not in skip and ln.strip() == END_LINE:
                raise BrokenMarkers("маркер конца есть, а начала нет")
        return None
    end = -1
    for j in range(begin + 1, len(lines)):
        if j not in skip and lines[j].strip() == END_LINE:
            end = j
            break
    if end < 0:
        raise BrokenMarkers("маркер начала есть, а конца нет")
    m = BEGIN_RE.match(lines[begin].strip())
    before = "".join(ln + "\n" for ln in lines[:begin])
    body = "".join(ln + "\n" for ln in lines[begin + 1:end])
    after = "\n".join(lines[end + 1:])
    return before, m.group(1), m.group(2), body, after


def put_block(text, block):
    # Вклейка живёт в конце рукописного текста: так её видно, и правка выше
    # ничего не сдвигает.
    found = find_block(text)
    if found is None:
        return text.rstrip("\n") + "\n\n" + block
    before, _, _, _, after = found
    return before + block + after


def drop_block(text):
    found = find_block(text)
    if found is None:
        return text
    before, _, _, _, after = found
    return before.rstrip("\n") + "\n" + ("\n" + after.lstrip("\n") if after.strip() else "")


def handwritten_imports(text):
    # Строки импорта в рукописной части: AGENTS.md читают и инструменты, которые
    # импортов не понимают вовсе, поэтому такая строка это не доставка правил, а
    # молчаливая потеря. Внутрь вклейки и блоков кода проверка не смотрит: там
    # строка импорта это текст правил, а не импорт.
    out = []
    skip = fenced(text)
    for i, ln in enumerate(text.split("\n")):
        if i in skip:
            continue
        if BEGIN_RE.match(ln.strip()):
            break
        if IMPORT_RE.match(ln.strip()):
            out.append((i + 1, ln.strip()))
    return out


def block_files(root, skip_dirs=()):
    # Файлы дерева с маркером вклейки: копия текста правил в проекте должна
    # быть одна, и охраняется это скананьем, а не доверием.
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in skip_dirs]
        for fn in sorted(filenames):
            if not fn.endswith(".md"):
                continue
            path = Path(dirpath) / fn
            try:
                text = read_text(path)
            except OSError:
                continue
            skip = fenced(text)
            if any(i not in skip and BEGIN_RE.match(ln.strip())
                   for i, ln in enumerate(text.split("\n"))):
                out.append(os.path.relpath(path, root))
    return sorted(out)


def check_imports(path, root):
    # Импорты генерённого файла обязаны разворачиваться: devkit мог переехать
    # или не быть склонированным вовсе, и тогда правила не доезжают.
    findings = []
    for i, ln in enumerate(read_text(path).split("\n"), 1):
        ln = ln.strip()
        if not IMPORT_RE.match(ln):
            continue
        target = Path(os.path.expanduser(ln[1:]))
        if not target.is_absolute():
            target = Path(path).parent / target
        if not target.exists():
            findings.append("%s:%d: импорт %s не разворачивается (devkit склонирован рядом?)"
                            % (os.path.relpath(path, root), i, ln))
    return findings


def check_thin(name, profile, root, devkit, board, embed, depth, fix, agents_root=None):
    findings, fixed = [], []
    fname = profile.str_of("rules", "file")
    path = Path(root) / fname
    want = thin_text(profile, root, devkit, board, embed, depth, agents_root=agents_root)
    if not path.exists():
        if fix:
            path.write_text(want, encoding="utf-8")
            fixed.append("%s сгенерирован для харнеса %s" % (fname, name))
        else:
            findings.append("нет %s: правила до харнеса %s не доезжают; "
                            "сгенерировать: devkitctl doctor --fix" % (fname, name))
            return findings, fixed
    else:
        have = read_text(path)
        stamp, body = generated_parts(have)
        if stamp is None:
            findings.append("%s без маркера devkit:generated, генератор его не трогает: "
                            "проектное перенести в %s, файл удалить, тонкий сгенерирует "
                            "devkitctl doctor --fix" % (fname, AGENTS_FILE))
        elif digest(body) != stamp:
            findings.append("%s правлен руками, содержимое разошлось с хешем маркера: "
                            "правку перенести в %s, файл удалить, тонкий сгенерирует "
                            "devkitctl doctor --fix" % (fname, AGENTS_FILE))
        elif have != want:
            if fix:
                path.write_text(want, encoding="utf-8")
                fixed.append("%s перегенерирован: состав правил или харнесов изменился" % fname)
            else:
                findings.append("%s устарел: состав правил или харнесов изменился; "
                                "перегенерировать: devkitctl doctor --fix" % fname)
    findings += check_imports(path, root)
    return findings, fixed


def tilde_path(path):
    # Путь от ~ там, где файл лежит внутри home, иначе абсолютный: то же
    # представление, в каком сегодняшняя глобальная точка называет devkit
    # руками, и оно переживает переезд devkit на другую машину лучше жёсткого
    # абсолютного пути.
    path = Path(path).resolve()
    home = Path.home().resolve()
    try:
        return "~/%s" % path.relative_to(home).as_posix()
    except ValueError:
        return str(path)


def global_target(devkit):
    # Что именно тянет глобальная точка: ядро, если оно уже нарезано, иначе
    # полный текст, как и rule_sources для проектных тонких файлов.
    core = core_of(Path(devkit) / "RULES.md")
    return core if core.exists() else Path(devkit) / "RULES.md"


def global_thin_text(profile, devkit):
    # Глобальная точка ловит любую сессию на машине, а не только проект
    # devkit: AGENTS.md и правила доски ей взять неоткуда, поэтому тело не
    # тонкий файл проекта, а короткая шапка плюс один импорт. Прозы ровно на
    # случай, когда импорт почему-то не развернулся (devkit не склонирован
    # либо переехал): куда смотреть и что делать до тех пор.
    tpl = profile.str_of("rules", "import_line") or "@{path}"
    body = (
        "Ядро правил работы живёт в devkit (`%s`), подключено импортом ниже.\n"
        "Если импорт не развернулся в контекст, прочитать файл явно перед тем,\n"
        "как писать любую прозу: комментарии, докстринги, README, LLD, описания\n"
        "задач, тексты коммитов и сообщения в чат.\n"
        "\n"
        "%s\n"
    ) % (tilde_path(devkit), tpl.replace("{path}", tilde_path(global_target(devkit))))
    return "%s\n%s" % (gen_marker(DEPTH_FULL, body), body)


def check_global(devkit, fix, machine_path=None, whence=""):
    # Глобальная точка правил (`[rules] global_file`): применяется к любой
    # сессии на машине, а не к проекту, из которого запущен doctor, поэтому
    # харнесы читаются машинным слоем без проектного сужения (root=None).
    # devkit тут не то же самое, что DEVKIT вызывающей стороны: правку машины,
    # видной каждой сессии сразу, кладёт только основной чекаут, а worktree
    # ветки задачи передаёт непустой whence и fix=False, как это уже устроено
    # для определений агентов и скиллов.
    findings, fixed = [], []
    profiles, f = enabled_harnesses(None, Path(devkit) / "kit" / "harness", machine_path)
    findings += f
    for name, profile in profiles:
        if mode_of(profile) != "import":
            continue
        gf = profile.str_of("rules", "global_file")
        if not gf:
            continue
        path = Path(os.path.expanduser(gf))
        want = global_thin_text(profile, devkit)
        if not path.exists():
            if fix:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(want, encoding="utf-8")
                fixed.append("%s сгенерирован: глобальная точка правил харнеса %s" % (path, name))
            else:
                findings.append("нет %s: правила харнеса %s до сессий вне проектов devkit не "
                                "доезжают; %sсгенерировать: devkitctl doctor --fix" % (path, name, whence))
            continue
        have = read_text(path)
        stamp, body = generated_parts(have)
        if stamp is None:
            findings.append("%s без маркера devkit:generated, генератор его не трогает: "
                            "локальные добавления перенести в RULES.local.md, файл убрать "
                            "с дороги руками (mv %s %s.bak) и повторить devkitctl doctor --fix"
                            % (path, path, path))
            continue
        if digest(body) != stamp:
            findings.append("%s правлен руками, содержимое разошлось с хешем маркера: "
                            "локальные добавления перенести в RULES.local.md, файл убрать "
                            "с дороги руками (mv %s %s.bak) и повторить devkitctl doctor --fix"
                            % (path, path, path))
            continue
        if have != want:
            if fix:
                path.write_text(want, encoding="utf-8")
                fixed.append("%s перегенерирован: путь devkit или состав ядра изменились" % path)
            else:
                findings.append("%s устарел: путь devkit или состав ядра изменились; "
                                "%sперегенерировать: devkitctl doctor --fix" % (path, whence))
        # Свой мини-check_imports: тот печатает путь относительно проекта, а у
        # глобальной точки проекта нет, и голое имя файла в находке было бы
        # неотличимо от чужого CLAUDE.md.
        for i, ln in enumerate(read_text(path).split("\n"), 1):
            ln = ln.strip()
            if not IMPORT_RE.match(ln):
                continue
            target = Path(os.path.expanduser(ln[1:]))
            if not target.is_absolute():
                target = path.parent / target
            if not target.exists():
                findings.append("%s:%d: импорт %s не разворачивается (devkit склонирован рядом?)"
                                % (path, i, ln))
    return findings, fixed


def check_embed(root, devkit, board, embeds, depth, fix):
    # Вклейка в AGENTS.md: единственная копия текста правил в дереве. src
    # разошёлся, значит devkit обновился и вклейка перегенерируется молча; body
    # разошёлся, значит внутри маркеров правили руками, и такое не перетирается.
    findings, fixed = [], []
    agents = Path(root) / AGENTS_FILE
    text = read_text(agents)
    try:
        found = find_block(text)
    except BrokenMarkers as e:
        findings.append("маркеры вклейки правил в %s поломаны (%s): починить руками, "
                        "в неполные маркеры генератор не пишет" % (AGENTS_FILE, e))
        return findings, fixed
    if not embeds:
        if found is None:
            return findings, fixed
        _, _, body_hash, body, _ = found
        if digest(body) != body_hash:
            findings.append("вклейку правил в %s правили руками, а embed-инструментов больше нет: "
                            "убрать блок руками, перенеся правку в devkit или в рукописную часть"
                            % AGENTS_FILE)
        elif fix:
            agents.write_text(drop_block(text), encoding="utf-8")
            fixed.append("вклейка правил убрана из %s: embed-инструментов среди включённых нет"
                         % AGENTS_FILE)
        else:
            findings.append("в %s лежит вклейка правил, а embed-инструментов среди включённых нет: "
                            "убрать её devkitctl doctor --fix" % AGENTS_FILE)
        return findings, fixed
    sources = rule_sources(devkit, root, board, depth)
    ptr = pointers_text(devkit, root) if depth == DEPTH_POINTERS else ""
    body = rules_body(sources) + ("\n" + ptr if ptr else "")
    src_hash = sources_hash(sources, ptr)
    who = ", ".join(embeds)
    if found is None:
        if fix:
            agents.write_text(put_block(text, block_text(src_hash, body, depth)),
                              encoding="utf-8")
            fixed.append("вклейка правил добавлена в %s: импортов не понимает %s" % (AGENTS_FILE, who))
        else:
            findings.append("в %s нет вклейки правил, а %s импортов не понимает: "
                            "вклеить devkitctl doctor --fix" % (AGENTS_FILE, who))
        return findings, fixed
    _, have_src, body_hash, have_body, _ = found
    if digest(have_body) != body_hash:
        findings.append("вклейку правил в %s правили руками: локальным исключениям в общих "
                        "правилах не место, правку перенести в рукописную часть %s либо в сам "
                        "devkit; генератор вклейку не перетирает" % (AGENTS_FILE, AGENTS_FILE))
        return findings, fixed
    if have_src == src_hash:
        return findings, fixed
    if fix:
        agents.write_text(put_block(text, block_text(src_hash, body, depth)), encoding="utf-8")
        fixed.append("вклейка правил в %s обновлена под devkit" % AGENTS_FILE)
    else:
        findings.append("вклейка правил в %s протухла против devkit: "
                        "обновить devkitctl doctor --fix" % AGENTS_FILE)
    return findings, fixed


def check(root, devkit, fix=False, skip_dirs=()):
    # Состояние файлов правил проекта: находки и список починенного, как их ждёт
    # doctor. Правки additive в том же смысле, что у остальных его починок:
    # генератор пишет только свои файлы и только там, где хеш сошёлся.
    root, devkit = Path(root), Path(devkit)
    findings, fixed = [], []
    board = (root / "docs" / "TASKS.md").exists()
    profiles, f = enabled_harnesses(root, devkit / "kit" / "harness")
    findings += f
    if not profiles:
        # Молчание тут неотличимо от штатной работы: файлы правил просто не
        # генерятся, и правила до сессии не доезжают вовсе.
        findings.append("включённых харнесов нет, файлы правил генерировать не для кого: "
                        "проверить enabled в %s и в %s" % (MACHINE_CONFIG, PROJECT_CONFIG))
        return findings, fixed
    imports = [(n, p) for n, p in profiles if mode_of(p) == "import"]
    embeds = [n for n, p in profiles if mode_of(p) == "embed"]
    depths = {n: actual_depth(devkit, root, board, declared_depth(p)[0]) for n, p in profiles}
    for name in [n for n, p in profiles if mode_of(p) == "render"]:
        findings.append("харнес %s просит режим render, а рендерера правил ещё нет "
                        "(он едет задачей профиля этого инструмента): файлы ему не генерятся" % name)
    agents = root / AGENTS_FILE
    if not agents.exists():
        hand = [p.str_of("rules", "file") for _, p in imports
                if (root / p.str_of("rules", "file")).exists()
                and generated_parts(read_text(root / p.str_of("rules", "file")))[0] is None]
        if hand:
            findings.append("нет %s, а %s рукописный: источник правил проекта теперь %s, "
                            "остальное генерится; перенести и сгенерировать: git mv %s %s, "
                            "потом devkitctl doctor --fix (импорты devkit из %s убрать, "
                            "их выпишет генератор)"
                            % (AGENTS_FILE, hand[0], AGENTS_FILE, hand[0], AGENTS_FILE, AGENTS_FILE))
        else:
            findings.append("нет %s в корне проекта; подключение: devkitctl new --prefix XX"
                            % AGENTS_FILE)
        return findings, fixed
    for i, ln in handwritten_imports(read_text(agents)):
        findings.append("%s:%d: строка импорта %s, а %s читают и инструменты без импортов: "
                        "правила доезжают генерёнными файлами харнесов, строку убрать"
                        % (AGENTS_FILE, i, ln, AGENTS_FILE))
    ef, ed = check_embed(root, devkit, board, embeds,
                         embed_depth([depths[n] for n in embeds]), fix)
    findings += ef
    fixed += ed
    # Вклейку могли только что убрать или добавить, и тонкие файлы считаются по
    # тому, что вышло на диске, а не по тому, что планировалось.
    try:
        embedded = find_block(read_text(agents)) is not None
    except BrokenMarkers:
        embedded = False
    for name, profile in imports:
        tf, td = check_thin(name, profile, root, devkit, board, embedded, depths[name], fix)
        findings += tf
        fixed += td
    blocks = block_files(root, skip_dirs)
    if len(blocks) > 1:
        findings.append("вклейка правил лежит не в одном файле, а в %d (%s): копия текста правил "
                        "в дереве должна быть одна, лишние убрать руками"
                        % (len(blocks), ", ".join(blocks)))
    return findings, fixed


def plan(root, devkit):
    root, devkit = Path(root), Path(devkit)
    board = (root / "docs" / "TASKS.md").exists()
    profiles, findings = enabled_harnesses(root, devkit / "kit" / "harness")
    embeds = [n for n, p in profiles if mode_of(p) == "embed"]
    out = ["включены: %s" % (", ".join("%s (%s)" % (n, mode_of(p)) for n, p in profiles) or "никого"),
           "доска: %s" % ("есть" if board else "нет"),
           "вклейка правил: %s" % (("нужна, импортов не понимает " + ", ".join(embeds))
                                   if embeds else "не нужна, все включённые понимают импорты")]
    depths = {}
    for name, profile in profiles:
        declared, why = declared_depth(profile)
        depths[name] = fact = actual_depth(devkit, root, board, declared)
        line = "глубина правил, %s: %s (%s)" % (name, DEPTH_TITLES[fact], why)
        if fact != declared:
            line += ", хотя объявлено %s: ядро в devkit ещё не нарезано" % DEPTH_TITLES[declared]
        out.append(line)
    for name, profile in profiles:
        if mode_of(profile) != "import":
            continue
        fname = profile.str_of("rules", "file")
        out.append("%s (харнес %s):" % (fname, name))
        out += ["  " + ln for ln in
                thin_text(profile, root, devkit, board, bool(embeds), depths[name])
                .rstrip("\n").split("\n")]
    return "\n".join(out + findings) + "\n"


def import_targets(path):
    # Куда ведут строки импорта файла. Путь от ~ возвращается таким, как записан:
    # в раскладке он ляжет под её home по тому же относительному пути.
    out = []
    for ln in read_text(path).split("\n"):
        ln = ln.strip()
        if IMPORT_RE.match(ln):
            out.append(ln[1:])
    return out


def layout(root, devkit, dst, depth):
    """Раскладка правил заданной глубины: то, что доезжает до сессии.

    Директория с файлами правил, тонким файлом харнеса и глобальной точкой в
    `home/`, ровно в том виде, в каком её ждёт obeycheck. Файлы правил ложатся в
    корень раскладки голыми именами, и импорты тонкого файла зовут их так же:
    раскладка едет в чужой проект, и путь наружу из неё не развернётся.
    """
    root, devkit, dst = Path(root), Path(devkit), Path(dst)
    profiles, findings = enabled_harnesses(root, devkit / "kit" / "harness")
    imports = [(n, p) for n, p in profiles if mode_of(p) == "import"]
    if not imports:
        raise BrokenMarkers("раскладку собирать не для кого: харнеса с импортами "
                            "среди включённых нет (%s)" % ("; ".join(findings) or "проверить enabled"))
    name, profile = imports[0]
    board = (root / "docs" / "TASKS.md").exists()
    dst.mkdir(parents=True, exist_ok=True)
    out = ["раскладка %s: харнес %s, доска %s, глубина %s"
           % (dst, name, "есть" if board else "нет", DEPTH_TITLES[depth])]
    agents = root / AGENTS_FILE
    if agents.is_file():
        (dst / AGENTS_FILE).write_text(read_text(agents), encoding="utf-8")
    else:
        # Проект без рукописного файла правил бывает только синтетический, и
        # тонкий файл всё равно на него сошлётся: пустая ссылка сломала бы импорт.
        (dst / AGENTS_FILE).write_text("# %s: правила проекта\n\nПроектных особенностей нет.\n"
                                       % root.name, encoding="utf-8")
    out.append("  %s" % AGENTS_FILE)
    local, copied = [], []
    for src in rule_sources(devkit, root, board, depth):
        if src.is_file():
            (dst / src.name).write_text(read_text(src), encoding="utf-8")
            local.append(dst / src.name)
            copied.append(src)
            out.append("  %s" % src.name)
    thin = profile.str_of("rules", "file")
    (dst / thin).write_text(thin_text(profile, dst, devkit, board, False, depth, sources=local),
                            encoding="utf-8")
    out.append("  %s" % thin)
    global_file = profile.str_of("rules", "global_file")
    point = Path(os.path.expanduser(global_file)) if global_file else None
    if point is not None and point.is_file():
        home = dst / "home"

        def put_home(spec, src):
            rel = spec[2:] if spec.startswith("~/") else spec.lstrip("/")
            (home / rel).parent.mkdir(parents=True, exist_ok=True)
            (home / rel).write_text(read_text(src), encoding="utf-8")
            out.append("  home/%s%s" % (rel, "" if src == point else " (из %s)" % src.name))

        # Глобальная точка тянет текст правил своим импортом, и в раскладке он
        # обязан быть той же глубины: иначе рядом с ядром приезжает полный
        # текст, и сравнивать стенду нечего. Ключ строится и по полному имени,
        # и по имени его ядра: глобальную точку генерит devkitctl и называет в
        # ней ядро напрямую, а до его нарезки (или на чужой машине с ручной
        # правкой) она называла полный текст, обычно через симлинк в
        # ~/.claude, и оба случая обязаны сойтись с тем, что уже тянет тонкий
        # файл.
        deep = {}
        for p in rule_sources(devkit, root, board, DEPTH_FULL):
            target_file = core_of(p) if core_of(p).is_file() and depth != DEPTH_FULL else p
            deep[p.name] = target_file
            if core_of(p).is_file():
                deep[core_of(p).name] = target_file
        drop = []
        for spec in import_targets(point):
            target = Path(os.path.expanduser(spec))
            if not spec.startswith("~/") or not target.is_file():
                continue
            src = deep.get(target.resolve().name, target)
            if src in copied:
                # Тот же текст уже приезжает импортом тонкого файла. На машине
                # это один файл под двумя путями, и харнес кладёт его в контекст
                # одной копией; тут копии вышло бы две, поэтому строка импорта из
                # глобальной точки снимается вместе с файлом.
                drop.append("@" + spec)
                out.append("  home/%s снят, текст уже в раскладке" % spec[2:])
                continue
            put_home(spec, src)
        text = "".join(ln + "\n" for ln in read_text(point).split("\n")[:-1]
                       if ln.strip() not in drop)
        rel = global_file[2:] if global_file.startswith("~/") else global_file.lstrip("/")
        (home / rel).parent.mkdir(parents=True, exist_ok=True)
        (home / rel).write_text(text, encoding="utf-8")
        out.append("  home/%s" % rel)
    return "\n".join(out) + "\n"


def main(argv):
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    devkit = Path(__file__).resolve().parent.parent.parent
    if argv[0] == "--layout":
        if len(argv) < 3 or argv[1] not in DEPTH_TITLES:
            sys.stderr.write(__doc__)
            return 2
        root = argv[3] if len(argv) > 3 else devkit
        try:
            sys.stdout.write(layout(root, devkit, argv[2], argv[1]))
        except BrokenMarkers as e:
            sys.stderr.write("%s\n" % e)
            return 2
        return 0
    sys.stdout.write(plan(argv[0], devkit))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
