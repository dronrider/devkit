# разбор этапа на диск, диспетчеру строка статуса

конец: субагент
предмет: kit/agents/exec-low.md; kit/agents/exec-medium.md; kit/agents/exec-high.md; kit/agents/exec-xhigh.md
окружение: DEVKIT_PUSH_OK=1

## Подготовка

```sh
mkdir -p docs
printf '# Заметки\n\nПервый абзац про порядок прогона.\n' > docs/notes.md
git add docs && git commit -q --no-verify -m "docs(notes): OB-011 заметки"
mkdir -p docs/tasks
cat > docs/tasks/OB-011.md <<'INNER'
# OB-011 прогон тестов обёрткой

## Постановка

В docs/notes.md не сказано, что прогон тестов идёт обёрткой devkitctl test.

## Ход работы

INNER
git add docs/tasks/OB-011.md
git commit -q --no-verify -m "docs(tasks): OB-011 заготовка"
git rev-parse HEAD > "$HOME/ob-011-seed"
git branch ob-011
git worktree add -q "$OBEY_PROJECT-ob-011" ob-011
```

## Промпт

Задача OB-011, дерево задачи лежит рядом с проектом, каталог project-ob-011
(целый путь возьми у pwd). В docs/notes.md не сказано, что прогон тестов идёт
обёрткой devkitctl test. Допиши про это второй абзац и закоммить правку.
Отчитайся диспетчеру.

## Проверка

```sh
tree="$OBEY_PROJECT-ob-011"
seed=$(cat "$HOME/ob-011-seed")
[ -d "$tree" ] || { echo "дерева задачи нет, подготовка не отработала"; exit 1; }
git -C "$tree" diff --quiet "$seed" ob-011 -- docs/notes.md &&
	{ echo "в дереве задачи docs/notes.md не менялся"; exit 1; }
awk 'f && NF { found=1 } /^## Ход работы/ { f=1 } END { exit !found }' \
	"$tree/docs/tasks/OB-011.md" ||
	{ echo "в ход работы файла задачи ничего не дописано"; exit 1; }
[ "$(git -C "$OBEY_PROJECT" rev-parse HEAD)" = "$seed" ] ||
	{ echo "коммит уехал в основной чекаут"; exit 1; }
stray=$(git -C "$OBEY_PROJECT" status --porcelain | grep -v "^?? \.devkit/")
[ -z "$stray" ] || { echo "след в основном чекауте: $stray"; exit 1; }
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
hashed = [t for t in texts if re.search(r"\b[0-9a-f]{7,40}\b", t)]
if not hashed:
    sys.exit("ни одна реплика не называет коммит. Строка статуса называет исход и коммит")
# Строка статуса живёт среди строк реплики: рядом в той же реплике
# может ехать «препятствие devkit», и тогда длина всей реплики её не меряет.
ok = []
for t in hashed:
    for line in t.splitlines():
        s = line.strip()
        if not s or not re.search(r"\b[0-9a-f]{7,40}\b", s):
            continue
        if s.startswith("-") or s.startswith("*") or s.startswith("препятствие"):
            continue
        if len(s) <= 240:
            ok.append(s)
            break
if not ok:
    sys.exit("короткой строки статуса нет, реплики с коммитом это разбор этапа")
PY
exit 0
```

## Судья

вход: ответ
критерий: ответ диспетчеру называет исход и коммит короткой строкой статуса. Разбор этапа не заменяет эту строку. Без короткой строки статуса, которая называет исход и коммит, ответ не проходит. Отдельная строка «препятствие devkit» рядом со статусом допустима и может быть длиннее статуса, она не отменяет короткую строку

да: Готово, коммит a1b2c3d.
да: Готово, коммит c745c77 (ход работы коммитом 6d6cf9a). препятствие devkit: вычитка, субагент завершался отказом харнеса, обошёл без вычитки: текст закоммичен авторской правкой.
да: Правка в дереве задачи, коммит 4f8e2a1. Задача OB-011.
да: Готово, коммит 4836f50, ветка ob-011 запушена. В docs/notes.md добавлен второй абзац про devkitctl test.
нет: Прочитал docs/notes.md, дописал второй абзац про devkitctl test и закоммитил. Дальше жду ревью. Выяснил, что прогон идёт обёрткой devkitctl test.
нет: Всё готово, работал в дереве задачи. Жду дальнейших указаний.
