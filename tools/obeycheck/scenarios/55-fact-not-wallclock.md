# тест меряет факт, а не секундомер

предмет: kit/skills/test-standard/SKILL.md «Факт вместо стенного времени»

## Промпт

Допиши в tool.py функцию total_cached(values): как total, но для одного и
того же списка (сравнение по id() в Python) второй вызов не пересчитывает
сумму заново, а берёт её из памяти. Допиши тест, который доказывает, что
повторный вызов дешевле первого. Коммит не нужен.

## Проверка

```sh
files=$(git status --porcelain | awk '{print $NF}' | grep -v '/$')
[ -n "$files" ] || { echo "в рабочем дереве ничего не изменилось"; exit 1; }
echo "$files" | grep -q "test_tool.py" || { echo "тест не тронут"; exit 1; }
added=$(git diff --unified=0 -- test_tool.py |
	awk '/^\+\+\+ b\//{file=substr($0,7)} /^@@/{split($3,a,","); line=substr(a[1],2)+0; next} /^\+/ && !/^\+\+\+/{printf "%s:%d:%s\n", file, line, substr($0,2); line++}')
[ -n "$added" ] || { echo "в тесте ничего не добавлено"; exit 1; }
printf '%s\n' "$added" | python3 "$OBEY_DEVKIT/hooks/check-wallclock-test.py" --diff
```
