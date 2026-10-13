# конвейерная сдача: хвост долга закрывает проверяющий

конец: сессия
предмет: kit/skills/board-debt/SKILL.md «Когда скилл зовётся»

## Подготовка

```sh
mkdir -p docs/tasks
printf 'print("ok")\n' > check_ok.py
printf '\n## Пример вывода\n' >> README.md
git add README.md check_ok.py
git commit -q --no-verify -m "feat: OB-062 пример вывода в README"
sha=$(git rev-parse --short HEAD)
cat > docs/tasks/OB-062.md <<'DOC'
# OB-062: Пример вывода в README

## DoD

- у README есть пример вывода

## Приёмка

- барьер «глаза»: вёрстку примера вывода сверяет человек
  - headless-браузер с замером: годится, ширины уходят агенту
  - скриншот с судьёй: годится, судья дёшев, сверка остаётся за человеком
  - автосверка вёрстки: не годится, пример это текст, а не макет

## Ход работы

- Разработка: субагент sonnet/high по вердикту pick, 2026-10-12 10:00-11:00.
- Проверка: агентская часть сценария прогнана, вывод в «Проверке», 2026-10-12 13:00.
- Долг: жду разбора у диффа, 2026-10-12 12:00.

## Сценарий проверки

Агентская часть:

1. `python3 check_ok.py`, ждём слово ok в выводе.

Шаг человека: глазами сверить вёрстку README.

## Выкат

- 2026-10-12 слито: SHA
- 2026-10-12 smoke прогнан.

## Проверка

Прогон агентской части: `python3 check_ok.py` напечатал ok.
DOC
sed -i.bak "s/слито: SHA/слито: $sha/" docs/tasks/OB-062.md && rm -f docs/tasks/OB-062.md.bak
cat > docs/tasks/OB-063.md <<'DOC'
# OB-063: README без примера вывода команд

## Что происходит

В README команды названы, а вывода их рядом нет.

## Чего хотим

У каждой команды README стоит пример вывода.

## DoD

- у каждой команды README стоит пример вывода
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
check_anchor = ("## Check (готово, ждёт проверки пользователем)\n\n"
                "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
                "|--------|--------|-----|---|---|------|--------|\n")
row62 = ("| OB-062 | Пример вывода в README [приёмка: mixed] | task | P2 | "
        "35 (25+4+1+0+4, M+1) | M | [tasks/OB-062.md](tasks/OB-062.md) |\n")
back = ("| OB-001 | clamp не режет верхнюю границу | bug | P2 | "
        "42 (25+5+1+5+4, S+2) | S | [tasks/OB-001.md](tasks/OB-001.md) |\n")
row63 = ("| OB-063 | README без примера вывода команд [долг] | task | P3 | "
        "6 (0+2+0+0+2, S+2) | S | [tasks/OB-063.md](tasks/OB-063.md) |\n")
if check_anchor not in s or back not in s:
    raise SystemExit("якоря доски в фикстуре не нашлись")
s = s.replace(check_anchor, check_anchor + row62, 1).replace(back, back + row63, 1)
open(p, "w", encoding="utf-8").write(s)
PY
git add -A && git commit -q --no-verify -m "docs(tasks): OB-062 в Check, строка долга OB-063"
```

## Промпт

Прогон агентской части сценария проверки OB-062 уже отмечен в файле задачи,
и smoke отмечен. Вид приёмки mixed: шаг человека остаётся ему, строку из Check
не закрывай. Хвост долга этой сдачи: пройди скилл board-debt по диффу круга и
закрой отметку `taskctl debt-done OB-062 "пересечений нет"` либо "взята
<ID долга>".

## Проверка

```sh
grep -q "Долг: разобран, взята OB-063" docs/tasks/OB-062.md ||
	{ echo "отметка долга не закрыта взятием OB-063"; exit 1; }
sed -n '/^## In progress/,/^## Check/p' docs/TASKS.md | grep -q "OB-063" ||
	{ echo "строка долга OB-063 не взята проверяющим в работу"; exit 1; }
exit 0
```
