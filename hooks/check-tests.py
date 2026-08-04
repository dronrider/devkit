#!/usr/bin/env python3
"""Проверка правила «Тесты обязательны» RULES.md на рубеже коммита: правка кода
едет вместе с тестами, поэтому коммит, где код есть, а тестового файла нет, это
находка.

Смотрятся имена файлов коммита, а не их содержимое: тест, дописанный к правке,
это либо новый тестовый файл, либо изменённый старый, и то и другое видно по
имени. Что считается кодом, а что тестом, задано перечнями ниже.

  git diff --cached --name-only --diff-filter=ACMR | check-tests.py --stdin
  check-tests.py <путь>...

Выход 0 чисто, 1 находка. Обход осознанный: DEVKIT_NO_TESTS=1 в окружении
коммита (пропускает только эту проверку) либо git commit --no-verify.
"""
import sys

CODE_EXT = (
    ".py", ".go", ".sh", ".bash", ".zsh", ".rs", ".rb", ".pl", ".lua",
    ".js", ".jsx", ".ts", ".tsx", ".mjs", ".vue", ".svelte",
    ".c", ".h", ".cc", ".cpp", ".hpp", ".m", ".mm", ".java", ".kt", ".kts",
    ".cs", ".php", ".swift", ".scala", ".ex", ".exs", ".erl", ".sql",
)

# Каталоги и имена, по которым файл читается как тестовый. testdata тут же:
# фикстура это часть теста, и правка, приехавшая с новой фикстурой, тестами
# закрыта.
TEST_DIRS = ("test", "tests", "spec", "specs", "testdata", "fixtures", "__tests__")
TEST_MARKS = ("_test.", ".test.", "test_", "_spec.", ".spec.", "spec_")


def is_test(path):
    parts = path.replace("\\", "/").split("/")
    if any(d in TEST_DIRS for d in parts[:-1]):
        return True
    name = parts[-1]
    return name.startswith("test.") or any(m in name for m in TEST_MARKS)


def is_code(path):
    name = path.replace("\\", "/").split("/")[-1]
    if name.endswith(CODE_EXT):
        return True
    # Утилиты devkit и git-хуки живут без расширения, и шебанг тут
    # единственный признак кода. Файла может уже не быть на диске (коммит
    # переименования), тогда он просто не код.
    if "." in name:
        return False
    try:
        with open(path, "rb") as f:
            return f.read(2) == b"#!"
    except OSError:
        return False


def check(paths):
    code = [p for p in paths if not is_test(p) and is_code(p)]
    if not code or any(is_test(p) for p in paths):
        return []
    return ["правка кода без единого теста в коммите (RULES.md, «Тесты обязательны»):\n"
            + "\n".join("  " + p for p in code[:20]) + "\n"
            "что делать: дописать тест той же правкой, а красноту проверить прогоном,\n"
            "  а не на глаз: regcheck -- <команда теста>, тест бага обязан падать на\n"
            "  старом коде и проходить на исправленном;\n"
            "если тесту взяться неоткуда (перенос файла, правка сборки), обход осознанный:\n"
            "  DEVKIT_NO_TESTS=1 git commit ... либо git commit --no-verify"]


def main(argv):
    if argv[:1] == ["--stdin"]:
        paths = [ln.strip() for ln in sys.stdin if ln.strip()]
    elif argv:
        paths = argv
    else:
        sys.stderr.write(__doc__)
        return 2
    findings = check(paths)
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
