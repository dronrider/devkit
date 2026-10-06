# припарковать вопрос из Backlog и оставить внешний блок в Backlog

конец: сессия
предмет: RULES.board.md «Трекинг задач (доска в репозитории)»; kit/skills/board-task/SKILL.md «Статусы»

## Подготовка

```sh
python3 - <<'EOF'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
head = "| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"
rows = (
    "| OB-003 | README ждёт новое имя утилиты | task | P2 | 30 (25+2+1+0+2) | S | [tasks/OB-003.md](tasks/OB-003.md) |\n"
    "| OB-004 | Справка tool ждёт доступ к стенду | task | P2 | 28 (25+2+1+0+2) | S | [tasks/OB-004.md](tasks/OB-004.md) |\n"
)
s = s.replace("## Backlog\n\n" + head, "## Backlog\n\n" + head + rows)
open(p, "w", encoding="utf-8").write(s)
EOF
printf '# OB-003: README ждёт новое имя утилиты\n\n## Что происходит\n\nREADME зовёт утилиту старым именем oldtool. Новое имя решает владелец проекта, в репозитории его нет нигде.\n' > docs/tasks/OB-003.md
printf '# OB-004: Справка tool ждёт доступ к стенду\n\n## Что происходит\n\nСправка tool описывает вывод по стенду, доступа к нему с машины нет и не будет до следующей недели, сверить описание не с чем.\n' > docs/tasks/OB-004.md
git add docs/TASKS.md docs/tasks/OB-003.md docs/tasks/OB-004.md && git commit -qm "docs(tasks): OB-003 ждёт имени, OB-004 ждёт доступа"
```

## Промпт

Ты ведёшь задачи доски проекта, и ход сейчас закончишь.

OB-003 в Backlog: README надо перевести на новое имя утилиты, а само имя
решает владелец проекта и в репозитории его нет нигде. Без ответа человека
начинать нечего.

OB-004 в Backlog: справку tool надо сверить со стендом, а доступа к стенду с
машины нет и не будет до следующей недели.

Оформи ожидание по обеим задачам на доске по правилам доски этого
проекта, и закончи ход. Код не трогай, имя не выдумывай.

## Проверка

```sh
awk -F'\t' '$2 == "taskctl" && $4 == "0"' .devkit/log 2>/dev/null | grep -q . ||
	{ echo "taskctl не звался, доску правили руками"; exit 1; }
grep -q '^| OB-003 .*\[блок: вопрос: ' docs/TASKS.md ||
	{ echo "OB-003 не припаркована вопросом: в строке нет «[блок: вопрос: ...]»"; grep 'OB-003' docs/TASKS.md; exit 1; }
grep -q '^| OB-004 .*\[блок:' docs/TASKS.md &&
	{ echo "OB-004 припаркована, а прочие блоки требуют начатой задачи"; grep 'OB-004' docs/TASKS.md; exit 1; }
sed -n '/^## Backlog/,/^## Blocked/p' docs/TASKS.md | grep -q 'OB-004' ||
	{ echo "OB-004 не осталась в Backlog"; grep 'OB-004' docs/TASKS.md; exit 1; }
echo "OB-003 ждёт ответа вопросом из Backlog, OB-004 осталась в Backlog"
```
