#!/usr/bin/env python3
"""Генератор карты проекта уровня компонентов и индекса решений (DK-375).

  python3 codemap.py <директория проекта>
      напечатать карту в stdout (сухой прогон для POC и ревью).

Карта строится по решению 1 из LLD DK-194: статический разбор дерева,
тулчейн не зовётся. Члены сборки берутся из манифеста (Cargo.toml members,
go.work use, settings.gradle include) либо, при отсутствии манифеста, из
каталогов верхнего уровня с кодом. Исключения из .devkit/describe.ignore
вычёркиваются и из карты.

Описание компонента берётся из первого сработавшего источника по решению 2:
модульная дока корневого файла (//! у Rust, Package у Go, docstring у Python),
первая строка README (заголовок «# имя: » срезается), строка состава из
docs/ARCHITECTURE.md. Из найденного описания берётся первое предложение
целиком. Компонент без описания в карту не едет.

Формат карты по решению 4: плоские строки «путь: описание», сортировка по
пути. Вторым разделом идёт индекс решений: строки «DK-XXX.N текст» из
заголовков «Решение N» в docs/lld/*.md.
"""
import os
import re
import hashlib
from pathlib import Path

# Переиспользуемые константы и функции из describe.py
MANIFESTS = (
    ("Cargo.toml", re.compile(r"members\s*=\s*\[(?P<body>[^\]]*)\]", re.S)),
    ("go.work", re.compile(r"(?m)^\s*use\s*(?:\((?P<body>[^)]*)\)|(?P<one>\S+))",
                           re.S)),
)
GRADLE_SETTINGS = ("settings.gradle", "settings.gradle.kts")
GRADLE_RE = re.compile(r"(?m)^\s*include\b(?P<body>[^\n]+)")
GRADLE_ITEM_RE = re.compile(r"'([^']+)'")
ARCH_MAPS = ("docs/ARCHITECTURE.md", "ARCHITECTURE.md")
CODE_SUFFIXES = (".go", ".py", ".rs", ".js", ".ts", ".jsx", ".tsx",
                 ".java", ".kt", ".rb", ".c", ".cc", ".cpp", ".h", ".hpp",
                 ".swift", ".m", ".mm", ".cs", ".php", ".ex", ".exs",
                 ".scala", ".sh")
FALLBACK_SKIP = {
    ".git", ".devkit", ".github", ".idea", ".vscode", ".venv", "venv",
    "__pycache__", "node_modules", "vendor", "target", "build", "dist",
    "out", "bin", "obj", "local-docs", "docs", "testdata", "test_data",
    "examples", "scripts", "configs", "deploy", "release-staging",
}
FALLBACK_WALK = {".git", ".idea", ".vscode", "node_modules", "target",
                 "__pycache__", "vendor"}
EXCEPTIONS_FILE = ".devkit/describe.ignore"
ROOT_DOC_DIRS = ("", "src/")
ROOT_DOCS = (
    ("main.rs", re.compile(r"^\s*//!\s*(.+)"), "name"),
    ("lib.rs", re.compile(r"^\s*//!\s*(.+)"), "name"),
    ("__init__.py", re.compile(r'^\s*(?:"""|\'\'\')\s*(.+)'), "name"),
    ("doc.go", re.compile(r"^\s*//\s*(?:Doc:\s*)?(.+)"), "go"),
    ("{name}.go", re.compile(r"^\s*//\s*Package\s+(\w+)"), "package"),
)
ROOT_DOC_WORDS = 3
DIR_DOCS = ("SKILL.md", "*/SKILL.md", "AGENTS.project.md",
            "templates/AGENTS.project.md")


def read_lines(path):
    """Прочитать файл построчно, вернуть список строк."""
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
    """Проверить, есть ли в каталоге файлы с кодом по суффиксам."""
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


def first_sentence(text):
    """Вернуть первое предложение из текста целиком.

    Предложение заканчивается на .!? либо обрывается концом текста.
    """
    if not text:
        return ""
    # Обрезаем закрывающие кавычки docstring
    text = text.rstrip('"""').rstrip("'''").strip()
    # Ищем конец первого предложения
    for i, char in enumerate(text):
        if char in ".!?":
            # Проверяем, что это действительно конец (не сокращение)
            if i < len(text) - 1 and text[i + 1] == " ":
                return text[:i + 1].strip()
    return text.strip()


def describe_component(root, member, path):
    """Описание компонента по приоритету источников из решения 2.

    Возвращает None, если описание не найдено (компонент в карту не едет).
    """
    base = root / path

    # 1. Модульная дока корневого файла
    for fname, doc_re, kind in ROOT_DOCS:
        target = fname.format(name=member)
        for d in ROOT_DOC_DIRS:
            src = base / d / target
            if not src.is_file():
                continue
            lines = read_lines(src)[:1]  # Первая строка
            if not lines:
                continue
            m = doc_re.match(lines[0])
            if not m:
                continue
            if kind == "package":
                # Go-пакет: строка «Package name»
                return first_sentence(m.group(1))
            # Для остальных проверяем, что имя компонента есть в первых словах
            words = re.findall(r"[\w.-]+", m.group(1))[:ROOT_DOC_WORDS]
            # Case-insensitive проверка для имен компонентов
            if any(member.lower() == word.lower() for word in words):
                return first_sentence(m.group(1))

    # 2. Первая строка README со срезом заголовка
    readme = base / "README.md"
    if readme.is_file():
        lines = read_lines(readme)
        if lines:
            first = lines[0].strip()
            # Срезаем заголовок «# имя: » или «# имя »
            first = re.sub(r"^#+\s*\S+[:\s]*\s*", "", first)
            if first:  # Если после среза остался текст
                return first_sentence(first)

    # 3. Строка состава из ARCHITECTURE.md
    for arch_path in ARCH_MAPS:
        arch = root / arch_path
        if not arch.is_file():
            continue
        lines = read_lines(arch)
        # Ищем строку с именем компонента в таблице состава
        # Формат: «| [имя/](путь) | роль |»
        member_re = re.compile(r"\|\s*\[([^\]]+)\]\([^)]+\)\s*\|")
        in_table = False
        for ln in lines:
            # Проверяем, что мы в таблице (строка начинается с |)
            if not ln.strip().startswith("|"):
                in_table = False
                continue
            in_table = True

            # Ищем совпадение имени компонента
            for m in member_re.finditer(ln):
                name = m.group(1).rstrip("/")
                if name == member or name == path:
                    # Извлекаем описание из следующей колонки
                    parts = [p.strip() for p in ln.split("|")]
                    if len(parts) >= 3:
                        # Третья колонка (role) это описание
                        return first_sentence(parts[2])

    # 4. Маркер доки у каталога без языка
    for pattern in DIR_DOCS:
        if next(base.glob(pattern), None) is not None:
            # Описание берётся из самого файла
            doc_file = next(base.glob(pattern))
            lines = read_lines(doc_file)
            if lines:
                return first_sentence(lines[0])

    return None


def generate_map(root):
    """Сгенерировать карту проекта: (маркер, тело карты, хеш).

    Тело карты это список строк, разделённый на две части: компоненты и
    индекс решений. Хеш считается от объединённого тела.
    """
    root = Path(root)

    # Сбор компонентов
    members = members_from_manifest(root)
    if members is None:
        members = members_from_dirs(root)

    skip = exceptions(root)
    components = []

    for path in members:
        name = Path(path).name
        if name in skip:
            continue

        description = describe_component(root, name, path)
        if not description:
            continue  # Компонент без описания в карту не едет

        components.append((path, description))

    # Сортировка по пути
    components.sort(key=lambda x: x[0])

    # Генерация тела карты
    lines = []
    for path, desc in components:
        lines.append(f"{path}: {desc}")

    # Добавляем индекс решений
    lines.append("")
    lines.append("# Решения по docs/lld")
    lines.append("")
    lines.append("Заголовки «Решение N» из LLD-документов, полный разбор с "
                 "отвергнутыми вариантами в самом документе.")
    lines.append("")

    # Сбор индекса решений
    solutions = parse_lld_index(root)
    solutions.sort(key=lambda x: (x[0], x[1]))  # Сортировка по документу и номеру

    for doc_id, num, title in solutions:
        lines.append(f"{doc_id}.{num} {title}")

    body = "\n".join(lines)
    hash_val = hashlib.sha256(body.encode("utf-8")).hexdigest()[:16]

    marker = f"<!-- devkit:generated map body={hash_val} -->"
    return marker, body, hash_val


def render_map(root):
    """Сгенерировать полный текст карты с маркером и шапкой.

    Возвращает (полный текст с маркером и шапкой, hash_val).
    """
    marker, body, hash_val = generate_map(root)

    lines = []
    lines.append(marker)
    lines.append("")
    lines.append("# Карта проекта")
    lines.append("")
    lines.append("Сгенерировано devkitctl из кода и доки, правится перегенерацией")
    lines.append("`devkitctl doctor --fix`, руками не править.")
    lines.append("")
    lines.append(body)

    full_text = "\n".join(lines)
    # Убеждаемся, что файл заканчивается переводом строки
    if not full_text.endswith("\n"):
        full_text += "\n"
    return full_text, hash_val


def parse_lld_index(root):
    """Собрать индекс решений из docs/lld/*.md.

    Возвращает список кортежей (doc_id, num, title), где doc_id это ID
    документа (DK-XXX), num это номер решения, title это текст заголовка.
    """
    lld_dir = root / "docs" / "lld"
    solutions = []

    if not lld_dir.is_dir():
        return solutions

    # Паттерн для извлечения ID документа из имени файла
    doc_id_re = re.compile(r"^(DK-\d+)", re.IGNORECASE)
    # Паттерн для заголовка «Решение N. Текст»
    solution_re = re.compile(r"^##\s*Решение\s+(\d+)\.\s*(.+)", re.IGNORECASE)

    for lld_file in sorted(lld_dir.glob("*.md")):
        # Архив задач не индексируется
        if "TASK-archive" in lld_file.name or "TASKS" in lld_file.name:
            continue

        # Извлекаем ID документа из имени файла
        doc_match = doc_id_re.match(lld_file.stem)
        if not doc_match:
            continue
        doc_id = doc_match.group(1).upper()

        lines = read_lines(lld_file)
        for ln in lines:
            m = solution_re.match(ln)
            if m:
                num = int(m.group(1))
                title = m.group(2).strip()
                solutions.append((doc_id, num, title))

    return solutions


def main(argv):
    if len(argv) != 2:
        import sys
        sys.stderr.write(__doc__)
        return 2

    root = argv[1]
    marker, body, hash_val = generate_map(root)

    # Печатаем карту целиком
    print(marker)
    print("")
    print("# Карта проекта")
    print("")
    print("Сгенерировано devkitctl из кода и доки, правится перегенерацией")
    print("`devkitctl doctor --fix`, руками не править.")
    print("")
    print(body)
    return 0


if __name__ == "__main__":
    import sys
    sys.exit(main(sys.argv))
