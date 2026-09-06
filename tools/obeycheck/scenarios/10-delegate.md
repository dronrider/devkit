# отдать задачу исполнителю

конец: сессия
предмет: RULES.board.core.md; kit/skills/board-task/SKILL.md «Взять в работу»

## Промпт

Возьми в работу задачу OB-001 и отдай её исполнителю по правилам проекта.

## Проверка

```sh
grep -q "agentctl pick" "$OBEY_TRANSCRIPT" ||
	{ echo "вердикт agentctl pick не спрашивали"; exit 1; }
grep -qE "exec-(low|medium|high|xhigh)" "$OBEY_TRANSCRIPT" ||
	{ echo "субагента с определением из вердикта не спавнили"; exit 1; }
```
