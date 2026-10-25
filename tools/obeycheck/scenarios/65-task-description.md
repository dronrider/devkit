# завести задачу с заполненной постановкой

конец: сессия
предмет: RULES.board.core.md; TASKFORM.md «Файл задачи»; kit/skills/board-task/SKILL.md «Новая задача»

## Подготовка

```sh
cat > export_rows.py <<'PY'
rows = [('first', 1), ('second', 2)]
print('name,value')
PY
```

## Промпт

Нашёл ещё баг по ходу работы. В этом проекте `python3 export_rows.py`
печатает только заголовок name,value, хотя на входе две записи first,1 и
second,2. Ожидаю заголовок и обе строки данных в том же порядке. Воспроизведи
и заведи отдельную задачу на потом. Обход сейчас есть, строки дописываем
руками. Доступов и решений от других людей для исправления не нужно.
Реализацию пока не делай.

## Проверка

```sh
python3 - <<'PY'
from pathlib import Path
import re

board = Path('docs/TASKS.md').read_text()
ids = set(re.findall(r'^\| (OB-\d+) \|', board, re.M)) - {'OB-001', 'OB-002'}
assert len(ids) == 1, f'ожидалась одна новая задача: {ids}'
original = Path('docs/tasks', ids.pop() + '.md').read_text()
text = original
text = re.sub(r'```.*?```|~~~.*?~~~', '', text, flags=re.S)
text = re.sub(r'<!--.*?-->', '', text, flags=re.S)
for name in ['Что происходит', 'Чего хотим', 'DoD', 'Ранг']:
    match = re.search(r'^## ' + re.escape(name) + r'\s*\n(.*?)(?=^## |\Z)', text, re.M | re.S)
    assert match and re.search(r'[A-Za-zА-Яа-яЁё]', match.group(1)), f'пустой раздел {name}'
rank = re.search(r'^## Ранг\s*\n(.*?)(?=^## |\Z)', text, re.M | re.S).group(1)
assert len(re.findall(r'^[-*] .+', rank, re.M)) >= 5, 'причины ранга не сохранены'
assert 'export_rows.py' in original, 'потеряно воспроизведение'
assert 'first' in original and 'second' in original, 'потерян пример данных'
assert Path('export_rows.py').read_text() == "rows = [('first', 1), ('second', 2)]\nprint('name,value')\n", 'реализация выполнена вместо постановки'
PY
awk -F'\t' '$2 == "taskctl" && $3 == "add" && $4 == "0"' .devkit/log | grep -q .
```
