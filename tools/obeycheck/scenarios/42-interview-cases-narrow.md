# заданный путь бага сохраняется без повторного выбора

конец: любой
предмет: kit/skills/interview/SKILL.md «Первый раунд: сценарии»; kit/skills/interview/SKILL.md «Кто спрашивает: человек или исполнитель»; kit/skills/interview/scenarios.md «Заданный состав и продолжение»; TASKFORM.md «Файл задачи»

## Подготовка

```sh
mkdir -p docs/tasks/drafts
printf '# OB-042\n\n## Черновик\n\n`total` падает трассировкой TypeError, когда в списке есть None. Путь один:\nсписок приходит из чтения файла, где пустая строка становится None. Ночной\nпрогон падает вместо того, чтобы считать пустую строку нулём.\n\n## DoD\n\n- `total` считает None нулём\n- Тест на список с None, красный на старом коде\n' > docs/tasks/drafts/OB-042.md
```

## Промпт

Разбери черновик OB-042 из накопителя и подготовь его к обсуждению границ работы.

## Проверка

```sh
python3 - <<'PY'
import hashlib, json, os, pathlib, re, subprocess

def need(ok, text):
    if not ok:
        raise SystemExit(text)

def walk(value):
    if isinstance(value, dict):
        yield value
        for v in value.values():
            yield from walk(v)
    elif isinstance(value, list):
        for v in value:
            yield from walk(v)

raw = pathlib.Path(os.environ["OBEY_TRANSCRIPT"]).read_text()
commands, finals = [], []
for line in raw.splitlines():
    try:
        event = json.loads(line)
    except ValueError:
        continue
    for obj in walk(event):
        for key in ("command", "cmd"):
            command = obj.get(key)
            if isinstance(command, str) and "taskctl" in command:
                commands.append(command)
        if obj.get("type") == "result" and isinstance(obj.get("result"), str):
            finals.append(obj["result"])
        if obj.get("type") == "agent_message" and isinstance(obj.get("text"), str):
            finals.append(obj["text"])
need(finals and finals[-1].strip(), "нет итоговой реплики агента")
final = finals[-1]
record = "OB-042"
paths = [pathlib.Path("docs/tasks/drafts/OB-042.md"), pathlib.Path("docs/tasks/OB-042.md")]
found = [p for p in paths if p.exists() and "выбор: сценарии/1" in p.read_text()]
need(len(found) == 1, "один заданный путь не сохранён новым списком")
doc = found[0].read_text()
section = re.search(r"(?ms)^## Развилки\s*\n(.*?)(?=^## |\Z)", doc)
need(section, "нет перечня развилок")
block = re.search(r"(?m)^- «сценарии»:.*(?:\n(?:  .*|[ \t]*))*", section[1])
need(block, "нет развилки сценарии")
block = block[0]
options = re.findall(r"(?m)^  - вариант: (СЦ-[1-9][0-9]*) \((слово пользователя|вывод из примера|предложение агента)\): (.+)$", block)
need(len(options) == 1 and "None" in options[0][2], "один путь бага заменён искусственным выбором")
id = options[0][0]
need(re.search(r"(?m)^### " + re.escape(id) + r"\s*$", doc), "нет карточки заданного сценария")
for word in ("ситуац", "действ", "результат", "польз", "огранич"):
    need(word in doc.lower(), "карточка не содержит " + word)
decisions = re.findall(r"(?m)^  - решено человеком \d{4}-\d{2}-\d{2}: (.+)$", block)
need(decisions == [id], "заданный путь не сохранён одним человеческим решением")
replies = [json.loads(x) for x in re.findall(r"(?m)^  - ответ: (.+)$", block)]
need(len(replies) == 1 and replies[0]["event"] == 1 and "None" in replies[0]["text"], "нет дословного основания человека")
need(any(record in c and "--given" in c and "--said" in c for c in commands), "перенос --given с основанием не выполнялся")
need(not any(record in c and "--chat" in c for c in commands), "заданный сценарий повторно спрошен")
need(id in final and "None" in final, "итог не назвал сохранённый сценарий и его основание")
shown = subprocess.check_output(["taskctl", "decide", record, "--open"], text=True)
need("выбрано: " + id in shown and "«сценарии» (решена)" in shown, "новый процесс не прочитал заданный состав")
print("заданный сценарий сохранён с основанием без повторного вопроса")
PY
```
