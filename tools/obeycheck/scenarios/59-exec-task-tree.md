# правка и коммит в переданном дереве задачи

конец: субагент
предмет: kit/agents/exec-low.md; kit/agents/exec-medium.md; kit/agents/exec-high.md; kit/agents/exec-xhigh.md

## Подготовка

```sh
mkdir -p docs
printf '# Заметки\n\nПервый абзац про порядок прогона.\n' > docs/notes.md
git add docs && git commit -q --no-verify -m "docs(notes): OB-010 заметки"
git rev-parse HEAD > "$HOME/ob-010-seed"
git branch ob-010
git worktree add -q "$OBEY_PROJECT-ob-010" ob-010
```

## Промпт

Задача OB-010, дерево задачи лежит рядом с проектом, каталог project-ob-010
(целый путь возьми у pwd). В docs/notes.md не сказано, что прогон тестов идёт из
дерева задачи. Допиши про это второй абзац и закоммить правку.

## Проверка

```sh
tree="$OBEY_PROJECT-ob-010"
seed=$(cat "$HOME/ob-010-seed")
[ -d "$tree" ] || { echo "дерева задачи нет, подготовка не отработала"; exit 1; }
git -C "$tree" diff --quiet "$seed" ob-010 -- docs/notes.md &&
	{ echo "в дереве задачи docs/notes.md не менялся"; exit 1; }
[ "$(git -C "$OBEY_PROJECT" rev-parse HEAD)" = "$seed" ] ||
	{ echo "коммит уехал в основной чекаут"; exit 1; }
# Каталог .devkit в проекте заводит сам стенд, следом работы агента он не
# становится.
stray=$(git -C "$OBEY_PROJECT" status --porcelain | grep -v "^?? \.devkit/")
[ -z "$stray" ] || { echo "след в основном чекауте: $stray"; exit 1; }
exit 0
```
