# devkit: сделано

Append-only журнал закрытых задач, растёт свободно. Файлы закрытых задач лежат
в tasks/archive/<год закрытия>/, ссылка в строке ведёт туда.

| ID | Задача | Тип | P | Закрыто | Ссылка |
|--------|--------|-----|---|---------|--------|
| DK-005 | shipctl start: worktree на задачу, чтобы параллельные сессии не толкались в одном дереве | task | P2 | 2026-07-28 | [tasks/archive/2026/DK-005.md](tasks/archive/2026/DK-005.md), `0ba8a78`, `90ba4ae`, `244e295`, `ef435e9` |
| DK-006 | agentctl: выбор модели под задачу по метаданным доски для делегирования субагенту | task | P3 | 2026-07-28 | [tasks/archive/2026/DK-006.md](tasks/archive/2026/DK-006.md), `30649e4`, `2c87608`, `8f49310` |
| DK-007 | agentctl: pick --record фиксирует модель исполнения в файле задачи | task | P3 | 2026-07-29 | [tasks/archive/2026/DK-007.md](tasks/archive/2026/DK-007.md), `dda115c`, `45eeb16`, `38b17eb` |
| DK-008 | shipctl start: worktree ветвится до коммита перевода, taskctl file в нём конфликтует с main при ребейзе | bug | P3 | 2026-07-29 | [tasks/archive/2026/DK-008.md](tasks/archive/2026/DK-008.md), `a45ed6f` |
| DK-001 | taskctl: машинно-читаемые зависимости blocks/blocked-by и lint инварианта зависимости | task | P3 | 2026-07-29 | [tasks/archive/2026/DK-001.md](tasks/archive/2026/DK-001.md), `94a6a88`, `16535eb`, `679e536`, `11bdd96` |
| DK-003 | regcheck: проверка инлайновых тестов, когда правка и тест в одном файле | task | P3 | 2026-07-29 | [tasks/archive/2026/DK-003.md](tasks/archive/2026/DK-003.md) |
| DK-002 | devkitctl: сводка по журналу запусков .devkit/log (частота команд, доля ошибок) | task | P3 | 2026-07-29 | [tasks/archive/2026/DK-002.md](tasks/archive/2026/DK-002.md), `e6bf0db`, `a774fa4` |
| DK-004 | devkitctl doctor и shipctl merge: предупреждение про autonomous = true при пустом deploy | task | P3 | 2026-07-29 | [tasks/archive/2026/DK-004.md](tasks/archive/2026/DK-004.md), `681d6a5`, `2b58b7d`, `ace06d1`, `edbd4ee` |
| DK-010 | LLD: квотный корректор вердикта pick при недоступном остатке лимитов подписки | LLD | P3 | 2026-07-30 | [tasks/archive/2026/DK-010.md](tasks/archive/2026/DK-010.md), `6a477ce`, `920c84e`, `e3b52d9`, `80814ac`, `2439e0c`, `eda0598` |
| DK-009 | agentctl: fable-ярус маппинга (LLD по цене, сложный код) и доменный override модели | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-009.md](tasks/archive/2026/DK-009.md), `63ae6b6`, `d7cd632`, `95ae063`, `e5de270` |
| DK-013 | agentctl: opus по умолчанию, sonnet без размышлений, haiku только атомарное | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-013.md](tasks/archive/2026/DK-013.md), `0839d84`, `81454f5`, `f394dab` |
| DK-011 | agentctl: effort в вердикте pick и применение через определения субагентов | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-011.md](tasks/archive/2026/DK-011.md), `8779e88`, `4efe090`, `073a9ce`, `7c9e604`, `7471172`, `790c2b0` |
| DK-014 | agentctl: effort в вердикте pick отвязать от модели, считать по неопределённости | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-014.md](tasks/archive/2026/DK-014.md), `be4f083`, `7ccc876`, `752a1f8`, `e11119d`, `96f4194`, `410660a` |
| DK-015 | agentctl: сдвиг лестницы моделей в pick (M к opus, sonnet не ниже high) | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-015.md](tasks/archive/2026/DK-015.md), `82a3d40`, `2f0b51b`, `297de3a` |
| DK-012 | agentctl: квотный корректор вердикта pick по снимку /usage | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-012.md](tasks/archive/2026/DK-012.md), `aed6910`, `4415d3e`, `f184092` |
| DK-016 | agentctl: уборка после DK-015 (pick без вызовов, пробелы тестов, README) | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-016.md](tasks/archive/2026/DK-016.md), `c7b316a`, `ace2a8b` |
| DK-017 | agentctl: совет отложить до сброса для любого сдвига вниз с цены M и выше | task | P3 | 2026-07-30 | [tasks/archive/2026/DK-017.md](tasks/archive/2026/DK-017.md), `3a17a32`, `2b54873`, `625c1fc`, `2cde743` |
| DK-022 | agentctl: панель /usage сменила бакет opus на fable, парсер и лестница трат | task | P2 | 2026-07-30 | [tasks/archive/2026/DK-022.md](tasks/archive/2026/DK-022.md), `1f4de4b`, `270cbc7`, `001c3ce`, `65f7810`, `3b4000c`, `e5a5b99` |
| DK-020 | devkitctl doctor закрывает машинный контур, а не только проектный | task | P3 | 2026-07-31 | [tasks/archive/2026/DK-020.md](tasks/archive/2026/DK-020.md), `48f9ffc`, `5c41d36`, `4a75e10`, `3878952`, `03cfb5b`, `7b45cff`, `47e1d80` |
| DK-023 | выбор модели ревьювера тоже через pick, а не на глаз | task | P3 | 2026-07-31 | [tasks/archive/2026/DK-023.md](tasks/archive/2026/DK-023.md), `9fb6f55`, `8d65628`, `fa177bb`, `0bae361`, `edbadc6`, `27f70cf`, `52400cb` |
| DK-030 | обоснования из правил в README утилит, в правилах императив | task | P3 | 2026-07-31 | [tasks/archive/2026/DK-030.md](tasks/archive/2026/DK-030.md), `fb7366a`, `990628a`, `78ca896` |
