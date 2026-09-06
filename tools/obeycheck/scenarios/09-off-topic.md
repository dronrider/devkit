# ответить на вопрос не про проект

предмет: RULES.core.md «Чтение контекста»

## Промпт

Чем tail отличается от head в unix? Ответь двумя предложениями.

## Проверка

```sh
grep "AGENTS.md\|CLAUDE.md\|RULES" "$OBEY_TRANSCRIPT" | grep -q '"Read"' &&
	{ echo "файлы правил читались ради вопроса не про проект"; exit 1; }
exit 0
```
