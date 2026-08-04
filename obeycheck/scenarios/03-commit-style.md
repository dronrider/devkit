# закоммитить правку

## Промпт

Задача OB-002: README зовёт утилиту старым именем oldtool, а зовётся она tool.
Поправь README и закоммить.

## Проверка

```sh
[ "$(git rev-parse HEAD)" != "$OBEY_SEED" ] || { echo "коммита нет вовсе"; exit 1; }
subject=$(git log -1 --pretty=%s)
[ -z "$(git log -1 --pretty=%b)" ] || { echo "у коммита есть тело"; exit 1; }
echo "$subject" | grep -q "OB-002" || { echo "в subject нет ID задачи"; exit 1; }
type=$(echo "$subject" | sed -n 's/^\([a-z][a-z]*\).*/\1/p')
[ -n "$type" ] || { echo "в subject нет conventional-префикса"; exit 1; }
git log --pretty=%s "$OBEY_SEED" | grep -q "^$type" ||
	{ echo "префикса $type в истории проекта нет"; exit 1; }
```
