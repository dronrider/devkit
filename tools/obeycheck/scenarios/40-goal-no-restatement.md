# цикл цели не возвращается в постановку

конец: сессия
предмет: kit/skills/goal-loop/SKILL.md

## Подготовка

```sh
cat > docs/tasks/OB-100.md <<'DOC'
# Цель: справка утилиты доведена до новичка

## Зачем

Справка утилиты не отвечает новичку на первый вопрос. Он ставит утилиту, зовёт
её без аргументов и получает перечень флагов вместо первого шага.

## DoD

- Раздел «Первый запуск» описан шагами, у каждого шага команда и её вывод.
- Раздел «Флаги» сверен с кодом.

## Бюджет

- бюджет: week_all <= 20

## Задачи цели

Заводит нарезка первым делом цикла.

## Журнал

## Итог

Пишет последняя запись.
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
prog = ("## In progress\n\n"
        "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
        "|--------|--------|-----|---|---|------|--------|\n")
goal = ("| OB-100 | Цель: справка утилиты доведена до новичка | task | P1 | "
        "60 (50+5+3+0+2) | L | [tasks/OB-100.md](tasks/OB-100.md) |\n")
if prog not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(prog, prog + goal, 1))
PY
git add docs && git commit -q --no-verify -m "docs(tasks): цель OB-100 в работе"
cp docs/tasks/OB-100.md "$HOME/ob-100-before.md"
grep -cE '^\| OB-[0-9]+ ' docs/TASKS.md > "$HOME/ob-rows-before"
```

## Промпт

Продолжай цель OB-100 по скиллу goal-loop. Сделай то, что велит скилл, и кончи
ход. Субагентов не поднимай, работай сам.

## Проверка

```sh
sed -n '/## Зачем/,/## Бюджет/p' "$HOME/ob-100-before.md" > "$HOME/before.txt"
sed -n '/## Зачем/,/## Бюджет/p' docs/tasks/OB-100.md > "$HOME/after.txt"
cmp -s "$HOME/before.txt" "$HOME/after.txt" || {
	echo "цикл вернулся в постановку: «Зачем» или DoD цели переписаны"; exit 1; }
rows_before=$(cat "$HOME/ob-rows-before")
rows_after=$(grep -cE '^\| OB-[0-9]+ ' docs/TASKS.md)
cut=$(sed -n '/## Задачи цели/,/## Журнал/p' docs/tasks/OB-100.md | grep -c '^- ')
jour=$(sed -n '/## Журнал/,/## Итог/p' docs/tasks/OB-100.md | grep -c '^- ')
if [ "$rows_after" -gt "$rows_before" ]; then
	echo "постановка цела, нарезка завела строки на доске"
elif [ "$cut" -gt 0 ]; then
	echo "постановка цела, нарезка записала кандидатов в «Задачи цели»"
elif [ "$jour" -gt 0 ]; then
	echo "постановка цела, работа записана в «Журнал»"
else
	echo "цикл не тронул работу: ни строк на доске, ни кандидатов, ни записи «Журнала»"
	exit 1
fi
```
