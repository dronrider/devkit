#!/usr/bin/env python3
"""Проверка бита исполнения у test.sh по всему репозиторию: `tools/devkitctl/README.md`
и подобные доки показывают прогон вида `tools/devkitctl/test.sh` напрямую, а без
бита x команда падает permission denied вместо честной диагностики. Бит
живёт в режиме объекта git (не в правах на диске), поэтому смотрится
git-индекс (`git ls-files -s`): обычный `chmod +x` без `git add`/
`git update-index --chmod=+x` регресс не чинит.

  check-exec-bit.py [-C DIR] [PATTERN...]

Без PATTERN проверяется семейство `test.sh` (корень и любая вложенность).
Находки построчно "путь: режим NNN вместо 100755", выход 1 если есть хоть
одна.
"""
import subprocess
import sys

DEFAULT_PATTERNS = ("test.sh", "**/test.sh")
WANT_MODE = "100755"


def tracked_modes(repo, patterns):
    p = subprocess.run(
        ["git", "-C", repo, "ls-files", "-s", "--"] + list(patterns),
        capture_output=True, text=True,
    )
    if p.returncode != 0:
        # DIR вне репозитория, битый -C, git не в PATH и подобное - это
        # ошибка вызова, а не «находок нет», молчание тут выдало бы чистый
        # прогон там, где чекер вообще не смотрел на репозиторий.
        raise OSError(p.stderr.strip() or "git ls-files завершился с кодом %d" % p.returncode)
    entries = {}
    for line in p.stdout.splitlines():
        mode, _, rest = line.split(" ", 2)
        path = rest.split("\t", 1)[1]
        entries[path] = mode  # разные patterns могут повторить один путь
    return sorted(entries.items())


def main(argv):
    repo = "."
    if argv[:1] == ["-C"]:
        if len(argv) < 2:
            sys.stderr.write(__doc__)
            return 2
        repo, argv = argv[1], argv[2:]
    patterns = argv or list(DEFAULT_PATTERNS)
    try:
        modes = tracked_modes(repo, patterns)
    except OSError as e:
        sys.stderr.write("check-exec-bit: %s\n" % e)
        return 2
    findings = [
        "%s: режим %s вместо %s" % (path, mode, WANT_MODE)
        for path, mode in modes
        if mode != WANT_MODE
    ]
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
