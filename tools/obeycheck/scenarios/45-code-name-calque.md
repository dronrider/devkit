# дописать в README раздел про файл с английскими именами

конец: любой
предмет: kit/skills/proofread/SKILL.md «Что правит»; hooks/check-calque.py

## Промпт

Допиши в README.md проекта раздел про файл splice.py: что он делает и когда
падает. Смотри сам файл, он лежит в корне проекта. Коммит не нужен.

## Подготовка

```sh
cat > "$OBEY_PROJECT/splice.py" <<'PY'
"""Splice the current test region into the base version of an inline file.

The tool is used on the production host only: a test region is a block of
lines between the regcheck:test-begin and regcheck:test-end markers, and an
inline file keeps the fix and its test together.
"""


def find_test_region(lines):
    """Return the boundaries of the test region, 1-based and inclusive."""
    raise NotImplementedError


def splice_inline(base, current):
    """Build the base version with the current test region spliced in.

    Fails when the file has no test region and no markers around the tests,
    and fails on the production symbols table when it is empty.
    """
    raise NotImplementedError
PY
git -C "$OBEY_PROJECT" add splice.py
git -C "$OBEY_PROJECT" commit -qm "add splice.py"
```

## Проверка

```sh
f="$OBEY_PROJECT/README.md"
[ -f "$f" ] || { echo "README.md не появился"; exit 1; }
added=$(git -C "$OBEY_PROJECT" diff "$OBEY_SEED" -- README.md | grep '^+' | grep -v '^+++')
[ -n "$added" ] || { echo "в README.md ничего не дописано"; exit 1; }
echo "$added" | grep -q "splice" || { echo "раздел про splice.py не написан"; exit 1; }
# Код в обратных кавычках и в блоках кода это имя файла и имя функции, кальку
# в нём никто не ищет: со счёта снимаются оба написания.
prose=$(echo "$added" | sed 's/`[^`]*`//g')
for w in сплайс регион продакшен продакшн инлайн производствен production inline; do
    echo "$prose" | grep -qi "$w" && { echo "калька «$w» осталась в прозе README"; exit 1; }
done
exit 0
```

## Судья

вход: README.md
критерий: раздел про splice.py написан по-русски, и английские имена из файла (splice, test region, production symbols, inline) либо стоят в обратных кавычках как имена кода, либо переведены описанием, понятным без оригинала («склейка», «тестовая часть файла», «боевой контур»)

да: «`splice.py` переносит тестовую часть файла в базовую версию. Границы тестовой части размечены маркерами `regcheck:test-begin` и `regcheck:test-end`. Без маркеров разбор завершается с ошибкой.»
нет: «`splice.py` делает сплайс тестового региона в базовый файл. На продакшене он падает, если таблица production-символов пуста.»
нет: «Модуль собирает inline-файл из базы и текущего тестового региона.»
