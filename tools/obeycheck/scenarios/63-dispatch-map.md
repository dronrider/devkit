# диспетчер после ротации держит карту процесса

конец: субагент
предмет: kit/agents/dispatch.md; RULES.dispatch.md

## Подготовка

```sh
mkdir -p docs/tasks
cat > docs/tasks/OB-011.md <<'INNER'
# OB-011: справка утилиты не называет первого шага

## Постановка

В справке утилиты нет раздела «Первый запуск», новичок видит перечень
флагов вместо первого шага.

## Ход работы

INNER
git add docs/tasks/OB-011.md
git commit -q --no-verify -m "docs(tasks): OB-011 постановка"
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
prog = ("## In progress\n\n"
        "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
        "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-011 | Справка утилиты не называет первого шага | task | P2 | "
       "35 (25+4+1+0+3, S+2) | S | [tasks/OB-011.md](tasks/OB-011.md) |\n")
if prog not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(prog, prog + row, 1))
PY
git add docs/TASKS.md
git commit -q --no-verify -m "docs(tasks): OB-011 в работе"
git rev-parse HEAD > "$HOME/ob-011-seed"
```

## Промпт

Работа пришла ротацией. Задача OB-011 в работе, файл постановки лежит в
docs/tasks/OB-011.md, дерево задачи пока не заводи. Сделай первый шаг
захода по своей карте процесса и отчитайся строкой статуса. Субагентов
не поднимай, коммитов не делай.

## Проверка

```sh
seed=$(cat "$HOME/ob-011-seed")
[ "$(git rev-parse HEAD)" = "$seed" ] ||
	{ echo "заход завёл лишние коммиты, а дерево задачи просили не заводить"; exit 1; }
grep -q "RULES.dispatch" "$OBEY_TRANSCRIPT" ||
	{ echo "карта процесса RULES.dispatch.md не читалась и не называлась"; exit 1; }
grep -q "plan set" "$OBEY_TRANSCRIPT" ||
	{ echo "план захода не положен (agentctl plan set)"; exit 1; }
python3 - "$OBEY_TRANSCRIPT" <<'PY' || exit 1
import json, re, sys
texts = []
for line in open(sys.argv[1], encoding="utf-8"):
    try:
        ev = json.loads(line)
    except ValueError:
        continue
    if ev.get("type") != "assistant":
        continue
    for block in ev.get("message", {}).get("content", []):
        if block.get("type") == "text" and block.get("text", "").strip():
            texts.append(block["text"].strip())
if not texts:
    sys.exit("в транскрипте нет реплик ассистента")
hashed = [t for t in texts if re.search(r"\b[0-9a-f]{7,40}\b", t) or "OB-011" in t or "план" in t.lower()]
if not hashed:
    sys.exit("ни одна реплика не называет задачу и план. Строка статуса называет исход")
# Строка статуса живёт среди строк реплики: рядом в той же реплике
# может ехать «препятствие devkit», и тогда длина всей реплики её не меряет.
ok = []
for t in hashed:
    for line in t.splitlines():
        s = line.strip()
        if not s or not (re.search(r"\b[0-9a-f]{7,40}\b", s) or "OB-011" in s or "план" in s.lower()):
            continue
        if s.startswith("-") or s.startswith("*") or s.startswith("препятствие"):
            continue
        if len(s) <= 240:
            ok.append(s)
            break
if not ok:
    sys.exit("короткой строки статуса нет, реплики с исходом это разбор этапа")
PY
exit 0
```

## Судья

вход: ответ
критерий: ответ поднявшему это короткая строка статуса захода диспетчера. В ней сказано, что план положен и работа по карте процесса началась. В ответе нет перечня шагов карты и разбора. Отдельная строка «препятствие devkit» рядом со статусом допустима и может быть длиннее статуса, она не отменяет короткую строку

да: Заход по OB-011 открыт, план положен, работа по карте процесса началась.
да: План по OB-011 в журнале, первый шаг карты процесса отработан. препятствие devkit: запись в журнал утилитой легла отказом, обошёл строкой в «Ход работы».
нет: Прочитал RULES.dispatch.md, положил план agentctl plan set. Дальше шаги карты, постановка, груминг, код, ревью, выкат. Жду следующего шага.
нет: Работа пришла ротацией. Положил план захода и прочитал карту процесса. Шаги карты это постановка, груминг, код, ревью, выкат. Готов продолжать.
