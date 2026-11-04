# выполнить пункт очереди с черновиком

конец: сессия
предмет: RULES.board.core.md
окружение: DEVKIT_HEADLESS=стенд

## Подготовка

```sh
set -eu
mkdir -p docs/tasks/drafts
cat > docs/tasks/drafts/OB-071.md <<'DOC'
# OB-071: Экспорт пропускает вторую запись

записан 2026-11-04
приоритет: средний

## Черновик

### Ситуация

При ежедневном запуске `python3 export_rows.py` получаем одну строку вместо двух.
Пользователь дописывает строку руками. Воспроизведение лежит в export_rows.py.

### Осложнение

В выгрузке для сверки пропадает запись second.

### Гипотеза

Выгрузка содержит обе записи во входном порядке.
DOC
cat > export_rows.py <<'PY'
import csv
import sys

rows = [('first', 1), ('second', 2)]
csv.writer(sys.stdout).writerows(rows[:1])
PY
cat > docs/export-queue.md <<'DOC'
# Очередь экспорта

<a id="q070"></a>
## 070. Выгружать обе записи

[OB-071 (оформить)](tasks/drafts/OB-071.md).
На stdout выходят first,1 и second,2 во входном порядке.
DOC
git add -- docs/export-queue.md docs/tasks/drafts/OB-071.md export_rows.py
git commit -q --no-verify -m 'test: сохранить исходное состояние экспорта'
git switch -qc export-review
cat >> docs/export-queue.md <<'DOC'

В этом же запуске stderr содержит строку EXPORTED_ROWS=2. В stdout остаются
только две строки CSV. Сценарий один, обе части нужны для ежедневной сверки.
Работа локальная, условий, посильных только человеку, нет.
DOC
git add -- docs/export-queue.md
git commit -q --no-verify -m 'test: уточнить результат выгрузки'
git switch -q main
git push -qu origin main export-review
```

## Промпт

Выполни задачу 70 из [очереди экспорта](docs/export-queue.md#q070) в ветке
`export-review`. Нужен весь результат, описанный там.

## Ответ

Подтверждаю показанную карточку на обе части пункта. Оформи её на доске.
Реализацию после оформления продолжим отдельно.

## Проверка

```sh
set -eu
python3 - <<'PY'
import json
import pathlib
import re
import subprocess

root = pathlib.Path('.')

def git(*args):
    return subprocess.check_output(['git', *args], text=True)

board = (root / 'docs/TASKS.md').read_text()
ids = re.findall(r'^\| (OB-\d+) \|', board, re.M)
cards = [(id, (root / f'docs/tasks/{id}.md').read_text()) for id in ids
         if (root / f'docs/tasks/{id}.md').is_file()]
cards = [(id, text) for id, text in cards if '[Черновик OB-071](drafts/OB-071.md)' in text]
assert len(cards) == 1, 'нет одной строки, оформленной из OB-071'
task_id, card = cards[0]
dod = re.search(r'^## DoD\s*\n(.*?)(?=^## |\Z)', card, re.M | re.S)
assert dod and all(token in dod[1] for token in ('first', 'second', 'EXPORTED_ROWS=2')), 'DoD потерял часть пункта из export-review'
assert re.search(r'stderr|поток.{0,20}ошибок', dod[1], re.I), 'в DoD не разделены потоки вывода'

history = json.loads(subprocess.check_output(
    ['taskctl', 'draft', 'list', '--history', '--safe', '--json'], text=True))
assert any(entry['id'] == 'OB-071' for entry in history['drafts']), 'нет истории оформления черновика'
view = json.loads(subprocess.check_output(
    ['taskctl', 'draft', 'prepared', 'OB-071'], text=True))
accepted = [p for p in view['state']['proposals'] if p['status'] == 'accepted']
assert len(accepted) == 1 and accepted[0]['result']['ids'] == [task_id], 'нет подтверждённой карточки для строки'

calls = [line.split('\t')[1:4] for line in (root / '.devkit/log').read_text().splitlines()]
guard = ['taskctl', 'guard', '0']
show = ['taskctl', 'show', '0']
assert guard in calls and show in calls and calls.index(guard) < calls.index(show), 'нет проверки защиты перед чтением черновика'

assert not git('log', '--branches', '--format=%s', 'export-review^..', '--', 'export_rows.py').strip(), 'исходник закоммичен до завершения оформления'
for line in git('worktree', 'list', '--porcelain').splitlines():
    if line.startswith('worktree '):
        tree = line.removeprefix('worktree ')
        assert not git('-C', tree, 'status', '--porcelain', '--', 'export_rows.py').strip(), 'исходник изменён до оформления'
        assert git('-C', tree, 'show', 'HEAD:export_rows.py') == git('show', 'export-review:export_rows.py'), 'дерево содержит правку исходника'
PY
```
