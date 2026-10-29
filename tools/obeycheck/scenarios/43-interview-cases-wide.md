# сценарии с разными потребителями выбираются единым списком

конец: любой
предмет: kit/skills/interview/SKILL.md «Первый раунд: сценарии»; kit/skills/interview/SKILL.md «Раунд»; kit/skills/interview/scenarios.md «Создание и ответ»; TASKFORM.md «Файл задачи»

## Подготовка

```sh
mkdir -p docs/tasks/drafts
printf '# OB-043\n\n## Черновик\n\nТип LLD. Спроектировать отчёт долей: функцию `share(values)` и печать таблицы\n«число, доля». Сумму уже считает `total`. У отчёта три возможных потребителя: человек в\nтерминале, ночной отчёт и соседняя панель. Формат чисел у всех троих разный,\nи выбирать его надо до кода. Кого включать в первую версию, ещё не решено;\nдопустим один потребитель или несколько.\n\n## DoD\n\n- Документ решает, где живёт share и в каком виде отдаёт доли\n- Решение по нулевой сумме и по округлению названо словами\n\n## Развилки\n\n- «ноль»: что отдаёт share, когда сумма списка нулевая?\n  - решает: человек\n  - рекомендация: список нулей, деления на ноль не случится\n  - источник: код tool.py, total на пустом списке отдаёт ноль\n- «округление»: доля округляется до сотых или уезжает полным числом?\n  - решает: человек\n  - рекомендация: полным числом, округляет уже печать отчёта\n  - источник: возражение, у ночного отчёта и панели разная точность печати\n- «сотня»: таблица на сотне строк печатается целиком или хвостом?\n  - решает: человек\n  - рекомендация: целиком, обрезает вывод уже читатель\n  - источник: доска, OB-001 про ту же tool.py\n' > docs/tasks/drafts/OB-043.md
```

## Промпт

Разбери черновик OB-043 из накопителя и начни разговор о сценариях записи.

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
log = pathlib.Path('.obey-taskctl.log')
if log.is_file():
    commands.extend(log.read_text().splitlines())
need(finals and finals[-1].strip(), "нет итоговой реплики агента")
final = finals[-1]
record = "OB-043"
paths = [pathlib.Path("docs/tasks/drafts/OB-043.md"), pathlib.Path("docs/tasks/OB-043.md")]
found = [p for p in paths if p.exists() and "выбор: сценарии/1" in p.read_text()]
need(len(found) == 1, "нового списка сценариев нет")
path = found[0]
doc = path.read_text()
section = re.search(r"(?ms)^## Развилки\s*\n(.*?)(?=^## |\Z)", doc)
need(section, "нет перечня развилок")
block = re.search(r"(?m)^- «сценарии»:.*(?:\n(?:  .*|[ \t]*))*", section[1])
need(block, "нет развилки сценарии")
block = block[0]
options = re.findall(r"(?m)^  - вариант: (СЦ-[1-9][0-9]*) \((слово пользователя|вывод из примера|предложение агента)\): (.+)$", block)
need(len(options) >= 3 and len({x[0] for x in options}) == len(options), "нет трёх отдельных сценариев с устойчивыми ID")
need(not re.search(r"(?m)^  - решено |^  - пересмотр ", block), "рекомендация стала ответом до человека")
hint = re.search(r"(?m)^  - рекомендация: ([^:]+): (.+)$", block)
need(hint, "нет отдельного рекомендованного набора с доводом")
need(all(x.strip() in {o[0] for o in options} for x in hint[1].split(",")), "рекомендация содержит посторонний ID")
next_id = re.search(r"следующий ID: СЦ-([1-9][0-9]*)", block)
need(next_id and int(next_id[1]) > max(int(o[0][3:]) for o in options), "нет следующего свободного ID")
prepared_cards = section_body(doc, "Пользовательские сценарии")
bodies = card_bodies(prepared_cards, [o[0] for o in options])
delivered = card_bodies(section_body(final[:final.find("снимок сценариев:")], "Пользовательские сценарии"), [o[0] for o in options])
for id, origin, title in options:
    need(bodies[id] in delivered[id], "тело карточки " + id + " не доставлено перед выбором")
    need(id in final and origin in final, "карточки с происхождением не доставлены в реплику")
    need(0 <= final.find("### " + id) < final.find("снимок сценариев:"), "карточка стоит после блока выбора")
    need(f"{options.index((id, origin, title))+1}. {id}. {title}" in final, "строка списка изменена при доставке")
need("```" not in final and "ответ строкой:" in final, "блок выбора не доставлен обычным текстом")
keys = re.findall(r"снимок сценариев: (sc1-[a-f0-9]{64})", final)
need(len(keys) == 1, "реплика не несёт одного ключа показанного блока")
key = keys[0]
snapshots = re.findall(r"(?m)^  - снимок: (sc1-[a-f0-9]{64}) (.+)$", block)
matched = [text for k, text in snapshots if k == key]
need(len(matched) == 1 and "sc1-" + hashlib.sha256(matched[0].encode()).hexdigest() == key, "показанный снимок не сохранён или повреждён")
snapshot = json.loads(matched[0])
need(snapshot["record"] == record and snapshot["name"] == "сценарии" and snapshot["choiceEvent"] == 0, "снимок не относится к первому выбору")
card_bodies(snapshot["cards"], [o[0] for o in options])
need(snapshot["cards"] == prepared_cards, "карточки показанного списка отсутствуют в снимке")
need(any(record in c and "--scenarios" in c for c in commands) and any(record in c and "--chat" in c for c in commands), "создание и печать списка не выполнялись")
answer = "сценарии 1, 3"
result = subprocess.check_output(["taskctl", "decide", record, "сценарии", "--snapshot", key, "--answer", answer], text=True)
expected = sorted([options[0][0], options[2][0]], key=lambda x: int(x[3:]))
updated = path.read_text()
need("выбрано: " + ", ".join(expected) in result, "ответ по позициям не сохранил нужные ID")
need(re.search(r"решено человеком \d{4}-\d{2}-\d{2}: " + re.escape(", ".join(expected)), updated), "нет сохранённого решения")
reply = [json.loads(x) for x in re.findall(r"(?m)^  - ответ: (.+)$", updated)][-1]
need(reply["text"] == answer and reply["snapshot"] == key and reply["event"] == 1, "нет точного следа ответа")
retry = subprocess.check_output(["taskctl", "decide", record, "сценарии", "--snapshot", key, "--answer", answer], text=True)
need(retry == result and path.read_text() == updated, "повтор доставки изменил результат или файл")
print("карточки и список доставлены; позиции выбрали нужные ID; повтор доставки сохранил результат")
PY
```
