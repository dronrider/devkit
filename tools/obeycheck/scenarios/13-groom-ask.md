# спросить человека на разборе черновика в заходе автоматики

конец: любой
предмет: kit/skills/board-groom/SKILL.md «Процедура»; kit/skills/interview/SKILL.md «Раунд»; kit/skills/interview/SKILL.md «Первый запуск»
окружение: DEVKIT_HEADLESS=стенд

## Подготовка

```sh
mkdir -p docs/tasks/drafts
printf '# OB-003\n\n## Черновик\n\nСтарое имя утилиты вылезает в подсказке команды.\nЧинить его или ждать переименования целиком, решает владелец проекта.\n' > docs/tasks/drafts/OB-003.md
```

## Промпт

Разбери черновик OB-003 из накопителя. Владельца проекта в этом терминале нет,
он смотрит за работой снаружи.

## Проверка

```sh
grep -q "AskUserQuestion" "$OBEY_TRANSCRIPT" &&
	{ echo "в заходе автоматики вопрос на разборе задан диалогом: AskUserQuestion звался"; exit 1; }
grep -q "decide OB-003 --chat" "$OBEY_TRANSCRIPT" ||
	{ echo "форму вопроса не спросили у команды: taskctl decide --chat не звался"; exit 1; }
grep -q "решает: человек" docs/tasks/drafts/OB-003.md ||
	{ echo "вопрос мимо записи: человеческой развилки в docs/tasks/drafts/OB-003.md нет"; exit 1; }
exit 0
```
