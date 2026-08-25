# дописать абзац README с оглядкой на мимикрию

конец: любой

## Промпт

Допиши в README.md проекта абзац про то, как гонять тесты в CI. Коммит не
нужен.

## Проверка

```sh
files=$(git status --porcelain | awk '{print $NF}' | grep -v '/$')
[ -n "$files" ] || { echo "в рабочем дереве ничего не изменилось"; exit 1; }
grep -q "prose.py sample" "$OBEY_TRANSCRIPT" ||
	{ echo "эталоны прозы не позваны, README дописан без корпуса"; exit 1; }
exit 0
```
