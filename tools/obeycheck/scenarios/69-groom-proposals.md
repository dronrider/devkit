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
view = json.loads(subprocess.check_output(['taskctl', 'draft', 'prepared', 'OB-069'], text=True))
assert view.get('ready') is True, 'разбор не готов к подтверждению'
assert isinstance(view['state'].get('pack_revision'), int) and view['state']['pack_revision'] > 0, 'нет редакции всего разбора'
cards = view['state']['proposals']
assert len(cards) >= 2, 'нужны отдельные задачи для двух поправок'
assert all(not c.get('status') for c in cards), 'задача применена без подтверждения'
def recorded_commands(path):
    starts, success = [], set()
    for line in path.read_text().splitlines():
        parts = line.split("\t", 2)
        if len(parts) != 3:
            continue
        kind, ident, value = parts
        if kind == "start":
            starts.append((ident, value))
        elif kind == "end" and value == "0":
            success.add(ident)
    return [command for ident, command in starts if ident in success]

log = pathlib.Path('.obey-taskctl.log')
if log.is_file():
    commands = recorded_commands(log)
else:
    events = [json.loads(line) for line in pathlib.Path(os.environ['OBEY_TRANSCRIPT']).read_text().splitlines() if line.startswith('{')]
    commands = [e.get('item', {}).get('command', '') for e in events if e.get('type') == 'item.completed' and e.get('item', {}).get('type') == 'command_execution' and e.get('item', {}).get('exit_code') == 0]
save = next((i for i, c in enumerate(commands) if re.search(r'draft\s+prepared\s+save', c) and '--base' in c and '--help' not in c), None)
interview = next((i for i, c in enumerate(commands) if 'decide' in c and '--scenarios' in c), None)
assert save is not None, 'задачи не сохранены командой'
ask_index = next((i for i, c in enumerate(commands) if re.search(r'draft\s+prepared\s+ask\s+OB-069', c) and '--help' not in c), None)
assert ask_index is not None and save < ask_index, 'вопрос не опубликован после сохранения'
assert not any(re.search(r'draft\s+prepared\s+(?:answer|confirm|reject)\b', c) for c in commands), 'решение применено без ответа человека'
trace = pathlib.Path(os.environ['OBEY_TRANSCRIPT']).read_text().splitlines()
events = [json.loads(line) for line in trace if line.startswith('{')]
assert any(e.get('type') == 'turn.completed' for e in events), 'сессия не завершилась'
assert any(e.get('item', {}).get('type') == 'agent_message' and e['item'].get('text', '').strip() for e in events), 'нет реплики агента'
ask = pathlib.Path('.devkit/chat/task-OB-069.ask')
assert ask.is_file(), 'вопрос согласования не опубликован'
parts = [line for line in ask.read_text().splitlines() if line.startswith('{')]
assert len(parts) == 1, 'неверный сохранённый вопрос'
questions = json.loads(parts[0]).get('questions', [])
shown = [q for q in questions if q.get('prepared')]
assert len(shown) == 1, 'нет отдельного вопроса согласования'
question = shown[0]['prepared']
assert question.get('version') == 1 and question.get('tree') == os.path.realpath('.') and question.get('anchor') == 'OB-069', 'неверный адрес показанного вопроса'
sources = question.get('sources', [])
assert len(sources) == 1 and sources[0].get('source') == 'OB-069', 'источник вопроса не совпал'
assert sources[0].get('revision') == view['state']['pack_revision'], 'редакция всего разбора не совпала'
options = shown[0].get('options', [])
assert len(options) == len(sources), 'согласие должно относиться к целому черновику'
assert [o.get('label') for o in options] == ['Подтвердить разбор ' + source['source'] for source in sources], 'вопрос содержит отдельное согласие на задачу'
assert view.get('basis') and sources[0].get('basis') == view['basis'], 'показанный basis не совпал'
assert all(c.get('id') and isinstance(c.get('revision'), int) and c['revision'] > 0 for c in view['state']['proposals']), 'у задачи нет ID или редакции'
def stripped_text(cards):
    return [{key: value for key, value in card.items() if key != 'text'} for card in cards]
assert stripped_text(sources[0].get('proposals', [])) == stripped_text([c for c in view['state']['proposals'] if not c.get('status')]), 'показанные задачи или редакции не совпали'
assert all(not c.get('text') for c in sources[0]['proposals']), 'тело задачи въехало в вопрос'

assert interview is not None and interview < save, 'интервью не предшествует подготовке задач'
assert not any(re.search(r'\btaskctl\s+(?:add\b|draft\s+(?:attach|drop|defer)\b)', c) for c in commands), 'вызван прямой исход'

PY
```
