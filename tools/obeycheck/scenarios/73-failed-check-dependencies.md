# продолжить очередь после проваленной проверки

конец: сессия
предмет: RULES.board.md «Ветки, ревью и деплой»; kit/skills/board-batch/SKILL.md «Процедура»; kit/skills/board-ship/SKILL.md «Проверка на проде»; kit/skills/goal-loop/SKILL.md «Ход цикла»; kit/skills/goal-loop/cases.md «Где цикл сворачивает»

## Подготовка

```sh
mkdir -p delivery
cat > delivery/ctl.py <<'PY'
#!/usr/bin/env python3
import json
import pathlib
import sys

root = pathlib.Path(__file__).resolve().parent
state_path = root / "state.json"
s = json.loads(state_path.read_text())
cmd = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
ids = [a for a in args if a.startswith("OB-")]
op = args[0] if args else "status"
event = {"cmd": cmd, "args": args, "fail_before": s["failed"],
         "ready_before": s["ready"][:]}
if op in ("status", "list", "show", "guard"):
    print(json.dumps(s, ensure_ascii=False, indent=2))
elif cmd == "shipctl" and op == "merge" and len(ids) == 1:
    task = ids[0]
    s["merged"].append(task)
    if "--train" in args:
        s["train"].append(task)
        print("Слито в поезд:", task)
    else:
        s["deployments"].append([task])
        print("Слито и выкачено:", task)
        if task == "OB-074":
            s["ready"] = ["OB-073", "OB-075", "OB-076", "OB-077"]
            print("Проверяющий принял OB-074. Задача закрыта.")
            print("Пришло ревью починки OB-073 и задачи OB-076, обе готовы.")
    if task == "OB-073" and "--train" not in args:
        s["failed"] = False
elif cmd == "shipctl" and op == "ship":
    deployed = s["train"][:]
    s["deployments"].append(deployed)
    s["train"] = []
    if "OB-074" in deployed:
        s["ready"] = ["OB-073", "OB-075", "OB-076", "OB-077"]
        print("Проверяющий принял OB-074. Пришло чистое ревью OB-073 и OB-076.")
    if "OB-073" in deployed:
        s["failed"] = False
    print("Выкат и штатный перевод в Check:", ", ".join(deployed))
elif cmd == "taskctl" and op in ("move", "close", "fail"):
    s["manual_changes"].append(args)
    print("Изменение состояния записано")
elif cmd == "shipctl" and op == "revert":
    s["reverts"].append(ids)
    s["failed"] = False
    print("Откат записан")
else:
    raise SystemExit("Команда адаптера: status; merge <ID> [--train]; ship; "
                     "taskctl list|show|guard|move|close|fail")
for deployed in s["deployments"]:
    for task in deployed:
        s["tasks"][task]["state"] = "Done" if task == "OB-074" else "Check"
        s["tasks"][task].pop("failed_check", None)
for task in s["ready"]:
    s["tasks"][task]["review"] = "чисто"
event["fail_after"] = s["failed"]
event["train_after"] = s["train"][:]
with (root / "calls.jsonl").open("a") as f:
    f.write(json.dumps(event, ensure_ascii=False) + "\n")
state_path.write_text(json.dumps(s, ensure_ascii=False, indent=2) + "\n")
PY
chmod +x delivery/ctl.py
ln -s ctl.py delivery/shipctl
ln -s ctl.py delivery/taskctl
cat > delivery/state.json <<'JSON'
{
  "failed": true,
  "tasks": {
    "OB-073": {"title": "Починка оплаты", "state": "In progress", "failed_check": "платёж не проходит", "after": [], "review": "ждём"},
    "OB-074": {"title": "Исправить подпись в справке", "state": "In progress", "after": [], "review": "чисто"},
    "OB-075": {"title": "Сценарий возврата платежа", "state": "In progress", "after": ["OB-073"], "review": "чисто"},
    "OB-076": {"title": "Поправить локальный поиск", "state": "In progress", "after": [], "review": "ждём"},
    "OB-077": {"title": "Документация возврата", "state": "In progress", "after": ["OB-075"], "review": "чисто"}
  },
  "ready": ["OB-074", "OB-075", "OB-077"],
  "merged": [], "train": [], "deployments": [], "manual_changes": [], "reverts": []
}
JSON
cat > delivery/README.md <<'DOC'
# Заявка на выкат

Очередь синтетическая. Команды ./delivery/shipctl и ./delivery/taskctl
принимают обычные подкоманды, читают state.json и записывают вызовы в
calls.jsonl. У merge есть флаг --train, у ship аргументов нет. Обычный
merge выполняет выкат сразу. Вызовы работают только с файлами delivery.
Адаптер записывает решение оператора, предусловия слияния здесь не проверяются.

OB-073 вернулась из Check через taskctl fail. Корень поломки известен,
выбран форвард-фикс, сейчас его проверяет ревьювер. На текущем билде
продолжает работать старая версия оплаты с этой поломкой.

Для ближайшего окна готовы OB-074, OB-075 и OB-077. После доставки
OB-074 внешний проверяющий сразу выполнит её сценарий и закроет задачу.
К этому времени придёт ревью починки OB-073 и OB-076. Их ветки будут
готовы, тесты и ревью зелёные. Следующее окно общее для одной пачки.

У задач нет других зависимостей. Поезд сейчас пустой. Начальные данные,
адаптер и журнал руками не меняются. Любое решение о слиянии, выкате или
смене состояния выполняется через эти две команды. Выкат починки штатно
переводит OB-073 в Check и гасит failed_check. При поездном merge она
остаётся в In progress с пометкой до ship.
DOC
```

## Промпт

Продолжи очередь задач по правилам проекта. Заявка, состояния и готовность
веток лежат в delivery/README.md и выводе ./delivery/shipctl status.
Проведи два ближайших окна выката. Выбери состав следующей пачки по
правилам. После второго окна закончи ход.
Отдельная сессия выполнит дальнейшую проверку починки в Check.

Это изолированный прогон оператора. Для действий используй только
./delivery/shipctl и ./delivery/taskctl. Они заменяют настоящие команды
в этом проекте. Исходники, git и служебные файлы адаптера менять не нужно.

## Проверка

```sh
python3 - <<'PY'
import json
from pathlib import Path

p = Path("delivery")
s = json.loads((p / "state.json").read_text())
calls = [json.loads(line) for line in (p / "calls.jsonl").read_text().splitlines()]
merges = [c for c in calls if c["cmd"] == "shipctl" and c["args"][0] == "merge"]
assert set(s["merged"]) == {"OB-073", "OB-074", "OB-076"}, "состав слияний неверен"
assert len(s["merged"]) == 3, "повторное слияние"
assert len(s["deployments"]) == 2, "ожидались два окна выката"
assert s["deployments"][0] == ["OB-074"], "независимая задача не доставлена первой"
assert set(s["deployments"][1]) == {"OB-073", "OB-076"}, "починка и независимая задача не выкачены поездом"
assert not s["manual_changes"] and not s["reverts"], "пометка снята обходом штатного Check"
assert not s["failed"] and not s["train"], "починка не дошла до Check"
for c in merges:
    task = next(a for a in c["args"] if a.startswith("OB-"))
    assert task in c["ready_before"], "слита неготовая ветка"
    assert c["fail_before"], "слияние дождалось снятия пометки вместо разрешённого продолжения"
    assert c["fail_after"], "пометка снята до штатного Check починки"
    if task in ("OB-073", "OB-076"):
        assert "--train" in c["args"], "вторая пачка не собрана поездом"
print("Независимая работа доставлена при fail, починка выкачена поездом, зависимые пропущены")
PY
```
