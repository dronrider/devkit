# devkit: общие правила и инструменты

Один репозиторий на все проекты: правила работы с ассистентом, система
приоритизации задач и инструменты доски. Клонируется рядом с проектами:

```bash
git clone https://github.com/dronrider/devkit.git ~/projects/devkit
```

Проект подключает правила импортом в своём `CLAUDE.md`:

```markdown
@../devkit/RULES.md
```

Импорт это локальный путь, поэтому devkit должен лежать в соседней с проектом
директории. Если файла нет, агент по строке-подсказке в проектном `CLAUDE.md`
клонирует devkit сам.

## Состав

- `RULES.md` - ядро правил для любой сессии: роль, тесты, git, стиль кода
  и текстов, документация изменений.
- `RULES.board.md` - дополнение для проектов с доской `docs/TASKS.md`: ветки,
  ревью и деплой, трекинг задач. Подключается вторым импортом только там, где
  доска ведётся, и не ест контекст остальных проектов.
- `RANKING.md` - система приоритизации: Ranking 1..100 из пяти слагаемых,
  раскладка в приоритеты P0..P3.
- `taskctl/` - Go-утилита канбан-доски `docs/TASKS.md`
  (init/add/move/close/sort/lint/id), см. [taskctl/README.md](taskctl/README.md).
- `hooks/` - проверка запрещённых символов: git-хуки `pre-commit` и
  `commit-msg` плюс PostToolUse-хук Claude Code, см.
  [hooks/README.md](hooks/README.md).
- `templates/CLAUDE.project.md` - заготовка проектного `CLAUDE.md`: импорт
  правил, подсказка клонирования devkit, объявление доски или трекера.

Установка taskctl в PATH:

```bash
cd taskctl && go build -o ~/go/bin/taskctl .
```

Новый проект подключается так: копия шаблона в корень как `CLAUDE.md`, доска
через `taskctl init --prefix XX` (если нет внешнего трекера), хуки по
[hooks/README.md](hooks/README.md).
