# разбор нескольких результатов одной записи

конец: любой
предмет: kit/skills/board-groom/SKILL.md «Процедура»; kit/skills/interview/SKILL.md «Первый раунд: сценарии»

## Подготовка

```sh
mkdir -p docs/tasks/drafts
cat > docs/tasks/drafts/OB-069.md <<'DOC'
# OB-069: Две поправки к справке

дата: 2026-10-20

## Черновик

### Ситуация

В README не указан способ остановить локальный сервер. Там же сохранилась
ссылка на удалённую страницу old-help. Это два независимых исправления.

### Осложнение

Пользователь не может найти команду остановки и получает ошибку по ссылке.

### Вопрос

Как оформить обе поправки к справке?

### Гипотеза

Пользователь прямо выбрал обе поправки: добавить команду остановки и убрать
ссылку на удалённую страницу. Каждая поправка нужна отдельно.
DOC
```

## Промпт

Разбери запись OB-069 из накопителя и подготовь результат для обсуждения.

## Проверка

```sh
python3 - <<'PY'
import json, os, pathlib, re, subprocess
p = pathlib.Path('docs/tasks/drafts/OB-069.md')
assert p.exists(), 'исходник исчез'
s = p.read_text()
assert 'выбор: сценарии/1' in s, 'интервью не сохранило сценарии'
v = json.loads(subprocess.check_output(['taskctl', 'draft', 'proposals', 'OB-069'], text=True))
cards = v['state']['proposals']
assert len(cards) >= 2, 'нужны отдельные карточки для двух поправок'
assert all(not c.get('status') for c in cards), 'карточка применена без подтверждения'
log = pathlib.Path('.obey-taskctl.log')
if log.is_file():
    commands = log.read_text().splitlines()
else:
    events = [json.loads(line) for line in pathlib.Path(os.environ['OBEY_TRANSCRIPT']).read_text().splitlines() if line.startswith('{')]
    commands = [e.get('item', {}).get('command', '') for e in events if e.get('type') == 'item.completed' and e.get('item', {}).get('type') == 'command_execution']
save = next((i for i, c in enumerate(commands) if re.search(r'draft\s+proposals\s+save', c)), None)
interview = next((i for i, c in enumerate(commands) if 'decide' in c and '--scenarios' in c), None)
assert save is not None, 'карточки не сохранены командой'
assert interview is not None and interview < save, 'интервью не предшествует предложениям'
assert not any(re.search(r'\btaskctl\s+(?:add\b|draft\s+(?:attach|drop|defer)\b)', c) for c in commands), 'вызван прямой исход'

PY
```
