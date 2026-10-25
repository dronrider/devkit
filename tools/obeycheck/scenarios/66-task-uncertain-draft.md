# сохранить неразобранную находку черновиком

конец: сессия
предмет: RULES.board.core.md; kit/skills/board-task/SKILL.md «Новая задача»; TASKFORM.md «Черновик»

## Промпт

Сохрани мысль на потом. В пятницу заметил, что экспорт CSV иногда кажется
медленным. Пока неизвестно, сколько он занял и сколько было записей; непонятно,
что именно тормозит и какой результат считать нормальным. Сейчас разбирать
и исправлять это не надо, просто сохрани известное, чтобы не потерялось.

## Проверка

```sh
python3 - <<'PY'
from pathlib import Path
import re

board = Path('docs/TASKS.md').read_text()
ids = set(re.findall(r'^\| (OB-\d+) \|', board, re.M))
assert ids == {'OB-001', 'OB-002'}, 'неразобранная мысль попала строкой на доску'
files = list(Path('docs/tasks/drafts').glob('OB-*.md'))
assert len(files) == 1, 'нужен один черновик'
text = re.sub(r'<!--.*?-->', '', files[0].read_text(), flags=re.S)
for name in ['Ситуация', 'Осложнение', 'Вопрос', 'Гипотеза']:
    match = re.search(r'^### ' + re.escape(name) + r'\s*\n(.*?)(?=^## |^### |\Z)', text, re.M | re.S)
    assert match and re.search(r'[A-Za-zА-Яа-яЁё]', match.group(1)), f'пустой подраздел {name}'
assert 'csv' in text.lower(), 'потерян предмет'
assert 'пятниц' in text.lower(), 'потерян момент наблюдения'
assert re.search(r'неизвест|не (зна|извест|определ|понят|измер)|неясн', text.lower()), 'неизвестное не сохранено'
PY
awk -F'\t' '$2 == "taskctl" && $3 == "draft" && $4 == "0"' .devkit/log | grep -q .
```
