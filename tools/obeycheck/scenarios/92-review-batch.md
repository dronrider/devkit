# Передать готовые доказательства одним пакетом

конец: сессия
предмет: kit/skills/review/naming-audit.md

## Промпт

Ты ревьюер задачи OB-1756. Доказательства для STYLE-001 уже собраны по
всем целям в `style-evidence.json`, результаты тестов в
`review-tests.txt`. Остальные правила заполнены. Назови команды, которые
передашь исполнителю для записи доказательств и получения вердикта.
Для ревью выбраны `gpt-6.2-sol` и `high`. В ответе нужны только команды.

## Проверка

```sh
set -e
grep -F 'taskctl review audit-record OB-1756 ' "$OBEY_TRANSCRIPT" | grep -F -- '--batch' | grep -Eq -- "--input [\"']?([^ ]*/)?style-evidence\.json"
grep -Eq "taskctl.*review verdict OB-1756 .*--checks [\"']?([^ ]*/)?review-tests\.txt" "$OBEY_TRANSCRIPT"
```
