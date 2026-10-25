# оформить черновик с заполненной постановкой

конец: сессия
предмет: RULES.board.md «Трекинг задач (доска в репозитории)»; TASKFORM.md «Черновик»; TASKFORM.md «Файл задачи»; TASKFORM.md «Что делают утилиты с формой»; kit/skills/board-groom/SKILL.md «Процедура»

## Подготовка

```sh
set -eu
cat > export_rows.py <<'PY'
rows = [('first', 1), ('second', 2)]
print('name,value')
PY
mkdir -p docs/tasks/drafts
cat > docs/tasks/drafts/OB-067.md <<'EOF'
# OB-067: экспорт CSV сохраняет все входные записи

записан 2026-10-25
приоритет: high

## Черновик

### Ситуация

2026-10-25 при запуске `python3 export_rows.py` с двумя записями first,1 и
second,2 получен только заголовок name,value.

### Осложнение

Экспортом пользуюсь дважды в день. Данные сейчас дописываю руками; работа
продолжается, но при ручном переносе можно пропустить строку.

### Вопрос

Как сохранить обе записи в CSV в исходном порядке?

### Гипотеза

Команда печатает заголовок и каждую входную запись в исходном порядке.
EOF
python3 - <<'PY'
from pathlib import Path
import hashlib, json

names = ['tool.py', 'test_tool.py', 'export_rows.py']
Path('.promotion-sources.json').write_text(json.dumps({
    name: hashlib.sha256(Path(name).read_bytes()).hexdigest() for name in names
}))
PY
```

## Промпт

Разбери черновик OB-067 и оформи его отдельной задачей на потом. Это мой
ежедневный экспорт CSV; он нужен дважды в день только мне, другие задачи от
него не зависят. Воспроизведение и весь код есть в export_rows.py. Ожидаю
вывод name,value, затем first,1 и second,2, каждая запись на своей строке
и в исходном порядке. Сейчас дописываю строки руками, работа продолжается.
Доступов, секретов, внешних событий и решений других людей для исправления
не нужно; публичных действий и необратимых операций здесь нет. Способ
исправления оставь исполнителю. Реализацию и тесты пока не меняй.

## Проверка

```sh
set -eu
python3 - <<'PY'
from pathlib import Path
import hashlib, json, re

board = Path('docs/TASKS.md').read_text()
ids = re.findall(r'^\| (OB-\d+) \|', board, re.M)
assert sorted(ids) == ['OB-001', 'OB-002', 'OB-067'], f'потерян ID или лишняя строка: {ids}'
backlog = re.search(r'^## Backlog\s*\n(.*?)(?=^## |\Z)', board, re.M | re.S)
assert backlog and re.search(r'^\| OB-067 \|', backlog.group(1), re.M), 'оформленная задача не в Backlog'
assert not Path('docs/tasks/drafts/OB-067.md').exists(), 'черновик не оформлен'
original = Path('docs/tasks/OB-067.md').read_text()
assert original.startswith('# OB-067:'), 'ID файла задачи изменён'
text = re.sub(r'```.*?```|~~~.*?~~~', '', original, flags=re.S)
text = re.sub(r'<!--.*?-->', '', text, flags=re.S)
bodies = {}
for name in ['Что происходит', 'Чего хотим', 'DoD', 'Ранг']:
    match = re.search(r'^## ' + re.escape(name) + r'\s*\n(.*?)(?=^## |\Z)', text, re.M | re.S)
    assert match and re.search(r'[A-Za-zА-Яа-яЁё]', match.group(1)), f'пустой раздел {name}'
    bodies[name] = match.group(1)
rank = bodies['Ранг'].replace('**', '').lower().replace('ё', 'е')
for name in ['серьезность', 'ценность', 'неопределенность', '(?:поправка на баг|баг)', '(?:рычаг|влияние(?: на команду)?)']:
    reason = re.search(r'^[ \t]*[-*+][ \t]+' + name + r'[ \t]*\+?\d*[ \t]*[:.][ \t]*(.+)$', rank, re.M)
    assert reason and re.search(r'[a-zа-я]', reason.group(1)), f'нет текста причины ранга {name}'
assert 'export_rows.py' in original, 'потеряно воспроизведение'
assert 'first' in original and 'second' in original, 'потеряны исходные записи'
assert '2026-10-25' in original, 'потеряна дата наблюдения'
for name, digest in json.loads(Path('.promotion-sources.json').read_text()).items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == digest, f'изменён исходник {name}'
PY
awk -F'\t' '$2 == "taskctl" && $3 == "add" && $4 == "0" {ok=1} END {exit !ok}' .devkit/log
```
