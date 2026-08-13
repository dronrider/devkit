"""Проверка раскладки devkit: место файла и язык инструмента (DK-139).

Правило описано в разделе «Раскладка» README.md, разбор в
docs/lld/DK-139-repo-layout.md. Тут его проверяемая часть: белый список
верхнего уровня, места для go, python и sh и граница, за которой sh перестаёт
быть обёрткой. Смысловое (оправдан ли go новому инструменту, верна ли группа
внутри kit/) остаётся на ревью: машина ловит форму.

Ходит проверка по git ls-files, а не по диску: на живом дереве лежат
__pycache__, собранные бинари и рабочие файлы worktree, и по диску она краснела
бы каждый день на исправном репозитории.
"""
import fnmatch
import os
import re
import subprocess
from pathlib import Path

# Белый список верхнего уровня, четырнадцать записей: пять каталогов раскладки,
# два служебных, пять корневых файлов по имени, шаблон правил и .gitignore. Тот
# же состав стоит в разделе «Раскладка» README.md, и сверяет их сторож
# (readme_gaps ниже): разъехавшиеся списки это правило, которого нет ни в тексте,
# ни в проверке. Пятый каталог раскладки, internal/, завёл DK-237: это общий
# go-модуль каркаса утилит, library, а не команда, поэтому под tools/ он не лёг.
TOP_LEVEL = ("tools", "kit", "hooks", "docs", "internal",
             ".github", ".devkit",
             "README.md", "CONNECT.md", "AGENTS.md", "CLAUDE.md", "RANKING.md",
             "ACCEPTANCE.md", "RULES*.md", ".gitignore")
# Места, где sh допустим сверх оболочки скилла: обёртка хука, съёмщик остатка
# квоты и одноразовый сценарий проверки задачи.
SH_PLACES = ("hooks/", "kit/harness/snap/", "docs/tasks/")
# Места, где python живёт сверх оболочки скилла: инструмент и хуки.
PY_PLACES = ("tools/", "hooks/")
# Оболочка скилла лежит рядом со своим SKILL.md, то есть ровно в
# kit/skills/<имя>/. Не «под kit/skills/ где-нибудь»: широкий префикс пропускал
# бы и файл прямо в kit/skills/, и файл в kit/skills/<имя>/sub/, а таких мест
# правило не заводило.
SKILL_DEPTH = 4
# Самопроверка скиллов и её тест это точечное исключение, а не место для кода:
# инструмент проверяет каталог рядом с собой, зовётся путём из чекаута и в
# tools/ его не унести, не разведя с проверяемым. Список закрытый, третий файл
# сюда без правки правила не ляжет.
SKILLS_SELFCHECK = ("kit/skills/check-skills.py", "kit/skills/check_skills_test.py")
# Форму sh не меряют там, где он пишется под чужой контракт или под один прогон.
SHAPE_FREE = ("docs/tasks/", "kit/harness/snap/")
SH_LIMIT = 100
SHEBANG_RE = re.compile(r"^#!.*\b(?:ba|da|z|k)?sh\b")
FUNC_RE = re.compile(r"^[ \t]*(?:function[ \t]+[A-Za-z_][A-Za-z0-9_]*|"
                     r"[A-Za-z_][A-Za-z0-9_]*[ \t]*\([ \t]*\))", re.M)
LOOSE = "в корне лишнее: верхний уровень задан белым списком раскладки"
ONE_LANG = "у инструмента один язык"
TOOL_CODE = "код инструмента лежит в tools/<имя>/"
SH_ONLY = "sh только обёрткой, съёмщиком и сценарием проверки"
SH_BIG = "это уже программа, ей место в python"
MATERIAL = "тут материал, а не код"
# Перечень верхнего уровня в README кончается там, где начинается разговор про
# язык: дальше в разделе backtick'и достаются другим примерам.
README_HEAD = "## Раскладка"
README_TAIL = "Язык инструмента"
TOKEN_RE = re.compile(r"`([^`\s]+)`")
# Каталоги раскладки README перечисляет списком, по каталогу на пункт, а
# корневые файлы и служебные каталоги абзацами следом. Читать весь раздел
# подряд нельзя: дальше в пунктах стоят и `testdata/`, и `SKILL.md`, и они не
# записи верхнего уровня.
BULLET_RE = re.compile(r"^- `([^`\s]+)`", re.M)


def tracked(root):
    """Файлы под git, относительными путями. Не репозиторий это пустой список."""
    p = subprocess.run(["git", "-C", str(root), "ls-files", "-z"],
                       capture_output=True, text=True)
    if p.returncode != 0:
        return []
    return [f for f in p.stdout.split("\0") if f]


def is_devkit(root):
    """Признак самого devkit, тот же, по которому чекаут находит стенд послушания.

    В чужом проекте раскладка своя, и правило туда не едет.
    """
    root = Path(root)
    return (root / "RULES.core.md").is_file() and (root / "hooks").is_dir()


def allowed_top(entry):
    return any(entry == e or fnmatch.fnmatch(entry, e) for e in TOP_LEVEL)


def is_sh(path, rel):
    # Расширения мало: три точки входа git (pre-commit, commit-msg, pre-push)
    # его не имеют, и проверка по одному лишь *.sh не увидела бы именно тех
    # файлов, ради которых правило пишет исключение.
    if rel.endswith(".sh"):
        return True
    if os.path.splitext(rel)[1]:
        return False
    try:
        with open(str(path), encoding="utf-8", errors="replace") as f:
            first = f.readline()
    except OSError:
        return False
    return bool(SHEBANG_RE.match(first))


def read_lines(path):
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError:
        return []


def in_skill(rel):
    """Файл лежит оболочкой скилла, рядом со своим SKILL.md."""
    parts = rel.split("/")
    return parts[:2] == ["kit", "skills"] and len(parts) == SKILL_DEPTH


def lang_rule(rel):
    """Правило, нарушенное файлом кода, либо пустая строка."""
    top = rel.split("/")[0]
    if rel.endswith(".go"):
        if top in ("kit", "docs"):
            return MATERIAL
        if top not in ("tools", "internal"):
            return TOOL_CODE
    if rel.endswith(".py"):
        if top == "docs":
            return MATERIAL
        if in_skill(rel) or rel in SKILLS_SELFCHECK:
            return ""
        if not rel.startswith(PY_PLACES):
            return TOOL_CODE
    return ""


def sh_findings(root, rel):
    findings = []
    if not rel.startswith(SH_PLACES) and not in_skill(rel):
        return ["%s: %s" % (rel, SH_ONLY)]
    if rel.startswith(SHAPE_FREE):
        return findings
    lines = read_lines(Path(root) / rel)
    if len(lines) > SH_LIMIT:
        findings.append("%s: %s (строк %d, граница %d)" % (rel, SH_BIG, len(lines), SH_LIMIT))
    elif FUNC_RE.search("\n".join(lines)):
        findings.append("%s: %s (своя функция)" % (rel, SH_BIG))
    return findings


def tool_findings(files):
    """Инструмент на двух языках и файл, лежащий в tools/ мимо директории."""
    findings, langs = [], {}
    for rel in files:
        parts = rel.split("/")
        if parts[0] != "tools":
            continue
        if len(parts) == 2:
            findings.append("%s: %s, и лежит он в своей директории tools/<имя>/" % (rel, ONE_LANG))
            continue
        # testdata это чужой код (фикстура синтетического проекта у obeycheck
        # написана на python, а инструмент go-шный), и языком инструмента он не
        # считается.
        if "testdata" in parts:
            continue
        seen = langs.setdefault(parts[1], set())
        if parts[-1] == "go.mod" or rel.endswith(".go"):
            seen.add("go")
        elif rel.endswith(".py"):
            seen.add("python")
    for name in sorted(langs):
        if len(langs[name]) > 1:
            findings.append("tools/%s: %s, а тут и go, и python" % (name, ONE_LANG))
    return findings


def check(root):
    """Находки раскладки. Не devkit и не репозиторий это пустой список."""
    root = Path(root)
    if not is_devkit(root):
        return []
    files = tracked(root)
    findings, loose, seen = [], set(), []
    for rel in files:
        entry = rel.split("/")[0]
        if entry in loose or entry in seen:
            continue
        if allowed_top(entry):
            seen.append(entry)
            continue
        loose.add(entry)
        if entry != rel:
            findings.append("%s/: %s" % (entry, LOOSE))
            continue
        # Корневой файл кода нарушает сразу два правила, и полезнее то, которое
        # говорит, куда его класть: «foo.go в корне: код инструмента лежит в
        # tools/<имя>/», а не «в корне лишнее».
        rule = lang_rule(rel)
        if not rule and is_sh(root / rel, rel):
            rule = SH_ONLY
        findings.append("%s: %s" % (rel, rule or LOOSE))
    for rel in sorted(files):
        parts = rel.split("/")
        if parts[0] in loose:
            continue  # про лишнюю запись верхнего уровня уже сказано
        if "testdata" in parts:
            continue
        rule = lang_rule(rel)
        if rule:
            findings.append("%s: %s" % (rel, rule))
            continue
        if is_sh(root / rel, rel):
            findings += sh_findings(root, rel)
    findings += tool_findings(files)
    return findings


def readme_entries(text):
    """Записи верхнего уровня, названные разделом «Раскладка» README."""
    head = text.find(README_HEAD)
    if head < 0:
        return []
    body = text[head:]
    tail = body.find(README_TAIL)
    if tail > 0:
        body = body[:tail]
    got, last = [], 0
    for m in BULLET_RE.finditer(body):
        last = m.end()
        tok = m.group(1)
        if tok.endswith("/") and "/" not in tok[:-1]:
            got.append(tok[:-1])
    for tok in TOKEN_RE.findall(body[last:]):
        if tok.endswith("/"):
            name = tok[:-1]
        elif tok.endswith(".md") or tok == ".gitignore":
            name = tok
        else:
            continue
        if not name or "/" in name or name in got:
            continue
        got.append(name)
    return got


def readme_gaps(text):
    """Сторож: белый список из кода против перечня в README.

    Правило, которое читают, и проверка, которая его стережёт, обязаны называть
    одно и то же. Разъедься они, и сессия, читающая README, считает нарушением
    исправное дерево, а проверка пропускает то, чего в тексте нет.
    """
    gaps = []
    got = readme_entries(text)
    for entry in TOP_LEVEL:
        if not any(name == entry or fnmatch.fnmatch(name, entry) for name in got):
            gaps.append("README не называет запись верхнего уровня «%s»" % entry)
    for name in got:
        if not allowed_top(name):
            gaps.append("README называет «%s», а в белом списке проверки её нет" % name)
    return gaps
