# devkit: задачи (префикс DK)

Канбан-доска открытой работы и единственный источник правды по тому, что в
работе. Правила ведения в devkit: RULES.md, раздел «Трекинг задач». Строка
держит метаданные и короткий заголовок, разбор задачи живёт в файле
tasks/DK-NNN.md (имя по чистому ID, файл заводится, когда появляется
содержимое). Закрытое переезжает в [TASKS-archive.md](TASKS-archive.md).
Механику доски гоняет taskctl (смотреть list/show, менять add/move/set/close),
таблицы руками не править.

Приоритет P выводится из ранга R по RANKING.md (devkit). Формула
R = Серьёзность(0-75) + Ценность(0-10) + Неопределённость(0-5) + Баг(+5) + Рычаг(0-5),
колонка R держит сумму и разбивку в этом порядке. Бакеты: P0 при R >= 75, P1
при 50-74, P2 при 25-49, P3 при 0-24. Backlog отсортирован по R убыванию, при
равенстве по возрастанию ID. Типы: bug / task / LLD. Цена это грубая оценка
затрат агента на исполнение (S / M / L / XL, шкала в RANKING.md), в ранг не
входит; «-» значит «не оценено».

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-048 | LLD: диспетчер пачки задач, параллельное исполнение независимых задач бэклога | LLD | P3 | 15 (0+7+4+0+4) | M | [tasks/DK-048.md](tasks/DK-048.md) |
| DK-058 | devkitctl doctor --fix не перекладывает разошедшееся определение агента, только называет cp: правка промптов не доезжает на машину сама | bug | P2 | 38 (25+4+1+5+3) | S | [tasks/DK-058.md](tasks/DK-058.md) |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-036 | профиль харнеса и слои конфигурации: claude-code.toml описывает сегодняшнее поведение | task | P3 | 14 (0+8+1+0+5) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-037 | ярусы mini/base/pro/max в agentctl, третья строка вердикта tier [после DK-036] | task | P3 | 14 (0+8+1+0+5) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-038 | снимок остатка по харнесам: директория quota/, бакеты ярусами, сменный съёмщик [после DK-037] | task | P3 | 14 (0+8+1+0+5) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-039 | генерация файлов правил: AGENTS.md источник, тонкий CLAUDE.md, вклейка с маркерами [после DK-036] | task | P3 | 14 (0+8+1+0+5) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-041 | хуки: разбор входа по имени протокола, следы ходовых ассистентов в check-commit [после DK-036] | task | P3 | 14 (0+8+1+0+5) | S | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-042 | devkitctl setup: мастер машинного конфига с файлом ответов, поглощает DK-032 [после DK-038, DK-039, DK-041] | task | P3 | 14 (0+9+1+0+4) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-050 | taskctl batch: отбор кандидатов пачки | task | P3 | 13 (0+7+1+0+5) | M | [lld/DK-048-batch-dispatcher.md](lld/DK-048-batch-dispatcher.md) |
| DK-051 | agentctl budget: размер пачки из остатка лимитов | task | P3 | 13 (0+7+1+0+5) | S | [lld/DK-048-batch-dispatcher.md](lld/DK-048-batch-dispatcher.md) |
| DK-040 | agentctl run: делегирование native/cli/none с ограничителем вложенности [после DK-037] | task | P3 | 12 (0+6+2+0+4) | L | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-043 | профиль Codex: детект, вклейка правил, headless-делегирование [после DK-040, DK-042] | task | P3 | 12 (0+8+2+0+2) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-052 | taskctl: рубеж доски из worktree и идемпотентный file | task | P3 | 12 (0+6+1+0+5) | S | [lld/DK-048-batch-dispatcher.md](lld/DK-048-batch-dispatcher.md) |
| DK-053 | shipctl: замок конвейера, тесты из конфига, бескодовая задача поезда | task | P3 | 12 (0+6+1+0+5) | M | [lld/DK-048-batch-dispatcher.md](lld/DK-048-batch-dispatcher.md) |
| DK-054 | скилл board-batch, раскладка скиллов и ключ test в devkitctl [после DK-050, DK-051, DK-052, DK-053] | task | P3 | 12 (0+6+1+0+5) | M | [lld/DK-048-batch-dispatcher.md](lld/DK-048-batch-dispatcher.md) |
| DK-044 | профиль OpenCode: детект, instructions, определения агентов [после DK-040, DK-042] | task | P3 | 11 (0+7+2+0+2) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-045 | профиль Gemini CLI: детект, контекстный файл, headless-делегирование [после DK-040, DK-042] | task | P3 | 10 (0+6+2+0+2) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-046 | профиль Cursor: детект, правила в .cursor/rules либо AGENTS.md [после DK-040, DK-042] | task | P3 | 10 (0+6+2+0+2) | M | [lld/DK-033-universal-kit.md](lld/DK-033-universal-kit.md) |
| DK-057 | Ревьювер не дешевеет ярусом, когда в правке участвует слой без автотестов: признак и его источник для agentctl pick --role review | task | P3 | 10 (0+4+3+0+3) | S | [tasks/archive/2026/DK-055.md](tasks/archive/2026/DK-055.md) |
| DK-018 | agentctl: строка --record всегда несёт состояние квоты, а не только сдвиг | task | P3 | 9 (0+5+1+0+3) | S | [tasks/DK-018.md](tasks/DK-018.md) |
| DK-032 | подключение машины к devkit одной командой: хуки в settings.json и глобальные правила | task | P3 | 9 (0+4+2+0+3) | M | [tasks/DK-032.md](tasks/DK-032.md) |
| DK-056 | Неопределённость в RANKING.md растёт от пересекаемых границ: правка через процессы и языки (сервер плюс клиент, JNI, UI) это минимум 3 | task | P3 | 9 (0+4+1+0+4) | S | [tasks/archive/2026/DK-055.md](tasks/archive/2026/DK-055.md) |
| DK-021 | agentctl: refresh честно говорит про экран входа и сохраняет неузнанную панель | task | P3 | 8 (0+5+1+0+2) | S | [tasks/DK-021.md](tasks/DK-021.md) |
| DK-024 | agentctl: выдержка на недорисованную панель не проверяется на влезание в таймаут refresh | bug | P3 | 8 (0+2+1+5+0) | S | - |
| DK-027 | слабый ассерт гитигнора в самопроверке devkitctl: грепает голое «не гитигнорнут» | bug | P3 | 8 (0+2+0+5+1) | S | - |
| DK-028 | devkitctl doctor: разбор taken берёт первую строку с = и молча принимает снимок из будущего | bug | P3 | 8 (0+2+0+5+1) | S | - |
| DK-029 | бюджет на резидентную часть правил, порог в devkitctl doctor | task | P3 | 8 (0+3+1+0+4) | S | [tasks/DK-029.md](tasks/DK-029.md) |
| DK-031 | закрытая задача уносит в архив относительные ссылки, доктор находит битые | bug | P3 | 8 (0+2+1+5+0) | S | - |
| DK-035 | инлайн-код с двойным разделителем не защищает ссылку внутри | bug | P3 | 6 (0+1+0+5+0) | S | - |
| DK-049 | своё приложение-отправитель уведомлений: иконка devkit вместо иконки terminal-notifier | task | P3 | 6 (0+3+3+0+0) | M | [tasks/DK-049.md](tasks/DK-049.md) |
| DK-025 | agentctl: пометка бакету вне лестницы трат объясняет частный случай week_opus | task | P3 | 1 (0+1+0+0+0) | S | - |

## Blocked

Нет.
