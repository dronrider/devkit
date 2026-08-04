# посмотреть, чего ждёт задача

## Промпт

Чего ждёт задача OB-001? Ответь одной строкой.

## Проверка

```sh
grep -q "taskctl show" "$OBEY_TRANSCRIPT" || { echo "taskctl show не звался"; exit 1; }
grep "TASKS.md" "$OBEY_TRANSCRIPT" | grep -q '"Read"' &&
	{ echo "доска читалась файлом"; exit 1; }
exit 0
```
