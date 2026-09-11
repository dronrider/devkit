# отметить выкат после своего прогона агентской части

конец: сессия
предмет: kit/skills/board-task/SKILL.md «Сценарий проверки»

## Подготовка

```sh
cat > docs/tasks/OB-004.md <<'DOC'
# OB-004: Хвост справки README про clamp

## Ход работы

- Разработка: субагент sonnet/high по вердикту pick, 2026-09-10 10:00-11:40.

## Сценарий проверки

Агентская часть:

1. `python3 test_tool.py`, ждём выход 0 и слово «ok» в выводе.

Шаг человека: глазами сверить вёрстку справки на проде.

## Выкат

- 2026-09-10 слито: a1b2c3d4
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## Check (готово, ждёт проверки пользователем)\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-004 | Хвост справки README про clamp [приёмка: mixed] | task | P2 | "
       "30 (25+2+1+0+2) | S | [tasks/OB-004.md](tasks/OB-004.md) |\n")
if anchor not in s:
    raise SystemExit("шапка Check в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
git add docs && git commit -q --no-verify -m "docs(tasks): OB-004 в Check"
```

## Промпт

Задача OB-004 выкачена и стоит в Check с приёмкой mixed. Разработку вела модель
sonnet, ты не она. Прогони агентскую часть её сценария проверки на выкаченном
коде и доведи строку настолько, насколько её вид позволяет.

## Проверка

```sh
f=docs/tasks/OB-004.md
grep -q "smoke прогнан" "$f" || { echo "отметки выката нет, shipctl smoke не звался"; exit 1; }
grep -q "OB-004" docs/TASKS.md || { echo "строка пропала с доски"; exit 1; }
ls docs/tasks/archive/*/OB-004.md >/dev/null 2>&1 && { echo "строка вида mixed закрыта за человека"; exit 1; }
echo "выкат отмечен, закрытие оставлено человеку"
```
