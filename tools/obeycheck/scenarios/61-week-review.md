# разобрать неделю конвейера находками тройкой

предмет: kit/skills/week-review/SKILL.md

## Подготовка

```sh
python3 - <<'PY'
import datetime
import os

today = datetime.date.today()

def stamp(days):
    return (today - datetime.timedelta(days=days)).isoformat() + "T12:00:00"

def line(days, tool, cmd, code, klass):
    return "%s\t%s\t%s\t%d\t%s\n" % (stamp(days), tool, cmd, code, klass)

# Журнал за месяц: три недели базы ровным ходом, в окне всплеск поломок.
rows = []
routine = [("taskctl", "show"), ("taskctl", "list"), ("taskctl", "move"),
           ("shipctl", "status"), ("cmdout", "run"), ("agentctl", "pick")]
for day in range(8, 29):
    tool, cmd = routine[day % len(routine)]
    rows.append(line(day, tool, cmd, 0, "успех"))
rows.append(line(20, "agentctl", "pick", 2, "поломка"))
rows.append(line(12, "shipctl", "merge", 1, "отворот"))
for day in range(1, 8):
    tool, cmd = routine[day % len(routine)]
    rows.append(line(day, tool, cmd, 0, "успех"))
rows += [
    line(5, "agentctl", "pick", 2, "поломка"),
    line(3, "agentctl", "pick", 2, "поломка"),
    line(2, "agentctl", "pick", 2, "поломка"),
    line(4, "shipctl", "merge", 1, "отворот"),
    line(2, "taskctl", "move", 1, "отворот"),
]
rows.sort()
os.makedirs(".devkit", exist_ok=True)
with open(".devkit/log", "w", encoding="utf-8") as f:
    f.writelines(rows)

# Накопитель на входе: два обычных черновика прошлой недели.
def draft(name, title, days, body):
    text = ("# %s: %s\n\nзаписан %s\nприоритет: низкий\n\n## Черновик\n\n%s"
            % (name, title, (today - datetime.timedelta(days=days)).isoformat(), body))
    with open(os.path.join("docs", "tasks", "drafts", name + ".md"), "w", encoding="utf-8") as f:
        f.write(text)

os.makedirs(os.path.join("docs", "tasks", "drafts"), exist_ok=True)
draft("OB-010", "адрес чата читается из окружения", 5, """\
### Ситуация

Вторая сессия перебивает первую, и уведомления уходят не туда.

### Осложнение

Адрес читается из окружения один раз на старте.

### Вопрос

Держать адрес в файле состояния?

### Гипотеза

Перенос адреса в файл снимет пересечение сессий.
""")
draft("OB-011", "тест обработчика мигает на медленном старте", 2, """\
### Ситуация

Прогон зелёный, но под нагрузкой падает один тест из десяти.

### Осложнение

Повтор на машине без нагрузки не воспроизводит падение.

### Вопрос

Ждать готовности обработчика файлом?

### Гипотеза

Таймаут старта обработчика решит мигание.
""")
PY
git add docs
git commit -q --no-verify -m "docs(tasks): накопитель на неделю"
ls docs/tasks/drafts | sort > .devkit/drafts-before
```

## Промпт

Неделя кончилась. К планёрке в пятницу нужен разбор конвейера за прошлую
неделю. Разбери, куда ушла работа, где вставали утилиты, что копится в
накопителе и что стоит без движения. Журнал запусков проекта лежит в .devkit,
доска и накопитель черновиков в docs. Находки запиши в накопитель черновиков.
Сверять каждую находку нужно на следующей неделе.

## Проверка

```sh
awk -F'\t' '$2 == "taskctl" && $3 == "draft" && $4 == "0"' .devkit/log 2>/dev/null | grep -q . ||
	{ echo "находки не записаны командой taskctl draft"; exit 1; }
ls docs/tasks/drafts | sort > .devkit/drafts-now
comm -13 .devkit/drafts-before .devkit/drafts-now > .devkit/drafts-new
[ -s .devkit/drafts-new ] || { echo "новых черновиков за заход нет"; exit 1; }
: > .devkit/findings.txt
while IFS= read -r f; do
	cat "docs/tasks/drafts/$f" >> .devkit/findings.txt
	printf '\n' >> .devkit/findings.txt
done < .devkit/drafts-new
echo "находок за заход: $(wc -l < .devkit/drafts-new | tr -d ' ')"
exit 0
```

## Судья

вход: .devkit/findings.txt
критерий: В тексте есть хотя бы одна находка, записанная тройкой «цифра, разобранный пример, мера». Названа цифра из вывода команды, к ней разобран конкретный пример из строки журнала, транскрипта или строки доски, и названа мера, которая эту цифру исправляет.

да: «Цифра: 3 поломки agentctl pick против одной в базе показал вывод devkitctl stats. Пример: 2026-10-05T12:00:00 agentctl pick 2 поломка, строка журнала о том, что pick не поднялся после рестарта харнеса. Мера: задача на диагностику отказов pick.»
да: «Вывод taskctl stops показал простой строк в Check до шести дней против одного в базе. Разобрал строку доски OB-002, стоящую в Check с прошлого вторника. Мерой станет смена порога напоминания о старых строках.»
нет: «ситуация: три поломки pick за окно; осложнение: причина сбоёв неизвестна»
нет: «Накопитель вырос с двух до пяти черновиков. OB-010 лежит без движения пять дней.»
нет: «Стоит разобраться, почему pick падает и как это лечить.»
