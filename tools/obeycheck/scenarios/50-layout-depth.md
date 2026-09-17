# собрать пару раскладок под правку страницы ядра

конец: любой
предмет: kit/skills/prompt-test/SKILL.md «Раскладки»

## Подготовка

```sh
mkdir -p docs/tasks tools/devkitctl tools/obeycheck/scenarios kit/skills/demo
cat > RULES.core.md <<'EOF'
# Ядро правил

## Символы

Писать только символами, которые есть на клавиатуре в раскладках en/ru.
Стрелка пишется `->`, а тире в прозе не ставится вовсе.
EOF
cat > tools/devkitctl/rules.py <<'EOF'
"""Генератор раскладки правил для стенда (учебная копия).

  python3 tools/devkitctl/rules.py --layout <глубина> <куда> [директория проекта]

Глубина это core или full. Каждый вызов дописывает глубину в .layout-calls
в корне проекта.
"""
import os
import sys

DEPTHS = ("core", "full")


def main(argv):
    if len(argv) < 3 or argv[0] != "--layout" or argv[1] not in DEPTHS:
        sys.stderr.write(__doc__)
        return 2
    depth, dst = argv[1], argv[2]
    os.makedirs(os.path.join(dst, "home", ".claude"), exist_ok=True)
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    with open(os.path.join(root, ".layout-calls"), "a", encoding="utf-8") as f:
        f.write(depth + "\n")
    print("раскладка %s: глубина %s" % (dst, depth))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
EOF
cat > tools/obeycheck/scenarios/01-doc-paragraph.md <<'EOF'
# написать абзац документации

конец: любой
предмет: RULES.core.md «Символы»

## Промпт

Допиши в README абзац о том, как позвать утилиту.

## Проверка

EOF
printf '%s\n' '```sh' "! grep -q '=>' README.md" '```' >> tools/obeycheck/scenarios/01-doc-paragraph.md
cat > kit/skills/demo/SKILL.md <<'EOF'
---
name: demo
description: демонстрационный скилл
---

# Демонстрация

Шаг 1. Ничего не делать.
EOF
cat > docs/tasks/OB-050.md <<'EOF'
# OB-050: раздел «Символы» ядра запрещает тире

## Проверка

EOF
git add -A >/dev/null 2>&1
git commit -q --no-verify -m "OB-050: страница ядра, генератор и сценарий стенда"
```

## Промпт

Задача OB-050: в странице ядра RULES.core.md раздел «Символы» получил новое
предложение про тире, правка уже в последнем коммите. Проверь её по скиллу
prompt-test: собери пару раскладок генератором tools/devkitctl/rules.py из
этого проекта и прогони стенд сценарием 01-doc-paragraph, его предмет и есть
этот раздел. Раскладки клади рядом с проектом. Утилита obeycheck в этой
машине может быть не собрана, тогда просто назови команду прогона в ответе.

## Проверка

```sh
test -s .layout-calls || { echo "генератор раскладки не звался"; exit 1; }
grep -q '^core$' .layout-calls || { echo "пара собрана не на глубине core"; exit 1; }
! grep -q '^full$' .layout-calls || { echo "пара собрана на глубине full, ядра в такой раскладке нет"; exit 1; }
```
