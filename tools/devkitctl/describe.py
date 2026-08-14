#!/usr/bin/env python3
"""Полнота доки: у каждого компонента сборки есть описание (DK-071).

  python3 describe.py <директория проекта>
      напечатать компоненты без описания построчно. Выход 0 чисто, 1 находки,
      2 ошибка запуска.

Контролёры доки смотрят в дифф ветки, и вопрос «а всё ли в проекте
описано» не задаёт никто: компонент, проехавший мимо доки при рождении,
не описывается уже никогда, потому что последующие правки в нём мелкие и
порога «заметное поведение» не берут. Тут проверяется не дифф, а всё дерево
сразу, и долг, накопленный до правила про дока-вместе-с-правкой,
находится тем же прогоном, что и свежий пропуск.

Компонент считается описанным, если есть любое из трёх: заголовок со
ссылкой на него в карте архитектуры, README.md рядом с кодом, модульная
дока в корневом файле компонента. В devkit дока живёт как README рядом с
кодом, в проектах с десятком членов сборки как разделы одного
ARCHITECTURE.md, и навязывать один стиль поверх второго незачем.
"""
import os
import re
import subprocess
import sys
from pathlib import Path

# Члены сборки читаются из манифеста в корне: он точнее каталогов, в него
# не попадают ни генерённый target, ни подставной local-docs, ни сторонний
# каталог рядом с репозиторием. Запись это (манифест, regex): строка members
# либо блок use, из которых вынимаются пути членов. Имя члена это последняя
# компонента его пути. gradle записан отдельно: список членов там не строка,
# а блок include с кавыченными именами через двоеточие.
MANIFESTS = (
    ("Cargo.toml", re.compile(r"members\s*=\s*\[(?P<body>[^\]]*)\]", re.S)),
    ("go.work", re.compile(r"(?m)^\s*use\s*(?:\((?P<body>[^)]*)\)|(?P<one>\S+))",
                           re.S)),
)
GRADLE_SETTINGS = ("settings.gradle", "settings.gradle.kts")
GRADLE_RE = re.compile(r"(?m)^\s*include\b(?P<body>[^\n]+)")
GRADLE_ITEM_RE = re.compile(r"'([^']+)'")
# Карта архитектуры: markdown, в котором компонент назван заголовком со
# ссылкой в его каталог, как «### 4.7 [xr-relay](../xr-relay/)», либо
# строкой таблицы или списка состава «| [xr-hub/](../xr-hub/) | роль |».
# У строки состава текст ссылки совпадает с именем каталога, на который
# она ведёт, и по этому совпадению состав отличается от прозы, где имя
# компонента бывает и метафорой (сеть хабов), и цитатой из другого
# раздела. Заборы кода пропускаются: ссылка в примере это текст.
ARCH_MAPS = ("docs/ARCHITECTURE.md", "ARCHITECTURE.md")
LINK_RE = re.compile(r"\[([^\]]*)\]\(([^)\s]+)\)")
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
HEADING_RE = re.compile(r"^#{1,6}\s")
ROW_RE = re.compile(r"^(?:[-*+]\s+(?:\[[ x]\]\s+)?|\|)\s*")
# Корневой файл с модульной докой: rust //! у main.rs/lib.rs, go //
# Package у doc.go либо у файла с именем пакета, python docstring у
# __init__.py. Файл лежит в src/, если у компонента есть этот каталог
# (rust-крейт), и в корне, если нет. Дока живая, если первая строка
# начинается с имени компонента: xr-setup так и начинает («xr-setup:
# идемпотентный установщик»), а список mod-строк имени не содержит вовсе.
# Go-пакет называет себя словом Package и идёт именем пакета, которое
# совпадает с именем каталога (internal/frame это Package frame), а не
# компонента, поэтому имени каталога в доке не ищем, а ищем только
# rust/python начало с имени. Корневого файла с докой в C-стиле
# (/* */) в живых проектах не случилось, и парсер под выдуманную форму
# осложнял бы находку ложным шумом; новому языку место в списке.
ROOT_DOC_DIRS = ("", "src/")
ROOT_DOCS = (
    ("main.rs", re.compile(r"^\s*//!\s*(.+)"), "name"),
    ("lib.rs", re.compile(r"^\s*//!\s*(.+)"), "name"),
    ("__init__.py", re.compile(r'^\s*(?:"""|\'\'\')\s*(.+)'), "name"),
    ("doc.go", re.compile(r"^\s*//\s*(?:Doc:\s*)?(.+)"), "go"),
    ("{name}.go", re.compile(r"^\s*//\s*Package\s+(\w+)"), "package"),
)
# Сколько слов первой строки доки отсекается под имя: дока начинается
# со статьи или со слова crate, и имя стоит не далее второго слова.
ROOT_DOC_WORDS = 3
# Маркер доки у каталога без языка: материал сессии описывается своими
# файлами, а не README. skills описаны своим SKILL.md, каталог заготовок
# шаблоном, и вычёркивать их в исключения значило бы чинить карту под
# проверку, а не проверку под раскладку. Шаблоны glob, а не один корень:
# скилл лежит на шаг глубже компонента. Новому маркеру место в списке.
DIR_DOCS = ("SKILL.md", "*/SKILL.md", "AGENTS.project.md",
            "templates/AGENTS.project.md")
# Запасной путь, когда манифеста в корне нет: каталоги верхнего уровня с
# кодом. Порог грубее манифеста, зато без привязки к экосистеме.
CODE_SUFFIXES = (".go", ".py", ".rs", ".js", ".ts", ".jsx", ".tsx",
                 ".java", ".kt", ".rb", ".c", ".cc", ".cpp", ".h", ".hpp",
                 ".swift", ".m", ".mm", ".cs", ".php", ".ex", ".exs",
                 ".scala", ".sh")
# Что запасный путь в компоненты не считает: зависимое, генерённое, сторона
# вывода сборки и материал сессии. Список шире FALLBACK_WALK, потому что
# сверху отсекается и то, что кодом притягивает скрипты обвязки (scripts).
FALLBACK_SKIP = {
    ".git", ".devkit", ".github", ".idea", ".vscode", ".venv", "venv",
    "__pycache__", "node_modules", "vendor", "target", "build", "dist",
    "out", "bin", "obj", "local-docs", "docs", "testdata", "test_data",
    "examples", "scripts", "configs", "deploy", "release-staging",
}
# Внутри каталога-кандидата обход не идёт вглубь больших посторонних
# подкаталогов: они не меняют ответ «есть код», а ходить по ним дорого.
FALLBACK_WALK = {".git", ".idea", ".vscode", "node_modules", "target",
                 "__pycache__", "vendor"}
# Куда проект кладёт исключения из проверки: генерённое, вендоренное и
# Android-модули вне манифеста основной сборки. Живёт рядом с deploy.local,
# в гитигнор не идёт: в отличии от адресов выката в вычёрнутом имени секрета
# нет, а ревью видит, кто и зачем вычеркнул компонент из описания.
EXCEPTIONS_FILE = ".devkit/describe.ignore"
HOW_TO = ("заголовок со ссылкой на него в карте архитектуры (%s), README.md "
          "рядом с кодом либо модульная дока в корневом файле компонента"
          % " или ".join(ARCH_MAPS))


def read_lines(path):
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError:
        return []


def members_from_manifest(root):
    """Члены сборки из манифеста в корне: список путей, либо None.

    Пути от корня: из «crates/hub» выходит и имя (последняя компонента), и
    место, где лежат README и модульная дока.
    """
    for name, regex in MANIFESTS:
        lines = read_lines(root / name)
        if not lines:
            continue
        found = []
        for m in regex.finditer("\n".join(lines)):
            chunk = m.group("body") or m.group("one") or ""
            found += chunk.replace(",", " ").split()
        return [item.strip("\"'./") for item in found if item]
    for name in GRADLE_SETTINGS:
        lines = read_lines(root / name)
        if not lines:
            continue
        found = []
        for m in GRADLE_RE.finditer("\n".join(lines)):
            found += GRADLE_ITEM_RE.findall(m.group("body"))
        return [item.replace(":", "/").lstrip("/") for item in found if item]
    return None


def has_code(directory):
    for dp, dirnames, filenames in os.walk(directory):
        dirnames[:] = [d for d in dirnames if d not in FALLBACK_WALK]
        for fn in filenames:
            if fn.endswith(CODE_SUFFIXES):
                return True
    return False


def members_from_dirs(root):
    """Запасной путь: каталоги верхнего уровня, внутри которых есть код."""
    found = []
    for entry in sorted(os.scandir(root), key=lambda e: e.name):
        if not entry.is_dir() or entry.name in FALLBACK_SKIP:
            continue
        if has_code(Path(entry.path)):
            found.append(entry.name)
    return found


def map_headings(root):
    """Строки-заголовки карты архитектуры, заборы кода пропущены.

    Вызов один на карту, а не по компоненту: карта читается единожды,
    и вопрос «кто назван в заголовке» стоит на том же проходе.
    """
    heads = []
    for rel in ARCH_MAPS:
        path = root / rel
        if not path.is_file():
            continue
        fence = ""
        for ln in read_lines(path):
            fm = FENCE_RE.match(ln)
            if fm:
                if not fence:
                    fence = fm.group(1)
                elif fm.group(1)[0] == fence[0] and len(fm.group(1)) >= len(fence):
                    fence = ""
                continue
            if fence or not HEADING_RE.match(ln):
                continue
            heads.append((rel, ln))
    return heads


def map_dirs(root, member):
    """Каталоги, названные картой архитектуры, плюс сам член, если заголовок
    называет его имя. Возвращает множество путей от корня.

    Заголовок со ссылкой в каталог компонента («### xr-hub, [control
    plane](../xr-hub/)») описывает его сам: путь нормализуется от корня,
    карта в docs/ ссылается в каталог как ../xr-hub/, и от корня это
    xr-hub. Заголовок без ссылки, но с именем компонента в тексте
    («### 4.7 xr-relay: слепой транзит»), описывает того, чьё имя несёт:
    имена членов одной сборки не повторяются, и другой каталог с тем же
    именем в проекте не описывается этим заголовком. Строка таблицы
    состава разделом не считается: таблица называет всех подряд, включая
    тех, у кого раздела нет, и по ней находка погасла бы целиком.
    """
    dirs = set()
    word_re = re.compile(r"(?<![\w.-])%s(?![\w-])" % re.escape(member))
    for rel, ln in map_headings(root):
        for m in LINK_RE.finditer(ln):
            target = m.group(2).split("#")[0].rstrip("/")
            if not target or "://" in target or target.startswith("mailto:"):
                continue
            norm = os.path.normpath(os.path.join(os.path.dirname(rel), target))
            if norm and not norm.startswith(".."):
                dirs.add(norm)
        if word_re.search(HEADING_RE.sub("", ln)):
            dirs.add(member)
    return dirs


def documented(root, member, path):
    """Описан ли компонент: README рядом, заголовок в карте, модульная дока."""
    base = root / path
    if (base / "README.md").is_file():
        return "README.md рядом с кодом"
    for pattern in DIR_DOCS:
        if next(base.glob(pattern), None) is not None:
            return "%s рядом с кодом" % pattern
    if path in map_dirs(root, member):
        return "заголовок в карте архитектуры"
    for fname, doc_re, kind in ROOT_DOCS:
        target = fname.format(name=member)
        for d in ROOT_DOC_DIRS:
            src = base / d / target
            if not src.is_file():
                continue
            m = doc_re.match("\n".join(read_lines(src)[:1]))
            if not m:
                continue
            if kind == "package":
                return "модульная дока в %s" % (d + target)
            words = re.findall(r"[\w.-]+", m.group(1))[:ROOT_DOC_WORDS]
            if member in words:
                return "модульная дока в %s" % (d + target)
    return None


def exceptions(root):
    """Имена, вычеркнутые из проверки: {имя: причина} из describe.ignore."""
    out = {}
    for ln in read_lines(Path(root) / EXCEPTIONS_FILE):
        ln = ln.strip()
        if not ln or ln.startswith("#"):
            continue
        name, sep, why = ln.partition(":")
        name = name.strip()
        if not sep or not name:
            continue
        out[name] = why.strip()
    return out


def check(root):
    """Находки doctor: компоненты без описания. Не репозиторий это пустой список."""
    root = Path(root)
    members = members_from_manifest(root)
    if members is None:
        if subprocess.run(["git", "-C", str(root), "rev-parse", "--is-inside-work-tree"],
                          capture_output=True).returncode != 0:
            return []
        members = members_from_dirs(root)
    skip = exceptions(root)
    findings = []
    for path in members:
        name = Path(path).name
        if name in skip:
            continue
        if documented(root, name, path):
            continue
        if name in map_dirs(root, name):
            findings.append(
                "%s: в карте архитектуры имя названо заголовком, а раздела "
                "за ним не нашлось; описать компонент" % path)
        else:
            findings.append("%s: нет описания компонента; %s" % (path, HOW_TO))
    return findings


def main(argv):
    if len(argv) != 2:
        sys.stderr.write("опрос: python3 describe.py <директория проекта>\n")
        return 2
    findings = check(argv[1])
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("все компоненты описаны")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
