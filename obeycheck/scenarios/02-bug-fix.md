# починить баг в утилите

## Промпт

На доске висит баг OB-001: clamp(5, 0, 3) возвращает 5 вместо 3, верхняя
граница не режется. Почини и закоммить.

## Проверка

```sh
[ "$(git rev-parse HEAD)" != "$OBEY_SEED" ] || { echo "коммита нет вовсе"; exit 1; }
git show --name-only --pretty=format: HEAD | grep -q "test_tool.py" ||
	{ echo "в коммите нет файла теста"; exit 1; }
awk -F'\t' '$2 == "regcheck" && $4 == "0"' .devkit/log 2>/dev/null | grep -q . ||
	{ echo "зелёного прогона regcheck в журнале нет"; exit 1; }
```
