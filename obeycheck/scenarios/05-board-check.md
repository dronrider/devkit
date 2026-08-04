# перевести задачу в Check

## Подготовка

```sh
sed -i.bak 's/oldtool/tool/g' README.md && rm -f README.md.bak
git add README.md
git commit -q --no-verify -m "docs: OB-002 утилита зовётся tool"
```

## Промпт

Задача OB-002 доделана: README поправлен и закоммичен. Переведи её в Check,
проверять так: `python3 test_tool.py` проходит, а README зовёт утилиту tool.

## Проверка

```sh
awk -F'\t' '$2 == "taskctl" && $4 == "0"' .devkit/log 2>/dev/null | grep -q . ||
	{ echo "taskctl не звался, доску правили руками"; exit 1; }
sed -n '/^## Check/,/^## Backlog/p' docs/TASKS.md | grep -q "OB-002" ||
	{ echo "OB-002 не доехала до Check"; exit 1; }
```
