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

def visible_lines(text):
    fence = ""
    for i, line in enumerate(text.splitlines()):
        stripped = line.lstrip(" \t")
        match = re.match(r"(`{3,}|~{3,})", stripped)
        token = match[0] if match else ""
        if fence:
            if token and token[0] == fence[0] and len(token) >= len(fence) and not stripped[len(token):].strip():
                fence = ""
            continue
        if token:
            fence = token
            continue
        yield i, line

def section_body(text, title):
    lines = text.splitlines()
    start = None
    for i, line in visible_lines(text):
        if line == "## " + title:
            need(start is None, "повтор раздела " + title)
            start = i + 1
        elif start is not None and line.startswith("## "):
            return "\n".join(lines[start:i]).strip()
    return "\n".join(lines[start:]).strip() if start is not None else ""

def card_bodies(cards, ids):
    lines, bodies = cards.splitlines(), {}
    current, start = None, 0
    for i, line in visible_lines(cards):
        if not re.match(r"^#{1,6}\s+", line):
            continue
        if current is not None:
            bodies[current] = "\n".join(lines[start:i]).strip()
        current = None
        head = re.fullmatch(r"### (СЦ-[1-9][0-9]*)\s*", line)
        if head:
            current, start = head[1], i + 1
            need(current not in bodies, "повтор карточки " + current)
    if current is not None:
        bodies[current] = "\n".join(lines[start:]).strip()
    for id in ids:
        need(id in bodies, "нет карточки " + id)
        fields = {}
        for _, line in visible_lines(bodies[id]):
            label, sep, value = line.strip().partition(":")
            if sep and value.strip():
                fields[label.casefold()] = value.strip()
        for label in ("ситуация", "действие", "результат", "польза", "рекомендация", "ограничение", "источник"):
            need(label in fields, "карточка " + id + " не содержит непустого поля " + label)
    return bodies

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
def recorded_commands(path):
    starts, success = [], set()
    for line in path.read_text().splitlines():
        parts = line.split("\t", 2)
        if len(parts) != 3:
            continue
        kind, ident, value = parts
        if kind == "start":
            starts.append((ident, value))
        elif kind == "end" and value == "0":
            success.add(ident)
    return [command for ident, command in starts if ident in success]

log = pathlib.Path('.obey-taskctl.log')
if log.is_file():
    commands.extend(recorded_commands(log))
need(finals and finals[-1].strip(), "нет итоговой реплики агента")
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
prepared_cards = section_body(doc, "Пользовательские сценарии")
card_bodies(prepared_cards, [id])
decisions = re.findall(r"(?m)^  - решено человеком \d{4}-\d{2}-\d{2}: (.+)$", block)
need(decisions == [id], "заданный путь не сохранён одним человеческим решением")
replies = [json.loads(x) for x in re.findall(r"(?m)^  - ответ: (.+)$", block)]
need(len(replies) == 1 and replies[0]["event"] == 1 and "None" in replies[0]["text"], "нет дословного основания человека")
key = replies[0]["snapshot"]
matched = [text for k, text in re.findall(r"(?m)^  - снимок: (sc1-[a-f0-9]{64}) (.+)$", block) if k == key]
need(len(matched) == 1 and "sc1-" + hashlib.sha256(matched[0].encode()).hexdigest() == key, "снимок заданного состава повреждён")
snapshot = json.loads(matched[0])
need(snapshot["record"] == record and snapshot["name"] == "сценарии" and snapshot["choiceEvent"] == 0, "снимок не относится к переносу")
need(snapshot["scenarios"] == [dict(zip(("id", "origin", "title"), option)) for option in options], "состав изменён после переноса")
card_bodies(snapshot["cards"], [id])
need(snapshot["cards"] == prepared_cards, "карточка подготовлена после снимка переноса")
need(any(record in c and "--given" in c and "--said" in c for c in commands), "перенос --given с основанием не выполнялся")
shown = subprocess.check_output(["taskctl", "decide", record, "--open"], text=True)
need("выбрано: " + id in shown and "«сценарии» (решена)" in shown, "новый процесс не прочитал заданный состав")
print("заданный сценарий сохранён с основанием без повторного вопроса")
PY
```
