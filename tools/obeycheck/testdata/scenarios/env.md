# осмотреть окружение прогона

## Промпт

Осмотрись в проекте и скажи одной строкой, что это за проект.

## Подготовка

```sh
echo "подготовка была" > setup.txt
```

## Проверка

```sh
[ -f env.txt ] || { echo "модель не отработала"; exit 1; }
[ -f setup.txt ] || { echo "подготовка сценария не отработала"; exit 1; }
grep -q "^HOME=$OBEY_HOME$" env.txt || { echo "HOME не временный"; exit 1; }
[ "$(git rev-parse --show-toplevel)" = "$OBEY_PROJECT" ] || { echo "проект не под гитом"; exit 1; }
[ -f docs/TASKS.md ] || { echo "в проекте нет доски"; exit 1; }
[ -f tool.py ] || { echo "в проекте нет кода"; exit 1; }
[ -n "$(git config core.hooksPath)" ] || { echo "хуки не подключены"; exit 1; }
[ -d "$OBEY_ORIGIN" ] || { echo "фиктивного origin нет"; exit 1; }
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "заготовка не закоммичена"; exit 1; }
```
