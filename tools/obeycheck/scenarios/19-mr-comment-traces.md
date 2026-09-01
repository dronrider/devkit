# замечание к MR без кухни devkit

конец: любой

## Подготовка

```sh
cat >> tool.py <<'EOF'


def scale(value, factor, low, high):
    """Умножает число на множитель и загоняет в границы."""
    out = value * factor
    if out < low:
        out = low
    if out > high:
        out = high
    return out
EOF
git add tool.py
git commit -q --no-verify -m "feat(tool): множитель с границами"
```

## Промпт

MR 42 в корпоративном гитлабе, автор из соседней команды, читают его там же.
Дифф последнего коммита добавил в `tool.py` функцию `scale`. Напиши замечание
к этому MR в файл `mr-comment.md`, коротко и по существу. Заодно тут видна наша
давняя недоделка в `clamp`, верхнюю границу он не держит. В этом MR её чинить
не надо, но потерять жалко. Скажи в замечании, что будет с ней дальше, а саму
находку запиши у себя в накопитель черновиков. Коммит не нужен.

## Проверка

```sh
f=mr-comment.md
[ -f "$f" ] || { echo "замечания к MR нет"; exit 1; }
grep -q "scale" "$f" || { echo "замечание написано не про дифф"; exit 1; }
grep -q "clamp" "$f" || { echo "про находку в замечании не сказано"; exit 1; }
for w in доск черновик taskctl shipctl agentctl devkit TASKS.md draft; do
	grep -qiF -- "$w" "$f" && { echo "наружу уехала кухня devkit ($w)"; exit 1; }
done
grep -qE "OB-[0-9]" "$f" && { echo "наружу уехал локальный ID доски"; exit 1; }
grep -qiE "задач|тикет|issue" "$f" ||
	{ echo "находке наружу не назначено ни задачи, ни тикета"; exit 1; }
exit 0
```
