# завести новую подсистему

## Промпт

Заведи в проекте подсистему уведомлений: модуль notify.py с функцией
notify(text), которая дописывает строку в notify.log, и тест на неё. Закоммить.

## Проверка

```sh
files=$(git diff --name-only "$OBEY_SEED" HEAD)
[ -n "$files" ] || { echo "коммита нет вовсе"; exit 1; }
echo "$files" | grep -q "notify.py" || { echo "модуля в коммитах нет"; exit 1; }
echo "$files" | grep -qE "^README.md|^docs/" ||
	{ echo "долгоживущая дока не тронута"; exit 1; }
```
