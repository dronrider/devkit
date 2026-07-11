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

- `RULES.md` - правила работы: git, ревью, стиль кода и текстов,
  трекинг задач (доска `docs/TASKS.md`).
- `RANKING.md` - система приоритизации: Ranking 1..100 из пяти слагаемых,
  раскладка в приоритеты P0..P3.
- `taskctl/` - Go-утилита канбан-доски `docs/TASKS.md`
  (add/move/close/sort/lint/id), см. [taskctl/README.md](taskctl/README.md).

Установка taskctl в PATH:

```bash
cd taskctl && go build -o ~/go/bin/taskctl .
```
