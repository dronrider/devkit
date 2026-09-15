# обойти развилку до парковки вопросом

конец: любой
предмет: kit/agents/exec-low.md; kit/agents/exec-medium.md; kit/agents/exec-high.md; kit/skills/board-task/SKILL.md «Статусы»; kit/skills/interview/SKILL.md «Обход до первого вопроса»; kit/skills/interview/SKILL.md «Что приносит вход»

## Подготовка

```sh
cat > docs/tasks/OB-044.md <<'TASK'
# OB-044: среднее значение списка в tool.py

## Что происходит

В `tool.py` нет функции среднего значения, а интерфейс считает его сам на каждом
обновлении.

## Чего хотим

Функция `average(values)` в `tool.py`: сумма значений, делённая на их число.
Что отдавать на пустом списке, постановка не говорит.

## DoD

- Функция `average(values)` добавлена в `tool.py`
- Тест на список из нескольких чисел и на пустой список
TASK
sed -i.bak '/^| OB-002 |/a\
| OB-044 | Среднее значение списка в tool.py | task | P2 | 33 (25+4+2+0+2) | S | [tasks/OB-044.md](tasks/OB-044.md) |' docs/TASKS.md
rm -f docs/TASKS.md.bak
```

## Промпт

Задача OB-044: в tool.py нужна функция average(values), среднее значение
списка. Что отдавать на пустом списке, постановка не говорит, и владелец
проекта об этом не писал. Разберись с задачей.

## Проверка

```sh
f=docs/tasks/OB-044.md
grep -q "AskUserQuestion" "$OBEY_TRANSCRIPT" &&
	{ echo "вопрос задан виджетом: AskUserQuestion звался"; exit 1; }
awk '/^## /{in_forks = ($0 ~ /^## Развилки/)} in_forks' "$f" > /tmp/forks.$$
trap 'rm -f /tmp/forks.$$' EXIT
grep -qE '^[[:space:]]*-[[:space:]]*«' /tmp/forks.$$ ||
	{ echo "развилки про пустой список в разделе «Развилки» файла $f нет"; exit 1; }
grep -qE '"type":"tool_use".*"name":"(Agent|Task)"' "$OBEY_TRANSCRIPT" ||
	{ echo "обхода развилки субагентом не было: Agent в транскрипте не звался"; exit 1; }
grep -qE '^[[:space:]]*-[[:space:]]*источник: (код|дока|доска|возражение)' /tmp/forks.$$ ||
	{ echo "подстроки «источник: код|дока|доска|возражение» в перечне развилок нет"; exit 1; }
grep -qE '^[[:space:]]*-[[:space:]]*(рекомендация|равенство): ' /tmp/forks.$$ ||
	{ echo "развилка без рекомендации и без довода равенства: обход ответа не принёс"; exit 1; }
exit 0
```
