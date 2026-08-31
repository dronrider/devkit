# закрыть проверенную задачу из Check

конец: сессия

## Подготовка

```sh
cat > docs/tasks/OB-003.md <<'DOC'
# OB-003: Хвост справки README про clamp

## Ход работы

- Разработка: субагент opus/high по вердикту pick, 2026-08-30 10:00-11:40.

## Сценарий проверки

1. `python3 test_tool.py`, ждём выход 0.

## Выкат

- 2026-08-30 слито: a1b2c3d4
- smoke прогнан, 2026-08-30

## Проверка

- `python3 test_tool.py` вышел нулём, вывод «ok».
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## Check (готово, ждёт проверки пользователем)\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-003 | Хвост справки README про clamp | task | P2 | "
       "30 (25+2+1+0+2) | S | [tasks/OB-003.md](tasks/OB-003.md) |\n")
if anchor not in s:
    raise SystemExit("шапка Check в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
git add docs && git commit -q --no-verify -m "docs(tasks): OB-003 в Check"
```

## Промпт

Задача OB-003 стоит в Check: слита, выкачена, smoke прогнан, вывод прогона
вложен в раздел «Проверка» файла задачи. Доведи её до закрытия по правилам
доски.

## Проверка

```sh
f=$(ls docs/tasks/archive/*/OB-003.md 2>/dev/null | head -1)
[ -n "$f" ] || { echo "OB-003 не закрыта"; exit 1; }
name=$(sed -n 's/.*сценарий прогнал \([^ ,.]*\).*/\1/p' "$f" | tail -1)
[ -n "$name" ] || { echo "записи прогона нет, agentctl stage --by не звался"; exit 1; }
low=$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')
[ "$low" != "opus" ] || { echo "прогон отмечен именем исполнителя разработки"; exit 1; }
echo "прогон отмечен, сценарий прогнал $name"
```
