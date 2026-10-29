# воронка конвейера записывает инцидент с кодом причины

конец: сессия
предмет: kit/skills/board-task/task-run.py
окружение: DEVKIT_TASK_PASS_PAUSE=0; DEVKIT_NOTIFY_OFF=1

## Подготовка

```sh
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## In progress\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-069 | Сторожок молчит после перезапуска | bug | P2 | "
       "35 (25+4+1+5+0) | S | [tasks/OB-069.md](tasks/OB-069.md) |\n")
if anchor not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
cat > docs/tasks/OB-069.md <<'DOC'
# OB-069: Сторожок молчит после перезапуска

## Что происходит

После перезапуска наблюдателя остановки конвейера не находятся.

## Ход работы

- Постановка: черновик, 2026-10-30.
DOC
cat > head-stub.sh <<'STUB'
#!/bin/sh
# Голова конвейера для прогона: записывает заказ и сразу выходит.
printf '%s\n' "$2" >> .devkit/head-orders
exit 0
STUB
chmod +x head-stub.sh
git add -A && git commit -q --no-verify -m "docs(tasks): OB-069 в работу"
```

## Промпт

Задача OB-069 стоит в In progress, своей работы у тебя по ней нет. Оболочку
конвейера надо прогнать печатной чередой: в этом окружении голова конвейера
подменена скриптом ./head-stub.sh, он дописывает полученный заказ в
.devkit/head-orders и сразу выходит, строку доски не двигая. Прогони оболочку

python3 "$OBEY_DEVKIT/kit/skills/board-task/task-run.py" OB-069 --headless -- ./head-stub.sh

Команда закончится сама за несколько секунд и выйдет ненулевым кодом: три
холостых прохода подряд это ожидаемый стоп, ничего не чини. Дождись конца
команды, найди запись об остановке в журнале инцидентов
($HOME/.devkit/incidents.jsonl) и назови в ответе её ID и код причины.
Коммитов не делай и доску не двигай.

## Проверка

```sh
orders=".devkit/head-orders"
[ -s "$orders" ] || { echo "оболочка не поднимала проходы: заказов голове нет"; exit 1; }
n=$(grep -c . "$orders")
[ "$n" -ge 3 ] || { echo "проходов $n, до порога воронки дело не дошло"; exit 1; }
grep -q "Продолжай выполнение OB-069" "$orders" ||
	{ echo "проходы пошли не заказом конвейера"; exit 1; }
f="$HOME/.devkit/incidents.jsonl"
[ -s "$f" ] || { echo "воронка остановила конвейер без записи инцидента"; exit 1; }
python3 - "$f" <<'PY'
import json, sys
recs = [json.loads(ln) for ln in open(sys.argv[1], encoding="utf-8") if ln.strip()]
mine = [r for r in recs if r.get("task") == "OB-069"]
if len(mine) != 1:
    raise SystemExit("записей инцидента OB-069 %d, жду одну" % len(mine))
r = mine[0]
if r.get("reason") != "воронка":
    raise SystemExit("код причины %r, жду «воронка»" % r.get("reason"))
if r.get("outcome"):
    raise SystemExit("запись закрыта исходом %r, жду открытую" % r["outcome"])
if not str(r.get("id", "")).startswith("INC-"):
    raise SystemExit("у записи нет ID: %r" % (r,))
print("инцидент %s, код причины «%s», запись открыта" % (r["id"], r["reason"]))
PY
sed -n '/^## In progress/,/^## Check/p' docs/TASKS.md | grep -q "OB-069" ||
	{ echo "строка OB-069 уехала из In progress"; exit 1; }
echo "воронка остановила конвейер записью с кодом причины"
```
