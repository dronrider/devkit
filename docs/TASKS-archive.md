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
| DK-019 | agentctl: снимок квоты обновляется сам, отсутствие снимка не молчит | task | P3 | 2026-07-31 | [tasks/archive/2026/DK-019.md](tasks/archive/2026/DK-019.md), `6c949ce`, `c12cc35`, `9e4b8d9`, `806f25a`, `9e02705` |
| DK-026 | doctor всегда красный на самом devkit: ссылки в архиве задач ведут мимо | bug | P3 | 2026-08-01 | [tasks/archive/2026/DK-026.md](tasks/archive/2026/DK-026.md), `b7cf586`, `279a37d`, `c6d3154`, `7322161`, `44ffb02`, `2c055e1`, `d54d77a`, `1233c43`, `41a8bd2` |
| DK-034 | уведомления, когда сессия ждёт действия или субагент закончил работу | task | P3 | 2026-08-01 | [tasks/archive/2026/DK-034.md](tasks/archive/2026/DK-034.md) |
| DK-047 | клик по уведомлению переключает в окно сессии: отправитель terminal-notifier на macOS | task | P3 | 2026-08-01 | [tasks/archive/2026/DK-047.md](tasks/archive/2026/DK-047.md), `afc0397`, `b5c7441`, `e9594c2`, `9b4e355`, `1b06137` |
| DK-033 | LLD: devkit как универсальный кит для любых ИИ-агентов | LLD | P3 | 2026-08-01 | [tasks/archive/2026/DK-033.md](tasks/archive/2026/DK-033.md), `b1b0538`, `ca0d42c`, `c78d918`, `18dd9e1` |
| DK-055 | Ревью бага идёт по пути от симптома к правке: звено вне диффа обосновывается вслух, сценарий проверки начинается с воспроизведения бага | task | P2 | 2026-08-01 | [tasks/archive/2026/DK-055.md](tasks/archive/2026/DK-055.md), `b7d085e`, `136eb94`, `2d5d724`, `ae5be88` |
| DK-058 | devkitctl doctor --fix не перекладывает разошедшееся определение агента, только называет cp: правка промптов не доезжает на машину сама | bug | P2 | 2026-08-01 | [tasks/archive/2026/DK-058.md](tasks/archive/2026/DK-058.md), `0fd9d0f` |
| DK-059 | клик по уведомлению подменяет активное окно VS Code деревом задачи | bug | P1 | 2026-08-01 | [tasks/archive/2026/DK-059.md](tasks/archive/2026/DK-059.md), `f6b3038`, `92983cc` |
| DK-036 | профиль харнеса и слои конфигурации: claude-code.toml описывает сегодняшнее поведение | task | P3 | 2026-08-01 | [tasks/archive/2026/DK-036.md](tasks/archive/2026/DK-036.md), `4228775`, `bc7d086`, `8687e43`, `0c655e4`, `c121427` |
| DK-048 | LLD: диспетчер пачки задач, параллельное исполнение независимых задач бэклога | LLD | P3 | 2026-08-01 | [tasks/archive/2026/DK-048.md](tasks/archive/2026/DK-048.md), `2047c4e`, `32f066a`, `0c6c008` |
| DK-037 | ярусы mini/base/pro/max в agentctl, третья строка вердикта tier | task | P3 | 2026-08-01 | [tasks/archive/2026/DK-037.md](tasks/archive/2026/DK-037.md), `4e52b0a`, `edb4a80`, `c26a92b`, `301a373` |
| DK-038 | снимок остатка по харнесам: директория quota/, бакеты ярусами, сменный съёмщик | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-038.md](tasks/archive/2026/DK-038.md), `bab801c`, `cefc245`, `9368262`, `5578b4f`, `d205861`, `a978e96` |
| DK-060 | CI-шаг проверки символов всегда красный: он ловит чужой вывод панели в agentctl/testdata | bug | P2 | 2026-08-02 | [tasks/archive/2026/DK-060.md](tasks/archive/2026/DK-060.md), `ca754bc` |
| DK-061 | клик по уведомлению открывает лишнее окно worktree вместо окна сессии | bug | P2 | 2026-08-01 | [tasks/archive/2026/DK-061.md](tasks/archive/2026/DK-061.md), `9d5f21c`, `2e81b3f`, `20fea02` |
| DK-039 | генерация файлов правил: AGENTS.md источник, тонкий CLAUDE.md, вклейка с маркерами | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-039.md](tasks/archive/2026/DK-039.md), `2348855`, `969ddd0`, `e761d82`, `095d28d`, `efc6778`, `a90f96b`, `3e4ee90`, `11da9e7` |
| DK-062 | уведомления по важности: громкий повод против фонового, событие Stop | task | P2 | 2026-08-02 | [tasks/archive/2026/DK-062.md](tasks/archive/2026/DK-062.md), `b45b082`, `7b5baa2`, `a1cc8b8`, `4211c2f`, `0ed5d9c`, `59f2af0` |
| DK-063 | громкие уведомления из taskctl и shipctl: Check, блокер, провал выката | task | P2 | 2026-08-02 | [tasks/archive/2026/DK-063.md](tasks/archive/2026/DK-063.md), `f6260c6`, `285db59`, `2e4815a`, `563c153`, `a1341e0`, `e192296`, `32a6806`, `44d15ce`, `b8a585b`, `24442a0` |
| DK-064 | конец хода зовёт по фокусу окна, а не по длительности | bug | P2 | 2026-08-02 | [tasks/archive/2026/DK-064.md](tasks/archive/2026/DK-064.md), `d5163fa`, `da0f914`, `54e319c`, `f63ebf3` |
| DK-065 | Факт выката хранится строкой задачи, а не выводится поиском ID в сабджекте | task | P2 | 2026-08-02 | [tasks/archive/2026/DK-065.md](tasks/archive/2026/DK-065.md), `2abe87f`, `0b40648`, `b48cf39`, `862c35a`, `213e669` |
| DK-050 | taskctl batch: отбор кандидатов пачки | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-050.md](tasks/archive/2026/DK-050.md) |
| DK-066 | Два исхода проверки: провал выката и приёмка с замечаниями, непроверенный выкат виден после возврата в работу | task | P2 | 2026-08-02 | [tasks/archive/2026/DK-066.md](tasks/archive/2026/DK-066.md), `2687f6a`, `7b14f71`, `2c63074`, `8c50d27`, `f7f8b9b`, `9b710f3`, `6340051`, `a5e3d01`, `9b098f2` |
| DK-051 | agentctl budget: размер пачки из остатка лимитов | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-051.md](tasks/archive/2026/DK-051.md), `f3b2ed9`, `1206bde`, `c3dbadf`, `bf2d51f` |
| DK-052 | taskctl: рубеж доски из worktree и идемпотентный file | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-052.md](tasks/archive/2026/DK-052.md) |
| DK-053 | shipctl: замок конвейера, тесты из конфига, бескодовая задача поезда | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-053.md](tasks/archive/2026/DK-053.md), `fd8fbb5`, `a28072f`, `3c1f620`, `4df298c`, `74407f0`, `9a6347b`, `baf5880` |
| DK-054 | скилл board-batch, раскладка скиллов и ключ test в devkitctl | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-054.md](tasks/archive/2026/DK-054.md), `cb0e0e6`, `0a5d36d`, `e3e3b67`, `49f61aa`, `ab08c3e` |
| DK-073 | taskctl draft и скилл board-groom: черновик задачи мимо доски, оформление отдельным шагом | task | P3 | 2026-08-02 | [tasks/archive/2026/DK-073.md](tasks/archive/2026/DK-073.md), `ca93d9f`, `9116c77`, `055963e`, `fbb4186`, `c900741`, `aa12aac`, `6e7a806`, `d8122ba` |
| DK-075 | doctor --fix дописывает недостающие ключи в готовый deploy.local, а не только в болванку new | task | P2 | 2026-08-02 | [tasks/archive/2026/DK-075.md](tasks/archive/2026/DK-075.md), `0319a35`, `aa29e7c`, `9b71773`, `8a97a1b`, `54b5678`, `6928c06`, `df394a1`, `6032e61`, `715de25`, `f7787d0`, `edf8064`, `995fc04`, `cabab72`, `f305ed5`, `8f79fcb` |
| DK-072 | devkitctl/test.sh без бита x, локальный прогон как в доке падает | bug | P3 | 2026-08-03 | [tasks/archive/2026/DK-072.md](tasks/archive/2026/DK-072.md), `96bfa53`, `633984e`, `91756b6`, `dbaba62`, `fce6d1b`, `28d56b9`, `0bce14f`, `7e55e7d` |
| DK-056 | Неопределённость в RANKING.md растёт от пересекаемых границ: правка через процессы и языки (сервер плюс клиент, JNI, UI) это минимум 3 | task | P3 | 2026-08-03 | [tasks/archive/2026/DK-056.md](tasks/archive/2026/DK-056.md), `31bdcee`, `09c0f59`, `78a5d65`, `42d2204` |
| DK-018 | agentctl: строка --record всегда несёт состояние квоты, а не только сдвиг | task | P3 | 2026-08-03 | [tasks/archive/2026/DK-018.md](tasks/archive/2026/DK-018.md), `81f30e5`, `77f216a`, `2678e1c`, `4f81ac9` |
| DK-041 | хуки: разбор входа по имени протокола, следы ходовых ассистентов в check-commit | task | P3 | 2026-08-03 | [tasks/archive/2026/DK-041.md](tasks/archive/2026/DK-041.md), `eeb510b`, `8f9b244`, `4464ee4`, `117c74c`, `3b891b9`, `21012d7` |
| DK-070 | mergedShas читает файл задачи построчно и не знает про ограждённые блоки: процитированный раздел «Выкат» становится настоящей записью | bug | P3 | 2026-08-03 | [tasks/archive/2026/DK-070.md](tasks/archive/2026/DK-070.md), `62981fd`, `71e8cac`, `6e3de24`, `76d4150`, `38c639f`, `b7a937f`, `b8d8e72`, `3b2271b`, `aefcf6d`, `337e4c7`, `c21cb82`, `e8d02f9` |
| DK-069 | Уведомитель шумит из песочницы: живой баннер на taskctl move во временном репозитории | bug | P2 | 2026-08-03 | [tasks/archive/2026/DK-069.md](tasks/archive/2026/DK-069.md), `591d1d1`, `09bde0d`, `f6abf28`, `6c6a4a3`, `90a6504`, `dedb608`, `6c45d6b` |
| DK-074 | Корпоративный контур: конвейер devkit поверх внешнего трекера без следов ИИ | LLD | P2 | 2026-08-03 | [tasks/archive/2026/DK-074.md](tasks/archive/2026/DK-074.md), `8263bca`, `65cc22f`, `1bd0ecd`, `b6a0b3d`, `0e42d07`, `db10288`, `000bb2d` |
| DK-079 | Автономный цикл разработки: цель, лимит трат и самоперезапуск | LLD | P2 | 2026-08-03 | [tasks/archive/2026/DK-079.md](tasks/archive/2026/DK-079.md), `e3b2e60`, `ae1d98a`, `23ffa47`, `4a1a02b`, `a7a2fd8`, `d4a579c`, `8c67796`, `e4325c5`, `6e757a7` |
| DK-094 | скилл goal-loop, вход постановки: разделы файла цели, строка XL на доске, бюджет диалогом | task | P2 | 2026-08-03 | [tasks/archive/2026/DK-094.md](tasks/archive/2026/DK-094.md), `bb2f3ab`, `a948df0`, `b92be40`, `72ba2ec`, `1d4a62b` |
| DK-093 | agentctl: гейт бюджета цели (spend) и потолок яруса в pick (--goal) | task | P2 | 2026-08-03 | [tasks/archive/2026/DK-093.md](tasks/archive/2026/DK-093.md), `56bbe02`, `f6d6629`, `b0c7278`, `6c61f88` |
| DK-097 | скилл live-core: порог, канал через порядок devkit, границы | task | P3 | 2026-08-03 | [tasks/archive/2026/DK-097.md](tasks/archive/2026/DK-097.md), `3a684e9`, `d20d8b5`, `7e90c04`, `803859f`, `b559b2f`, `ddf9657`, `e124da5` |
| DK-068 | Развести Blocked и зависимость [после ...]: внешнее обстоятельство против своей же задачи | task | P3 | 2026-08-03 | [tasks/archive/2026/DK-068.md](tasks/archive/2026/DK-068.md), `e908d94`, `0507484`, `fc5a13e`, `046892e`, `e9cd6c7`, `1a8ef87`, `d1329e7`, `a099336`, `62035d2`, `ce9403c`, `655f7cd`, `71747ee` |
| DK-067 | taskctl list: чего ждёт задача в Check и сколько строка лежит без движения | task | P3 | 2026-08-04 | [tasks/archive/2026/DK-067.md](tasks/archive/2026/DK-067.md), `7bf2236`, `6dcfde8`, `eee65d6`, `ed8cba8`, `39c7013`, `e30d52e`, `e5720a8`, `6f95cf5`, `77c25e1`, `b00b245` |
| DK-095 | скилл goal-loop, виток: чтение состояния, нарезка, конвейер задачи, журнал, маркеры | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-095.md](tasks/archive/2026/DK-095.md), `0460c36`, `62ea849`, `653e102` |
| DK-096 | оболочка goal-run: tmux-цикл витков, замок, защита от воронки, уведомления | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-096.md](tasks/archive/2026/DK-096.md), `27f1f59`, `530738a`, `ba35c59`, `6eb8846` |
| DK-100 | Дерево контекста: тонкий резидентный корень, правила скиллами и хуками | LLD | P2 | 2026-08-04 | [tasks/archive/2026/DK-100.md](tasks/archive/2026/DK-100.md) |
| DK-103 | Ось скиллов в профиле харнеса, глубина правил в генераторе | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-103.md](tasks/archive/2026/DK-103.md) |
| DK-102 | obeycheck: стенд послушания, сценарии, две раскладки, прогон на временном HOME | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-102.md](tasks/archive/2026/DK-102.md) |
| DK-110 | devkitctl weigh: живой замер резидента headless-прогоном и сверка с расчётом doctor | task | P3 | 2026-08-04 | [tasks/archive/2026/DK-110.md](tasks/archive/2026/DK-110.md) |
| DK-104 | RULES.core.md и RULES.board.core.md: разрез ядра под стенд | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-104.md](tasks/archive/2026/DK-104.md) |
| DK-107 | Тонкая глобальная точка правил в doctor/setup | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-107.md](tasks/archive/2026/DK-107.md) |
| DK-113 | Права headless-витков цели: allowlist машинного контура через doctor и предполётная проверка goal-run | task | P1 | 2026-08-04 | [tasks/archive/2026/DK-113.md](tasks/archive/2026/DK-113.md) |
| DK-106 | Хуки вместо прозы: разбор в текстах находок, тесты в pre-commit, pre-push на запрет пуша | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-106.md](tasks/archive/2026/DK-106.md) |
| DK-105 | Правила скиллами: board-task, board-ship, test-standard | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-105.md](tasks/archive/2026/DK-105.md) |
| DK-029 | бюджет на резидентную часть правил, порог в devkitctl doctor | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-029.md](tasks/archive/2026/DK-029.md) |
| DK-118 | Тело скилла goal-loop вдвое выше порога DK-029: резать надвое или пересматривать порог | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-118.md](tasks/archive/2026/DK-118.md) |
| DK-108 | devkitctl stats --context: куда ушёл объём сессии и перезаписи префикса | task | P3 | 2026-08-04 | [tasks/archive/2026/DK-108.md](tasks/archive/2026/DK-108.md) |
| DK-109 | Цель: тонкий резидент по DK-100, минус 65% контекста правил | task | P2 | 2026-08-04 | [tasks/archive/2026/DK-109.md](tasks/archive/2026/DK-109.md) |
| DK-081 | Редирект корня в боковую директорию и признак корп-контура | task | P3 | 2026-08-04 | [tasks/archive/2026/DK-081.md](tasks/archive/2026/DK-081.md) |
| DK-082 | trackctl: адаптер трекера, конфиг контура, маппинг статусов | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-082.md](tasks/archive/2026/DK-082.md) |
| DK-086 | Хуки корп-контура: обёртка чужих хуков и рубеж следов devkit | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-086.md](tasks/archive/2026/DK-086.md) |
| DK-121 | Уведомление конца хода: в баннере видно задачу и завершённый шаг | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-121.md](tasks/archive/2026/DK-121.md), `c9ac2d5`, `a7dc1a9` |
| DK-084 | trackctl submit: ворклоги по фактам, эстимейт, pull-синхронизация | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-084.md](tasks/archive/2026/DK-084.md) |
| DK-085 | devkitctl: подключение корп-проекта и диагностика следов | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-085.md](tasks/archive/2026/DK-085.md) |
| DK-087 | shipctl: ветка по ключу тикета, отказ слияния в корп-контуре | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-087.md](tasks/archive/2026/DK-087.md) |
| DK-083 | Адаптер jira: REST-операции и образцы ответов API | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-083.md](tasks/archive/2026/DK-083.md) |
| DK-124 | Рубеж следов бьёт по ключу тикета: коммит по конвенции компании не проходит | bug | P1 | 2026-08-05 | [tasks/archive/2026/DK-124.md](tasks/archive/2026/DK-124.md) |
| DK-111 | Цель: корп-контур по DK-074, конвейер поверх внешнего трекера | task | P3 | 2026-08-05 | [tasks/archive/2026/DK-111.md](tasks/archive/2026/DK-111.md) |
| DK-125 | Инструкция подключения проекта: два сценария, обычный и корп, без раскладки на пользователя | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-125.md](tasks/archive/2026/DK-125.md) |
| DK-129 | LLD: организация груминга черновиков, критерий готовности и режимы захода | LLD | P2 | 2026-08-05 | [tasks/archive/2026/DK-129.md](tasks/archive/2026/DK-129.md), `23d7609`, `bd36f38`, `c4eb101`, `da849cc`, `67aca37` |
| DK-132 | taskctl: исходы разбора черновика, команды defer, attach и drop | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-132.md](tasks/archive/2026/DK-132.md), `865f505`, `e2d9db7`, `5128c1d`, `b81f802`, `5825ef3`, `3efc9d5`, `b6b8fdd` |
| DK-130 | board-groom: исследование черновика до оформления, режимы захода и тест скилла | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-130.md](tasks/archive/2026/DK-130.md), `baf71e4`, `5ba26dd`, `f266d5b` |
| DK-131 | Разбор накопителя черновиков devkit доработанным board-groom | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-131.md](tasks/archive/2026/DK-131.md) |
| DK-139 | LLD: раскладка каталогов devkit и правило выбора языка инструмента | LLD | P2 | 2026-08-05 | [tasks/archive/2026/DK-139.md](tasks/archive/2026/DK-139.md) |
| DK-128 | Цель: груминг черновиков доски с критерием готовности и разбором пачкой | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-128.md](tasks/archive/2026/DK-128.md) |
| DK-140 | переезд файлов devkit по новой раскладке с починкой путей на машинах | task | P2 | 2026-08-05 | [tasks/archive/2026/DK-140.md](tasks/archive/2026/DK-140.md) |
| DK-141 | раннер тестов devkitctl на python вместо devkitctl/test.sh | task | P3 | 2026-08-06 | [tasks/archive/2026/DK-141.md](tasks/archive/2026/DK-141.md) |
| DK-142 | раннер тестов хуков на python вместо hooks/test.sh | task | P3 | 2026-08-06 | [tasks/archive/2026/DK-142.md](tasks/archive/2026/DK-142.md) |
| DK-143 | остаток sh по правилу выбора языка: раннеры скиллов и мелочь | task | P3 | 2026-08-06 | [tasks/archive/2026/DK-143.md](tasks/archive/2026/DK-143.md) |
| DK-144 | devkitctl doctor ловит файл не по правилу раскладки | task | P3 | 2026-08-06 | [tasks/archive/2026/DK-144.md](tasks/archive/2026/DK-144.md) |
| DK-135 | Цель: раскладка devkit по местам и правило выбора языка инструмента | task | P2 | 2026-08-06 | [tasks/archive/2026/DK-135.md](tasks/archive/2026/DK-135.md) |
| DK-149 | LLD: чем доставляются готовые бинари devkit и откуда бинарь знает свою версию | LLD | P2 | 2026-08-06 | [tasks/archive/2026/DK-149.md](tasks/archive/2026/DK-149.md) |
| DK-150 | Бинари называют свой коммит, а собирает их релизная автоматика на macos и linux | task | P2 | 2026-08-06 | [tasks/archive/2026/DK-150.md](tasks/archive/2026/DK-150.md) |
| DK-151 | Установка и обновление devkit на чистой машине без сборки из исходников | task | P2 | 2026-08-06 | [tasks/archive/2026/DK-151.md](tasks/archive/2026/DK-151.md) |
| DK-152 | doctor сверяет версии всех устанавливаемых бинарей, а не mtime четырёх | task | P2 | 2026-08-06 | [tasks/archive/2026/DK-152.md](tasks/archive/2026/DK-152.md) |
| DK-155 | update не обновляется, пока в origin двигается служебный тег deployed | bug | P1 | 2026-08-06 | [tasks/archive/2026/DK-155.md](tasks/archive/2026/DK-155.md) |
| DK-157 | Лог установки говорит по-человечески, а окружение доезжает без ручных brew | task | P2 | 2026-08-06 | [tasks/archive/2026/DK-157.md](tasks/archive/2026/DK-157.md) |
| DK-160 | doctor из каталога проектов ползёт по чужим репозиториям и печатает сотни ложных находок | bug | P2 | 2026-08-07 | [tasks/archive/2026/DK-160.md](tasks/archive/2026/DK-160.md) |
| DK-163 | doctor на машине потребителя требует обвязку разработчика devkit | bug | P2 | 2026-08-07 | [tasks/archive/2026/DK-163.md](tasks/archive/2026/DK-163.md) |
| DK-127 | Генератор правил переписывает CLAUDE.md чужого чекаута абсолютными путями временного devkit | bug | P1 | 2026-08-07 | [tasks/archive/2026/DK-127.md](tasks/archive/2026/DK-127.md), `d91face`, `e00247d`, `f43f245`, `b9e6a7d`, `141672d`, `58d4ada`, `15ff887` |
| DK-164 | второе обновление на машине потребителя отказывает: отставший origin/main выдаёт тег за неотправленную работу | bug | P1 | 2026-08-07 | [tasks/archive/2026/DK-164.md](tasks/archive/2026/DK-164.md) |
| DK-138 | Цель: devkit ставится на чистую машину без сборки из исходников | task | P2 | 2026-08-07 | [tasks/archive/2026/DK-138.md](tasks/archive/2026/DK-138.md) |
| DK-154 | shipctl: вставший deploy проваливается по пределу, ожидание фона через Monitor | task | P2 | 2026-08-07 | [tasks/archive/2026/DK-154.md](tasks/archive/2026/DK-154.md) |
| DK-153 | Сторожок цикла цели: простой дольше порога зовёт громко | task | P1 | 2026-08-07 | [tasks/archive/2026/DK-153.md](tasks/archive/2026/DK-153.md) |
| DK-169 | goal-run: стоп без вердикта перезапускает виток, попытки ограничены, продолжение одной командой | task | P2 | 2026-08-07 | [tasks/archive/2026/DK-169.md](tasks/archive/2026/DK-169.md) |
| DK-156 | Цель: автономный цикл не стоит молча и переживает обрывы | task | P1 | 2026-08-07 | [tasks/archive/2026/DK-156.md](tasks/archive/2026/DK-156.md) |
| DK-174 | Скилл командной работы над одним проектом: захват задачи и разбор столкновений | task | P2 | 2026-08-07 | [tasks/archive/2026/DK-174.md](tasks/archive/2026/DK-174.md) |
| DK-175 | Запускатор vscode с конфигом z.ai: своё окно на второй подписке и dry-run окружения | task | P2 | 2026-08-08 | [tasks/archive/2026/DK-175.md](tasks/archive/2026/DK-175.md) |
| DK-090 | Вторая подписка ступенью лестницы: ярус разворачивается в чужой харнес | LLD | P2 | 2026-08-08 | [tasks/archive/2026/DK-090.md](tasks/archive/2026/DK-090.md) |
| DK-177 | Назначение яруса в машинном слое: строка via и запись про подписку | task | P2 | 2026-08-08 | [tasks/archive/2026/DK-177.md](tasks/archive/2026/DK-177.md) |
| DK-179 | Раскладка машинного контура по профилям включённых харнесов | task | P2 | 2026-08-08 | [tasks/archive/2026/DK-179.md](tasks/archive/2026/DK-179.md) |
| DK-040 | agentctl run: делегирование native/cli/none с ограничителем вложенности | task | P2 | 2026-08-08 | [tasks/archive/2026/DK-040.md](tasks/archive/2026/DK-040.md) |
| DK-173 | Проза агентов в задачах и доке тяжело читается человеком | LLD | P2 | 2026-08-08 | [tasks/archive/2026/DK-173.md](tasks/archive/2026/DK-173.md), `505aefe`, `00f5e8e`, `3d6ee13`, `33ac83b` |
| DK-178 | agentctl run отдаёт работу харнесу назначения: каталог и окружение подпроцесса | task | P2 | 2026-08-08 | [tasks/archive/2026/DK-178.md](tasks/archive/2026/DK-178.md) |
| DK-180 | Профиль glm-code и живой прогон конвейера на второй подписке | task | P2 | 2026-08-08 | [tasks/archive/2026/DK-180.md](tasks/archive/2026/DK-180.md) |
| DK-189 | Секция харнеса без ярусов глушит предложение маппинга из профиля | bug | P2 | 2026-08-08 | [tasks/archive/2026/DK-189.md](tasks/archive/2026/DK-189.md), `833d2c4`, `b159859`, `081bdd6`, `d47f9bf` |
| DK-190 | doctor: вес резидента и импорты правил подключённого проекта | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-190.md](tasks/archive/2026/DK-190.md), `e403b10`, `0d8ab51`, `f76f9b1`, `6dbcf5a`, `8eab34b` |
| DK-192 | Слияние из окна запускатора убивает окно: копия на окно вместо копии на задачу | bug | P2 | 2026-08-09 | [tasks/archive/2026/DK-192.md](tasks/archive/2026/DK-192.md), `a079a04`, `fffedf8`, `0f5e3c2`, `76a211b`, `8b88c11`, `37025fe` |
| DK-171 | Цель: подписка z.ai в конвейере: гетерогенная лестница, запускатор vscode, скилл командной работы | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-171.md](tasks/archive/2026/DK-171.md) |
| DK-193 | LLD: доставка правил devkit в сессии подключённых проектов | LLD | P2 | 2026-08-09 | [tasks/archive/2026/DK-193.md](tasks/archive/2026/DK-193.md), `b2249d3`, `a2cb6aa`, `bd67ff2`, `ca3bff3`, `46aebad`, `87dceaf` |
| DK-204 | Доставка правил через .devkit: ссылки и тонкий файл в devkitctl | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-204.md](tasks/archive/2026/DK-204.md) |
| DK-205 | Доставка правил через .devkit: ссылки в дереве задачи от shipctl start | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-205.md](tasks/archive/2026/DK-205.md) |
| DK-191 | taskctl/shipctl: команда перехода печатает следующий шаг конвейера | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-191.md](tasks/archive/2026/DK-191.md) |
| DK-117 | Первый виток цели идёт в чате, а ход фоновых витков виден до их завершения | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-117.md](tasks/archive/2026/DK-117.md) |
| DK-212 | Ворота перехода в Check: сценарий в файле задачи и слитая ветка | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-212.md](tasks/archive/2026/DK-212.md) |
| DK-213 | Хук check-commit: ID задачи в коммите на main проекта с доской | task | P2 | 2026-08-09 | [tasks/archive/2026/DK-213.md](tasks/archive/2026/DK-213.md) |
| DK-089 | Выбор модели опирается на недельный лимит, расход которого описан в профиле неверно | bug | P2 | 2026-08-09 | [tasks/archive/2026/DK-089.md](tasks/archive/2026/DK-089.md) |
| DK-214 | Копия окна второй подписки едет за main сама | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-214.md](tasks/archive/2026/DK-214.md) |
| DK-215 | LLD дашборда: границы сервер-клиент, API, аутентификация, раскладка кода | LLD | P2 | 2026-08-10 | [tasks/archive/2026/DK-215.md](tasks/archive/2026/DK-215.md) |
| DK-216 | Экраны дашборда в Claude Design: телефон и ноутбук | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-216.md](tasks/archive/2026/DK-216.md) |
| DK-226 | secretctl: имена агенту, значение в подпроцесс через два бэкенда | task | P1 | 2026-08-10 | [tasks/archive/2026/DK-226.md](tasks/archive/2026/DK-226.md), `0ee4a9e`, `58b0ea0`, `c6d595e` |
| DK-217 | Каркас дашборда: сервер с аутентификацией и доска проектов на чтение | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-217.md](tasks/archive/2026/DK-217.md) |
| DK-165 | Исполнитель возвращает работу незакоммиченной, ожидая фоновый прогон | bug | P2 | 2026-08-10 | [tasks/archive/2026/DK-165.md](tasks/archive/2026/DK-165.md) |
| DK-218 | Запуск и стоп задачи и цели с дашборда | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-218.md](tasks/archive/2026/DK-218.md) |
| DK-119 | Коммит доски, собранный руками, отбивает pre-push | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-119.md](tasks/archive/2026/DK-119.md) |
| DK-219 | Живой статус агента в дашборде: журнал витка, транскрипт, tmux | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-219.md](tasks/archive/2026/DK-219.md) |
| DK-220 | Переписка с агентом через состояние цели | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-220.md](tasks/archive/2026/DK-220.md) |
| DK-229 | settings.json: allow без литеральных токенов, копии прочь, находка doctor | task | P1 | 2026-08-10 | [tasks/archive/2026/DK-229.md](tasks/archive/2026/DK-229.md), `1712cbf`, `6451734`, `c0882cc`, `671f858` |
| DK-221 | Правка задачи и цели с дашборда, зависимости в обе стороны | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-221.md](tasks/archive/2026/DK-221.md) |
| DK-126 | hook_events падает traceback на структурно необычном hooks-JSON | bug | P2 | 2026-08-10 | [tasks/archive/2026/DK-126.md](tasks/archive/2026/DK-126.md), `982e67d`, `ff82475`, `1b0b3b5` |
| DK-228 | Рубеж чтения секретов: permissions.deny и PreToolUse-хук на Bash | task | P1 | 2026-08-10 | [tasks/archive/2026/DK-228.md](tasks/archive/2026/DK-228.md), `6a73980`, `be786fc`, `1ac22b3`, `4d586a4` |
| DK-222 | Лента уведомлений дашборда и smoke агентской части DoD | task | P2 | 2026-08-10 | [tasks/archive/2026/DK-222.md](tasks/archive/2026/DK-222.md) |
| DK-099 | taskctl: разбор аргументов подкоманд молча теряет данные (позиционный после флага, имя несуществующей подкоманды) | bug | P2 | 2026-08-10 | [tasks/archive/2026/DK-099.md](tasks/archive/2026/DK-099.md), `9b44437`, `7b4e34c`, `474e653` |
| DK-223 | Заход извне и проверка дашборда с телефона | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-223.md](tasks/archive/2026/DK-223.md) |
| DK-244 | Экран задачи дашборда: единая кнопка сохранения, валидация слагаемых по типу, спокойное живое обновление | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-244.md](tasks/archive/2026/DK-244.md) |
| DK-242 | Экраны дашборда открываются секунды: профилировать и убрать ожидание из стартовой и карточки задачи | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-242.md](tasks/archive/2026/DK-242.md) |
| DK-243 | Доска дашборда: действия со строки, ранг тултипом, дата статуса вместо возраста, кнопка на главную | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-243.md](tasks/archive/2026/DK-243.md) |
| DK-245 | Заведение задачи и черновика с дашборда | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-245.md](tasks/archive/2026/DK-245.md) |
| DK-246 | Лента уведомлений входом с колокольчика, а не пунктом меню | task | P3 | 2026-08-11 | [tasks/archive/2026/DK-246.md](tasks/archive/2026/DK-246.md) |
| DK-134 | taskctl: -m съедает следующий флаг как сообщение, пуш молча пропадает | bug | P2 | 2026-08-11 | [tasks/archive/2026/DK-134.md](tasks/archive/2026/DK-134.md) |
