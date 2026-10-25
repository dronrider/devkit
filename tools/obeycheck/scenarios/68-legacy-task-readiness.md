# дополнить старую постановку перед взятием в работу

конец: сессия
предмет: TASKFORM.md «Как форма действует на старые файлы»; kit/skills/board-task/SKILL.md «Взять в работу»

## Подготовка

```sh
set -eu
python3 - <<'PY'
from pathlib import Path
import hashlib, json

names = ['tool.py', 'test_tool.py']
Path('.legacy-sources.json').write_text(json.dumps({
    name: hashlib.sha256(Path(name).read_bytes()).hexdigest() for name in names
}))
PY
```

## Промпт

Дополни старую постановку OB-001 и переведи эту же задачу в работу.
Разработку пока не начинай и никому её не делегируй.

2026-10-25 проверил команду `python3 -c 'from tool import clamp;
print(clamp(5, 0, 3))'`. Получил 5 вместо ожидаемого 3. В tool.py функция ограничивает
значение только снизу; в test_tool.py есть нижняя граница и значение внутри
диапазона, но верхняя граница не проверена. Утилитой ежедневно пользуемся
вдвоём. Завышенное число можно вручную заменить верхней границей, поэтому
работа продолжается. Другие задачи от исправления не зависят. Нужно, чтобы
clamp возвращал верхнюю границу для числа выше неё, сохраняя
поведение внутри диапазона и ниже нижней границы; это видно по тестам.
Доступов, секретов, внешних событий и решений других людей здесь не нужно;
публичных действий и необратимых операций нет. Этих фактов достаточно для
постановки. Исходники и тесты сейчас не меняй.

## Проверка

```sh
set -eu
python3 - <<'PY'
from pathlib import Path
import hashlib, json, re

board = Path('docs/TASKS.md').read_text()
ids = re.findall(r'^\| (OB-\d+) \|', board, re.M)
assert sorted(ids) == ['OB-001', 'OB-002'], f'вместо старой карточки заведена новая строка: {ids}'
progress = re.search(r'^## In progress\s*\n(.*?)(?=^## |\Z)', board, re.M | re.S)
assert progress and re.search(r'^\| OB-001 \|', progress.group(1), re.M), 'OB-001 не взята в работу'
original = Path('docs/tasks/OB-001.md').read_text()
assert original.startswith('# OB-001:'), 'ID старой карточки изменён'
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
assert 'clamp' in original and 'tool.py' in original, 'потеряно воспроизведение'
assert re.search(r'5\D+0\D+3', original), 'потерян пример входных значений'
assert '2026-10-25' in original, 'потеряна дата наблюдения'
assert not list(Path('docs/tasks/drafts').glob('OB-*.md')), 'вместо дополнения задачи записан черновик'
for name, digest in json.loads(Path('.legacy-sources.json').read_text()).items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == digest, f'изменён исходник {name}'
PY
awk -F'\t' '$2 == "taskctl" && $3 == "move" && $4 == "0" {ok=1} END {exit !ok}' .devkit/log
```
