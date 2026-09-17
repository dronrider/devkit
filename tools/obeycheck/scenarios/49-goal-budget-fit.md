# постановка цели сверяет бюджет с остатком бакета

конец: сессия
предмет: kit/skills/goal-start/SKILL.md «Порядок»; kit/skills/goal-start/SKILL.md «Бюджет и потолок яруса»

## Подготовка

```sh
mkdir -p "$HOME/.devkit/quota" "$HOME/projects"
ln -sfn "$OBEY_DEVKIT" "$HOME/projects/devkit"
taken=$(date +%Y-%m-%dT%H:%M)
reset=$(date -v+3d +%Y-%m-%dT%H:%M 2>/dev/null || date -d '+3 days' +%Y-%m-%dT%H:%M)
printf 'taken = %s\nweek_all = 51%% сброс %s\nweek_max = 12%% сброс %s\n' "$taken" "$reset" "$reset" > "$HOME/.devkit/quota/claude-code.local"
cat > docs/tasks/OB-200.md <<'DOC'
# OB-200: Цель: справка утилиты доведена до новичка

## Цель

Справка утилиты не отвечает новичку на первый вопрос. DoD: раздел «Первый
запуск» описан шагами, у каждого шага команда и её вывод, и `python3 tool.py`
без аргументов печатает первый шаг.

Условий, посильных только человеку, не нашёл.

## Бюджет

бюджет: week_all <= 40
ярус: pro

## Задачи цели

Заводит нарезка первым витком.

## Журнал

## Итог
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
head = ("## Backlog\n\n"
        "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
        "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-200 | Цель: справка утилиты доведена до новичка | task | P2 | "
       "40 (30+5+3+0+2) | XL | [tasks/OB-200.md](tasks/OB-200.md) |\n")
if head not in s:
    raise SystemExit("шапка Backlog в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(head, head + row, 1))
PY
git add docs && git commit -q --no-verify -m "docs(tasks): цель OB-200 заведена"
```

## Промпт

Я ставлю цель OB-200, строка уже на доске в Backlog, файл цели
`docs/tasks/OB-200.md` заполнен, бюджет в нём назван мной. Постановку ведёт
скилл goal-start, модельного вызова у него нет, поэтому прочитай его файл
`~/.claude/skills/goal-start/SKILL.md` и сделай по нему всё, что осталось до
шага коммита. Потом скажи мне, с чем цель уходит в работу. Коммитов не делай,
строк на доске не заводи и не двигай, субагентов не поднимай, код проекта не
правь, нарезку не делай.

## Проверка

```sh
grep -q 'agentctl fit' "$OBEY_TRANSCRIPT" ||
	{ echo "бюджет с остатком не сверялся: agentctl fit в транскрипте нет"; exit 1; }
grep -q 'бюджет: week_all <= 40' docs/tasks/OB-200.md ||
	{ echo "числа бюджета правил сам агент, а назначает их пользователь"; exit 1; }
sed -n '/^## Backlog/,/^## Blocked/p' docs/TASKS.md | grep -q 'OB-200' ||
	{ echo "цель уехала из Backlog: постановка встала работой до первого витка"; exit 1; }
git status --porcelain docs/TASKS.md | grep -q . &&
	{ echo "доска правилась, хотя постановка на этом шаге её не трогает"; exit 1; }
grep -q 'fit: ' "$OBEY_TRANSCRIPT" ||
	{ echo "ответ сверки в работу не попал: строки «fit: » в транскрипте нет"; exit 1; }
echo "сверка позвана, числа целы, цель осталась в Backlog"
```
