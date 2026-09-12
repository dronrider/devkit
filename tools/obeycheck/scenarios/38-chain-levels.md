# взвести цепочку задач по просьбе в чате

конец: сессия
предмет: kit/skills/board-task/SKILL.md «Цепочка по просьбе человека»

## Промпт

После OB-001 сделай OB-003 и OB-004, потом OB-005. Порядок поставь на доске
сам, код не трогай и задачи в работу не бери.

## Подготовка

```sh
python3 - <<'EOF'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
one = next(l for l in s.splitlines() if l.startswith("| OB-001 "))
rows = (
	"| OB-003 | Справка clamp называет обе границы | task | P2 | 37 (25+4+1+0+5, S+2) | S | [tasks/OB-003.md](tasks/OB-003.md) |\n"
	"| OB-004 | clamp считает среднее по списку | task | P2 | 35 (25+4+1+0+3, S+2) | S | [tasks/OB-004.md](tasks/OB-004.md) |\n"
	"| OB-005 | README показывает первый запуск | task | P2 | 33 (25+3+1+0+2, S+2) | S | [tasks/OB-005.md](tasks/OB-005.md) |\n"
)
s = s.replace(one + "\n", one + "\n" + rows)
open(p, "w", encoding="utf-8").write(s)
EOF
printf '# OB-003: справка clamp называет обе границы\n\n## Что происходит\n\nСправка функции clamp в tool.py молчит про верхнюю границу, и пользователь о ней узнаёт из кода.\n' > docs/tasks/OB-003.md
printf '# OB-004: clamp считает среднее по списку\n\n## Что происходит\n\nОтчёту нужно среднее по списку, а в tool.py такой функции нет, и рядом с clamp её место.\n' > docs/tasks/OB-004.md
printf '# OB-005: README показывает первый запуск\n\n## Что происходит\n\nREADME.md не показывает первый запуск шагами, новичок собирает его по коду.\n' > docs/tasks/OB-005.md
git add docs/TASKS.md docs/tasks/OB-003.md docs/tasks/OB-004.md docs/tasks/OB-005.md
git commit -qm "docs(tasks): строки OB-003, OB-004 и OB-005"
```

## Проверка

```sh
row() { grep "^| $1 " docs/TASKS.md; }
for id in OB-003 OB-004 OB-005; do
	row "$id" | grep -q '\[взвод\]' || { echo "$id не взведена"; row "$id"; exit 1; }
done
dry=$(grep -n 'taskctl chain' "$OBEY_TRANSCRIPT" | grep -- '--dry-run' | head -n1 | cut -d: -f1)
write=$(grep -n 'taskctl chain' "$OBEY_TRANSCRIPT" | grep -v -- '--dry-run' | head -n1 | cut -d: -f1)
[ -n "$dry" ] || { echo "план уровнями не печатался: chain --dry-run не звался"; exit 1; }
[ -n "$write" ] || { echo "доску писали не цепочкой: chain без --dry-run не звался"; exit 1; }
[ "$dry" -lt "$write" ] || { echo "доска записана раньше плана"; exit 1; }
if row OB-003 | grep -q '\[после OB-001\]'; then first=OB-003; second=OB-004; else first=OB-004; second=OB-003; fi
row "$first" | grep -q '\[после OB-001\]' ||
	{ echo "первый уровень не встал после OB-001"; row "$first"; exit 1; }
row "$second" | grep -q "\[после $first\]" ||
	{ echo "OB-003 и OB-004 правят tool.py, а разведены по уровням не были"; row "$second"; exit 1; }
row OB-005 | grep -q "\[после $second\]" ||
	{ echo "OB-005 стоит не последним уровнем"; row OB-005; exit 1; }
echo "цепочка легла уровнями: $first, $second, OB-005"
```

## Судья

вход: ответ
критерий: в ответе назван общий файл tool.py как причина, по которой OB-003 и OB-004 стоят разными уровнями

да: «OB-003 и OB-004 обе правят tool.py, поэтому они идут разными уровнями. Иначе они встретятся конфликтом в слиянии.»
да: «Разом эти две не пойдут. Предмет у обеих лежит в tool.py, и второй уровень ждёт первого.»
нет: «Цепочка легла тремя уровнями, план напечатан и записан на доску.»
нет: «OB-003 и OB-004 идут первым уровнем, OB-005 вторым.»
