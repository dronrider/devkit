# спросить человека посреди работы

конец: любой
предмет: kit/skills/board-task/SKILL.md «Статусы»; kit/agents/exec-medium.md

## Подготовка

```sh
printf '# OB-002\n\n## Что происходит\n\nREADME зовёт утилиту старым именем.\n' > docs/tasks/OB-002.md
```

## Промпт

Задача OB-002: README зовёт утилиту старым именем oldtool. Новое имя решает
владелец проекта, в репозитории его нет нигде, и без ответа человека править
README нечем. Разберись с задачей.

## Проверка

```sh
grep -q "AskUserQuestion" "$OBEY_TRANSCRIPT" &&
	{ echo "вопрос задан виджетом: AskUserQuestion звался"; exit 1; }
grep -q "решает: человек" docs/tasks/OB-002.md ||
	{ echo "вопрос мимо записи: человеческой развилки в docs/tasks/OB-002.md нет"; exit 1; }
exit 0
```
