# Хуки: проверка запрещённых символов

Механизация п. 1 раздела «Код и тексты» RULES.md: в своём тексте только
символы клавиатурных раскладок en/ru, «ёлочки» и `№`. Проверка стоит в двух
рубежах: git-хуки ловят перед коммитом, PostToolUse-хук Claude Code сразу при
записи файла (агент получает находки как фидбек и переписывает сам, правило не
занимает контекст).

Ядро одно, `check-symbols.py` (нужен только python3): аргументами файлы, либо
`--stdin` для потока, либо `--hook` для Claude Code. Выход 0 чисто, 1 находки
(в режиме `--hook` находки дают 2, так их видит агент).

## Git-хуки (подключаются на проект)

Из корня проекта, devkit лежит рядом:

```sh
git config core.hooksPath ../devkit/hooks
```

`pre-commit` проверяет только добавленные строки staged-диффа, `commit-msg`
текст коммита без комментариев git. Совпадения в чужом коде не всплывают, пока
его не трогаешь; для осознанного исключения (тестовые данные с юникодом)
`git commit --no-verify`.

`core.hooksPath` выключает `.git/hooks` целиком. Если у проекта уже есть свои
хуки, вместо этого симлинки на конкретные:

```sh
ln -s ../../../devkit/hooks/pre-commit .git/hooks/pre-commit
ln -s ../../../devkit/hooks/commit-msg .git/hooks/commit-msg
```

## Хук Claude Code (подключается глобально)

В `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [
          { "type": "command", "command": "python3 ~/projects/devkit/hooks/check-symbols.py --hook" }
        ]
      }
    ]
  }
}
```

Хук смотрит не файл целиком, а записанный фрагмент (new_string/content из
tool_input), поэтому чужой существующий текст не трогает.

## Самопроверка

```sh
hooks/test.sh
```
