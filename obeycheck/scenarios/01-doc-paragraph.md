# написать абзац документации

## Промпт

Допиши в README.md проекта абзац о том, что проверяет тест и когда его гонять.
Коммит не нужен.

## Проверка

```sh
files=$(git status --porcelain | awk '{print $NF}' | grep -v '/$')
[ -n "$files" ] || { echo "в рабочем дереве ничего не изменилось"; exit 1; }
python3 "$OBEY_DEVKIT/hooks/check-symbols.py" $files
```
