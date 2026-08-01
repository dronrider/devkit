#!/usr/bin/env python3
"""Генерация файлов правил проекта: рукописный AGENTS.md, остальное генерится.

  python3 rules.py <директория проекта>
      напечатать эталон для проекта: включённые харнесы, содержимое тонких
      файлов и решение по вклейке. Полезно глазом, когда doctor говорит, что
      файл устарел, а чем именно, из находки не видно.

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

GEN_RE = re.compile(r"^<!-- devkit:generated body=([0-9a-f]{12}) -->$")
BEGIN_RE = re.compile(r"^<!-- devkit:rules begin src=([0-9a-f]{12}) body=([0-9a-f]{12}) -->$")
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


def enabled_harnesses(root, profiles_dir, machine_path=None):
    # Включённые харнесы с их профилями и находки по дороге. Слои те же, что у
    # agentctl: машинный конфиг включает, проектный только сужает.
    findings = []
    machine = Path(os.path.expanduser(machine_path or MACHINE_CONFIG))
    names = list(DEFAULT_ENABLED)
    if machine.exists():
        try:
            d = harness.parse(str(machine), read_text(machine))
        except harness.TomlError as e:
            return [], ["машинный конфиг харнесов не разобран: %s" % e]
        names = list(d.arr_of("", "enabled"))
    proj = Path(root) / PROJECT_CONFIG
    if proj.exists():
        try:
            d = harness.parse(str(proj), read_text(proj))
        except harness.TomlError as e:
            return [], ["проектный конфиг харнесов не разобран: %s" % e]
        if d.get("", "enabled") is not None:
            names = [n for n in d.arr_of("", "enabled") if n in names]
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


def rule_sources(devkit, root, board):
    # Файлы правил, которые проект тянет из devkit. Своё ядро devkit себе не
    # импортирует: RULES.md и есть содержимое этого репозитория, а в сессию оно
    # приезжает глобальным подключением (об этом сказано в его AGENTS.md).
    src = []
    if Path(root).resolve() != Path(devkit).resolve():
        src.append(Path(devkit) / "RULES.md")
    if board:
        src.append(Path(devkit) / "RULES.board.md")
    return src


def thin_text(profile, root, devkit, board, embed):
    # Тонкий файл харнеса: строка-маркер с хешем тела и импорты. При вклейке
    # остаётся один импорт AGENTS.md, в нём правила уже лежат целиком.
    tpl = profile.str_of("rules", "import_line") or "@{path}"
    paths = [AGENTS_FILE]
    if not embed:
        paths += [Path(os.path.relpath(p, root)).as_posix()
                  for p in rule_sources(devkit, root, board)]
    body = "".join(tpl.replace("{path}", p) + "\n" for p in paths)
    return "<!-- devkit:generated body=%s -->\n%s" % (digest(body), body)


def generated_parts(text):
    # (хеш из маркера, тело файла) для генерённого файла, (None, None) для
    # рукописного: файл без маркера генератор считает чужим и не трогает.
    lines = text.split("\n")
    m = GEN_RE.match(lines[0].strip()) if lines else None
    if not m:
        return None, None
    return m.group(1), "\n".join(lines[1:])


def rules_body(sources):
    return "\n".join(read_text(p).strip("\n") + "\n" for p in sources)


def sources_hash(sources):
    # Протухание вклейки против devkit меряется по конкатенации источников в
    # порядке вклейки, а не по тому, что лежит между маркерами.
    return digest("".join(read_text(p) for p in sources))


def block_text(src_hash, body):
    return "<!-- devkit:rules begin src=%s body=%s -->\n%s%s\n" % (
        src_hash, digest(body), body, END_LINE)


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


def check_thin(name, profile, root, devkit, board, embed, fix):
    findings, fixed = [], []
    fname = profile.str_of("rules", "file")
    path = Path(root) / fname
    want = thin_text(profile, root, devkit, board, embed)
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


def check_embed(root, devkit, board, embeds, fix):
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
    sources = rule_sources(devkit, root, board)
    src_hash = sources_hash(sources)
    who = ", ".join(embeds)
    if found is None:
        if fix:
            agents.write_text(put_block(text, block_text(src_hash, rules_body(sources))),
                              encoding="utf-8")
            fixed.append("вклейка правил добавлена в %s: импортов не понимает %s" % (AGENTS_FILE, who))
        else:
            findings.append("в %s нет вклейки правил, а %s импортов не понимает: "
                            "вклеить devkitctl doctor --fix" % (AGENTS_FILE, who))
        return findings, fixed
    _, have_src, body_hash, body, _ = found
    if digest(body) != body_hash:
        findings.append("вклейку правил в %s правили руками: локальным исключениям в общих "
                        "правилах не место, правку перенести в рукописную часть %s либо в сам "
                        "devkit; генератор вклейку не перетирает" % (AGENTS_FILE, AGENTS_FILE))
        return findings, fixed
    if have_src == src_hash:
        return findings, fixed
    if fix:
        agents.write_text(put_block(text, block_text(src_hash, rules_body(sources))),
                          encoding="utf-8")
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
    profiles, f = enabled_harnesses(root, devkit / "harness")
    findings += f
    if not profiles:
        # Молчание тут неотличимо от штатной работы: файлы правил просто не
        # генерятся, и правила до сессии не доезжают вовсе.
        findings.append("включённых харнесов нет, файлы правил генерировать не для кого: "
                        "проверить enabled в %s и в %s" % (MACHINE_CONFIG, PROJECT_CONFIG))
        return findings, fixed
    imports = [(n, p) for n, p in profiles if mode_of(p) == "import"]
    embeds = [n for n, p in profiles if mode_of(p) == "embed"]
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
    ef, ed = check_embed(root, devkit, board, embeds, fix)
    findings += ef
    fixed += ed
    # Вклейку могли только что убрать или добавить, и тонкие файлы считаются по
    # тому, что вышло на диске, а не по тому, что планировалось.
    try:
        embedded = find_block(read_text(agents)) is not None
    except BrokenMarkers:
        embedded = False
    for name, profile in imports:
        tf, td = check_thin(name, profile, root, devkit, board, embedded, fix)
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
    profiles, findings = enabled_harnesses(root, devkit / "harness")
    embeds = [n for n, p in profiles if mode_of(p) == "embed"]
    out = ["включены: %s" % (", ".join("%s (%s)" % (n, mode_of(p)) for n, p in profiles) or "никого"),
           "доска: %s" % ("есть" if board else "нет"),
           "вклейка правил: %s" % (("нужна, импортов не понимает " + ", ".join(embeds))
                                   if embeds else "не нужна, все включённые понимают импорты")]
    for name, profile in profiles:
        if mode_of(profile) != "import":
            continue
        fname = profile.str_of("rules", "file")
        out.append("%s (харнес %s):" % (fname, name))
        out += ["  " + ln for ln in
                thin_text(profile, root, devkit, board, bool(embeds)).rstrip("\n").split("\n")]
    return "\n".join(out + findings) + "\n"


def main(argv):
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    devkit = Path(__file__).resolve().parent.parent
    sys.stdout.write(plan(argv[0], devkit))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
