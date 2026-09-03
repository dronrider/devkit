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
| DK-227 | Значения секретов переезжают из access.local.md в secretctl | task | P1 | 2026-08-11 | [tasks/archive/2026/DK-227.md](tasks/archive/2026/DK-227.md) |
| DK-230 | check-sensitive: скоуп шире доски и паттерн корп-доменов, obeycheck | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-230.md](tasks/archive/2026/DK-230.md) |
| DK-231 | Транскрипты ~/.claude/projects очищены от утёкших тел токенов | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-231.md](tasks/archive/2026/DK-231.md) |
| DK-247 | Запуск конвейера из-под launchd не находит claude: ~/.local/bin мимо PATH агента | bug | P2 | 2026-08-11 | [tasks/archive/2026/DK-247.md](tasks/archive/2026/DK-247.md) |
| DK-232 | Корп-домен вычищен из дерева и истории, force-push пользователя | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-232.md](tasks/archive/2026/DK-232.md) |
| DK-207 | Цель: исключить попадание секретов в контекст модели | task | P1 | 2026-08-11 | [tasks/archive/2026/DK-207.md](tasks/archive/2026/DK-207.md) |
| DK-252 | Транскрипт активной задачи мешает чужие сессии: выбор по mtime вместо привязки к задаче | bug | P2 | 2026-08-11 | [tasks/archive/2026/DK-252.md](tasks/archive/2026/DK-252.md) |
| DK-253 | Остаток подписок на главной: снимки квоты подключаемыми модулями без хардкода харнесов | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-253.md](tasks/archive/2026/DK-253.md) |
| DK-240 | Первый прогон devkitctl corp одной командой: опрос контура и префикс из имени клона | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-240.md](tasks/archive/2026/DK-240.md) |
| DK-254 | Чат с агентом: открытие хвостом, якорь при обновлении, markdown и ссылки, время клиента, честные имена | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-254.md](tasks/archive/2026/DK-254.md) |
| DK-263 | Интерактивные сессии агентов видны в работах: живость по mtime транскрипта, включая окна боковых деревьев | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-263.md](tasks/archive/2026/DK-263.md) |
| DK-255 | Экран агента: журнал цели из файла при чате-цикле, заголовок вместо имени сессии, формулировки | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-255.md](tasks/archive/2026/DK-255.md) |
| DK-237 | LLD: общий модуль каркаса go-утилит: 35 функций в копиях, 12 уже разошлись | LLD | P2 | 2026-08-11 | [tasks/archive/2026/DK-237.md](tasks/archive/2026/DK-237.md) |
| DK-248 | Редизайн экранов дашборда в Claude Design: форма задачи, шапка с логотипом, лента, индикаторы, состав цели, заведение | task | P2 | 2026-08-11 | [tasks/archive/2026/DK-248.md](tasks/archive/2026/DK-248.md) |
| DK-271 | Замок ship.lock попал под git: rm пустого файла грязнит дерево и рвёт flock | bug | P2 | 2026-08-11 | [tasks/archive/2026/DK-271.md](tasks/archive/2026/DK-271.md) |
| DK-249 | Реализация редизайна: форма и тексты экранов, логотип-переход, лента с колокольчиком, кружки статуса, флеш-уведомления | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-249.md](tasks/archive/2026/DK-249.md) |
| DK-272 | board-groom: сверка предмета после ответов и критик готового решения | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-272.md](tasks/archive/2026/DK-272.md) |
| DK-264 | cmdout: утилита-обёртка на общем go-модуле, полный вывод в файл, агенту выжимку | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-264.md](tasks/archive/2026/DK-264.md), `8f330d2`, `fb15c63`, `2b1840b`, `3849fe6`, `9274731` |
| DK-273 | shipctl merge: коммит доски за время тестов роняет fast-forward | bug | P2 | 2026-08-12 | [tasks/archive/2026/DK-273.md](tasks/archive/2026/DK-273.md), `cd0293e` |
| DK-250 | Действия задачи по статусу: выполнить, продолжить, закрыть со своими промптами конвейеру | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-250.md](tasks/archive/2026/DK-250.md) |
| DK-274 | shipctl merge --train проходит при занятой очереди выката | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-274.md](tasks/archive/2026/DK-274.md), `c2b9191`, `de34c6a`, `5648978`, `89e55b6` |
| DK-080 | taskctl add --id: флаг -m падает, когда черновик не закоммичен (pathspec на удалённый файл) | bug | P3 | 2026-08-12 | [tasks/archive/2026/DK-080.md](tasks/archive/2026/DK-080.md), `6f18703`, `82a3704`, `dea4eb4`, `f0c2aed`, `8de5331` |
| DK-251 | Состав цели сабтасками, единая форма задача-черновик и раздел черновиков с оформлением | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-251.md](tasks/archive/2026/DK-251.md) |
| DK-266 | shipctl на cmdout: 13 вызовов tail() в ops.go переезжают на обёртку | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-266.md](tasks/archive/2026/DK-266.md), `2b042a4` |
| DK-256 | Экран «Агенты»: живые агенты всех проектов списком, переходы в статус и чат | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-256.md](tasks/archive/2026/DK-256.md) |
| DK-265 | regcheck: хвосты обоих прогонов и пути к полным логам | bug | P2 | 2026-08-12 | [tasks/archive/2026/DK-265.md](tasks/archive/2026/DK-265.md), `4af1650a` |
| DK-267 | cmdout: чистка старых выводов, гитигнор, находка doctor | task | P3 | 2026-08-12 | [tasks/archive/2026/DK-267.md](tasks/archive/2026/DK-267.md) |
| DK-268 | правило про длинные прогоны в долгоживящей доке | task | P3 | 2026-08-12 | [tasks/archive/2026/DK-268.md](tasks/archive/2026/DK-268.md) |
| DK-184 | Скилл proofread для вычитки прозы с парами, словарём и сторожевым корпусом | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-184.md](tasks/archive/2026/DK-184.md) |
| DK-196 | Конец хода сессии из песочницы звонит баннером: хук-путь notify без фильтра временных корней | bug | P2 | 2026-08-12 | [tasks/archive/2026/DK-196.md](tasks/archive/2026/DK-196.md) |
| DK-115 | Находка диагностики про чужой go.work рядом с go-проектом | task | P2 | 2026-08-12 | [tasks/archive/2026/DK-115.md](tasks/archive/2026/DK-115.md) |
| DK-279 | dashboard: производная tmux-сессия становится мусорной работой на экране | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-279.md](tasks/archive/2026/DK-279.md) |
| DK-282 | dashboard: флеш-уведомление нечем закрыть | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-282.md](tasks/archive/2026/DK-282.md) |
| DK-283 | Тестовый стенд дашборда пишет уведомления в живой журнал машины | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-283.md](tasks/archive/2026/DK-283.md) |
| DK-277 | shipctl merge считает открытым замечанием любую строку раздела «Ревью» с маркером списка | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-277.md](tasks/archive/2026/DK-277.md) |
| DK-281 | dashboard: сообщение агенту идёт без статуса отправки и дублируется повтором | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-281.md](tasks/archive/2026/DK-281.md) |
| DK-284 | dashboard: экран задачи на телефоне, шапка, ранг и описание | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-284.md](tasks/archive/2026/DK-284.md) |
| DK-278 | гитигнор-записи .devkit/cmdout/ и ship.lock не раскладываются автоматикой, doctor их отсутствие пропускает | task | P3 | 2026-08-13 | [tasks/archive/2026/DK-278.md](tasks/archive/2026/DK-278.md) |
| DK-137 | Цель: обёртка, отдающая агенту выжимку вместо всего вывода команды | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-137.md](tasks/archive/2026/DK-137.md) |
| DK-287 | dashboard: сообщение в чате уходит само, без кнопки «Повторить» | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-287.md](tasks/archive/2026/DK-287.md) |
| DK-280 | dashboard: разговор законченной работы не открыть ни с задачи, ни с чата | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-280.md](tasks/archive/2026/DK-280.md) |
| DK-296 | dashboard: чат открывается у обычной задачи и зовёт её целью | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-296.md](tasks/archive/2026/DK-296.md) |
| DK-292 | LLD: критерии вида приёмки задачи и место, где он назначается | LLD | P2 | 2026-08-13 | [tasks/archive/2026/DK-292.md](tasks/archive/2026/DK-292.md), `ec4839b1`, `dd981904`, `39a62af9`, `53b4769e`, `bf61a2ab`, `14d7db05` |
| DK-236 | shipctl, agentctl и trackctl молча теряют флаг за позиционным: pick отдаёт вердикт не той роли | bug | P2 | 2026-08-13 | [tasks/archive/2026/DK-236.md](tasks/archive/2026/DK-236.md), `7a8828c0`, `a3f1f27e`, `76942107`, `374a45c3`, `09ef8203`, `947f453e`, `53bd7bda`, `c3ce0cef`, `0a3ed22c` |
| DK-092 | shipctl: ворота готовности правки как предусловие merge | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-092.md](tasks/archive/2026/DK-092.md), `561da82f`, `a1e4d754`, `a4781997`, `db7251a8`, `96a90716`, `6169eb97` |
| DK-112 | Цель: дашборд управления агентской разработкой с любого устройства | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-112.md](tasks/archive/2026/DK-112.md) |
| DK-298 | taskctl: вид приёмки суффиксом строки доски, флаги add и set, ворота move check | task | P2 | 2026-08-13 | [tasks/archive/2026/DK-298.md](tasks/archive/2026/DK-298.md) |
| DK-306 | LLD: очередь слияния при занятом выкате | LLD | P2 | 2026-08-13 | [tasks/archive/2026/DK-306.md](tasks/archive/2026/DK-306.md) |
| DK-302 | shipctl и taskctl batch читают вид: один совет после выката и отбор пачки полем | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-302.md](tasks/archive/2026/DK-302.md), `9466f830`, `7e61cfd1`, `d9103266`, `e718b953` |
| DK-299 | Критерии вида приёмки в ACCEPTANCE.md и вопрос про вид в груминге, нарезке цели, работе и ревью | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-299.md](tasks/archive/2026/DK-299.md), `6184d850`, `fc680ff2`, `002e3530`, `6d8e3a9e` |
| DK-300 | Рубеж против фиктивного агентского сценария: вопрос ревью и ворота close | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-300.md](tasks/archive/2026/DK-300.md), `885f3819`, `f64335ff`, `7fb30252` |
| DK-314 | Headless-конвейер дашборда теряет исполнителей: фоновый субагент гибнет через 10 минут | bug | P1 | 2026-08-14 | [tasks/archive/2026/DK-314.md](tasks/archive/2026/DK-314.md) |
| DK-309 | Ручки дашборда отказывают молча: в журнале нет ни отказа, ни попытки | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-309.md](tasks/archive/2026/DK-309.md) |
| DK-270 | dashboard: ручка message шьёт путь цели жёстко, журнал идёт по ссылке строки | bug | P3 | 2026-08-14 | [tasks/archive/2026/DK-270.md](tasks/archive/2026/DK-270.md) |
| DK-323 | Событие уведомителя несёт задачу и проект полем, а не догадкой ленты | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-323.md](tasks/archive/2026/DK-323.md) |
| DK-316 | dashboard: перерисовка дёргает экран, позиция скролла и чат прыгают [приёмка: mixed] | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-316.md](tasks/archive/2026/DK-316.md) |
| DK-319 | dashboard: сообщение агенту ждёт витка, который никто не поднимет | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-319.md](tasks/archive/2026/DK-319.md) |
| DK-328 | LLD: макеты дашборда, перетаскивание с пересчётом ранга, поиск, выбор подписки, экран груминга [приёмка: user] | LLD | P2 | 2026-08-14 | [tasks/archive/2026/DK-328.md](tasks/archive/2026/DK-328.md) |
| DK-146 | Повторный Read неизменённого файла отвечает подсказкой, а не содержимым | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-146.md](tasks/archive/2026/DK-146.md) |
| DK-147 | Read длинного файла целиком отвечает подсказкой читать куском | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-147.md](tasks/archive/2026/DK-147.md) |
| DK-148 | Регулярный замер расхода контекста по журналам сессий | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-148.md](tasks/archive/2026/DK-148.md) |
| DK-290 | dashboard: лента задачи склеивает разговоры разных сессий | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-290.md](tasks/archive/2026/DK-290.md) |
| DK-285 | dashboard: доска на телефоне, заголовок строки и полоса действий | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-285.md](tasks/archive/2026/DK-285.md) |
| DK-317 | dashboard: строка задачи не знает про идущую работу, ни признака, ни стопа | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-317.md](tasks/archive/2026/DK-317.md) |
| DK-289 | dashboard: кнопка «Закрыть» поднимает сессию агента и молчит про это | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-289.md](tasks/archive/2026/DK-289.md) |
| DK-305 | dashboard: экран агента на телефоне, вкладка разговора и подпись живого потока | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-305.md](tasks/archive/2026/DK-305.md) |
| DK-326 | dashboard: выбор подписки при запуске работы | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-326.md](tasks/archive/2026/DK-326.md) |
| DK-325 | dashboard: поиск задач по доске и архиву | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-325.md](tasks/archive/2026/DK-325.md) |
| DK-321 | dashboard: груминг черновика зовётся грумингом, исход виден, черновик удаляется | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-321.md](tasks/archive/2026/DK-321.md) |
| DK-294 | dashboard: разговор живой сессии без узнанной задачи не открыть | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-294.md](tasks/archive/2026/DK-294.md) |
| DK-310 | shipctl: merge при занятой очереди сливается в поезд вместо отказа | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-310.md](tasks/archive/2026/DK-310.md) |
| DK-324 | dashboard: движение задачи в бэклоге перетаскиванием с пересчётом ранга [приёмка: mixed] | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-324.md](tasks/archive/2026/DK-324.md) |
| DK-286 | dashboard: непонятно, что сделают кнопки запуска и оформления | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-286.md](tasks/archive/2026/DK-286.md) |
| DK-071 | doctor проверяет полноту доки: компонент без описания в карте архитектуры это находка | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-071.md](tasks/archive/2026/DK-071.md), `970caab8`, `c048e62b`, `1db6c0ee`, `aa2f59bf`, `db0dcd2c`, `fef9b73a`, `3c44f49a`, `86558a1b`, `fd372ecf`, `55df00a5` |
| DK-336 | dashboard: составная кнопка запуска не по макету подписок | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-336.md](tasks/archive/2026/DK-336.md) |
| DK-208 | Пробная нарезка цели до старта цикла и сверка оценки с бюджетом | task | P2 | 2026-08-14 | [tasks/archive/2026/DK-208.md](tasks/archive/2026/DK-208.md) |
| DK-337 | dashboard: кнопки действий не сверены с макетами экранов 11 и 12 целиком | bug | P2 | 2026-08-14 | [tasks/archive/2026/DK-337.md](tasks/archive/2026/DK-337.md) |
| DK-136 | LLD: двусторонний чат с идущей сессией агента | LLD | P2 | 2026-08-15 | [tasks/archive/2026/DK-136.md](tasks/archive/2026/DK-136.md) |
| DK-340 | hookio: ось события инструмента и второй канал ответа протокола | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-340.md](tasks/archive/2026/DK-340.md) |
| DK-341 | hooks/inbox.py: почтальон доставляет реплики идущему витку на событии инструмента | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-341.md](tasks/archive/2026/DK-341.md) |
| DK-349 | dashboard: одиночная кнопка запуска без стрелки теряет скругление справа | bug | P2 | 2026-08-15 | [tasks/archive/2026/DK-349.md](tasks/archive/2026/DK-349.md) |
| DK-194 | LLD: карта проекта уровня модулей из кода в контекст сессий | LLD | P2 | 2026-08-15 | [tasks/archive/2026/DK-194.md](tasks/archive/2026/DK-194.md) |
| DK-342 | dashboard: состояние «доставлено агенту» в очереди исходящих и честная плашка чата | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-342.md](tasks/archive/2026/DK-342.md) |
| DK-358 | dashboard: завершённый груминг висит живой работой до 12 минут | bug | P2 | 2026-08-15 | [tasks/archive/2026/DK-358.md](tasks/archive/2026/DK-358.md) |
| DK-343 | goal-loop: живая реплика в витке и зов оболочки через python3 | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-343.md](tasks/archive/2026/DK-343.md) |
| DK-120 | Строка вердикта ревью от pick --record остаётся незакоммиченной и валит merge | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-120.md](tasks/archive/2026/DK-120.md) |
| DK-346 | devkitctl: параллельный раннер питоновой сюиты, классы отдельными процессами | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-346.md](tasks/archive/2026/DK-346.md) |
| DK-344 | goal-run.py --ask: вопрос человеку с ожиданием ответа и отметкой в ящике | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-344.md](tasks/archive/2026/DK-344.md) |
| DK-362 | dashboard: headless-запуск с доски не наблюдаем, чата нет в сессиях | bug | P2 | 2026-08-15 | [tasks/archive/2026/DK-362.md](tasks/archive/2026/DK-362.md) |
| DK-303 | Открытые строки доски получают вид приёмки по критериям | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-303.md](tasks/archive/2026/DK-303.md) |
| DK-327 | Цель: пользовательские сценарии дашборда проходят с экрана целиком, без терминала и F5 | task | P1 | 2026-08-15 | [tasks/archive/2026/DK-327.md](tasks/archive/2026/DK-327.md) |
| DK-301 | Дашборд заводит задачу с видом приёмки, флаг add становится обязательным | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-301.md](tasks/archive/2026/DK-301.md) |
| DK-338 | Конвейер отмечает этап работы: живое состояние за gitignore и история пакетом | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-338.md](tasks/archive/2026/DK-338.md) |
| DK-347 | deploy.local: компоненты команды test идут параллельно с потолком воркеров | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-347.md](tasks/archive/2026/DK-347.md) |
| DK-304 | taskctl kinds: сводка по видам приёмки, расхождениям назначения и пересмотрам | task | P3 | 2026-08-15 | [tasks/archive/2026/DK-304.md](tasks/archive/2026/DK-304.md) |
| DK-308 | Цель: вид приёмки задачи назначается критериями и виден с доски | task | P2 | 2026-08-15 | [tasks/archive/2026/DK-308.md](tasks/archive/2026/DK-308.md) |
| DK-354 | LLD: черновик на дашборде правится и грумится одним процессом [приёмка: user] | LLD | P2 | 2026-08-15 | [tasks/archive/2026/DK-354.md](tasks/archive/2026/DK-354.md) |
| DK-158 | devkitctl selfcheck: живость связки после установки проверяется одной командой | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-158.md](tasks/archive/2026/DK-158.md) |
| DK-348 | devkitctl: разгрузка тяжёлых юнитов devkitctl_test, update_test, rules_test | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-348.md](tasks/archive/2026/DK-348.md) |
| DK-375 | devkitctl map: генератор карты проекта и индекса решений, сторож свежести, импорт в тонкий файл | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-375.md](tasks/archive/2026/DK-375.md) |
| DK-376 | POC-замер карты на xr-proxy: пара типовых задач с картой и без, цифры расхода | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-376.md](tasks/archive/2026/DK-376.md) |
| DK-297 | Цель: сессия начинает с готовой карты проекта, а не собирает её грепами | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-297.md](tasks/archive/2026/DK-297.md) |
| DK-166 | Цель: полный прогон тестов укладывается в две минуты | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-166.md](tasks/archive/2026/DK-166.md) |
| DK-383 | Приоритет груминга накопителя: три уровня и сортировка draft list | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-383.md](tasks/archive/2026/DK-383.md), `ccd791f1`, `cd842928`, `a7cf7a43`, `c987c97e`, `6c3a751b`, `3b9bdac2`, `84869cc1`, `73e84123`, `d0f55e41` |
| DK-206 | Цель: чтение файлов перестаёт съедать контекст впустую | task | P2 | 2026-08-16 | [tasks/archive/2026/DK-206.md](tasks/archive/2026/DK-206.md) |
| DK-329 | taskctl add с --link и не агентской приёмкой затирает текст черновика | bug | P2 | 2026-08-16 | [tasks/archive/2026/DK-329.md](tasks/archive/2026/DK-329.md), `9838fe8f`, `cf2a16b2`, `2650d2f0`, `c6edd78b` |
| DK-133 | LLD: единые ворота заведения строки доски при любом входе [приёмка: user] | LLD | P2 | 2026-08-17 | [tasks/archive/2026/DK-133.md](tasks/archive/2026/DK-133.md) |
| DK-389 | Строка доски получает файл задачи с DoD при заведении: абзац в п. 5 правил доски | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-389.md](tasks/archive/2026/DK-389.md) |
| DK-390 | board-groom держит полное описание трёх ворот, прочие входы ссылаются туда | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-390.md](tasks/archive/2026/DK-390.md) |
| DK-392 | goal-loop и goal-cut: ворота заменяются связью с целью, ссылка на цель уходит в файл задачи нарезки | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-392.md](tasks/archive/2026/DK-392.md) |
| DK-393 | goal-start: ворота постановки заменяются DoD цели и бюджетом | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-393.md](tasks/archive/2026/DK-393.md) |
| DK-394 | taskctl add заводит файл задачи с пустым DoD, изменяющие команды без файла отказывают | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-394.md](tasks/archive/2026/DK-394.md) |
| DK-391 | board-task: что идёт строкой сразу и что в черновик, файл задачи при заведении, а не при взятии в работу | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-391.md](tasks/archive/2026/DK-391.md) |
| DK-388 | Цель: единые ворота заведения строки доски при любом входе | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-388.md](tasks/archive/2026/DK-388.md) |
| DK-400 | LLD: парковка вопроса, рубеж, планировщик слота, полка фона и потолки [приёмка: user] | LLD | P1 | 2026-08-17 | [tasks/archive/2026/DK-400.md](tasks/archive/2026/DK-400.md) |
| DK-401 | Рубеж задачи машинным состоянием, переживающим смену статуса | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-401.md](tasks/archive/2026/DK-401.md) |
| DK-406 | Двухступенчатый Check: агентский прогон после выката освобождает очередь | task | P1 | 2026-08-17 | [tasks/archive/2026/DK-406.md](tasks/archive/2026/DK-406.md) |
| DK-407 | Карта переходов работы плоскими строками со сторожем полноты | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-407.md](tasks/archive/2026/DK-407.md) |
| DK-345 | почта любой живой сессии, а не только витка цели | task | P1 | 2026-08-17 | [tasks/archive/2026/DK-345.md](tasks/archive/2026/DK-345.md) |
| DK-288 | shipctl записывает коммит в задачу по упоминанию её ID в чужом subject, merge встаёт | bug | P1 | 2026-08-17 | [tasks/archive/2026/DK-288.md](tasks/archive/2026/DK-288.md) |
| DK-419 | одноразовая строка сценария DK-402 | task | P3 | 2026-08-17 | [tasks/archive/2026/DK-419.md](tasks/archive/2026/DK-419.md) |
| DK-402 | Парковка задачи по вопросу и пробуждение тиком watch | task | P1 | 2026-08-17 | [tasks/archive/2026/DK-402.md](tasks/archive/2026/DK-402.md) |
| DK-403 | Отвязка wait-human от стопа цикла цели | task | P1 | 2026-08-17 | [tasks/archive/2026/DK-403.md](tasks/archive/2026/DK-403.md) |
| DK-404 | Планировщик слота: ворота, счёт внутри полосы, разбор отказа | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-404.md](tasks/archive/2026/DK-404.md) |
| DK-420 | Дока запускатора окна второй подписки: порядок первого запуска с нуля | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-420.md](tasks/archive/2026/DK-420.md) |
| DK-405 | Полка фона и потолки незакрытых деревьев и висящих вопросов | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-405.md](tasks/archive/2026/DK-405.md) |
| DK-421 | Правила доки фичи: вход для читателя, впервые её видящего, и проверка чужим контекстом | task | P2 | 2026-08-17 | [tasks/archive/2026/DK-421.md](tasks/archive/2026/DK-421.md) |
| DK-429 | Калибровка ранга по следу выборов: якоря devkit, бонус за цену, наследование по цели, пересчёт доски [приёмка: user] | task | P2 | 2026-08-18 | [tasks/archive/2026/DK-429.md](tasks/archive/2026/DK-429.md) |
| DK-443 | Рабочее состояние целей в .devkit не гитигнорнуто и висит в статусе | bug | P2 | 2026-08-18 | [tasks/archive/2026/DK-443.md](tasks/archive/2026/DK-443.md), `837be8c3` |
| DK-330 | Подсказка taskctl add и set не называет --accept и --barrier | bug | P2 | 2026-08-18 | [tasks/archive/2026/DK-330.md](tasks/archive/2026/DK-330.md) |
| DK-440 | Словарь канала с почты на разговор: .devkit/chat, hooks/chat-in.py, имена в коде и слова доки | task | P3 | 2026-08-18 | [tasks/archive/2026/DK-440.md](tasks/archive/2026/DK-440.md), `20586879` |
| DK-413 | trackctl: адаптер Jira ходит только в API v3, на v2 не работает | task | P2 | 2026-08-18 | [tasks/archive/2026/DK-413.md](tasks/archive/2026/DK-413.md) |
| DK-444 | Форма файла задачи и черновика: страница формы, болванка и сторож первой строки [приёмка: mixed] | task | P1 | 2026-08-18 | [tasks/archive/2026/DK-444.md](tasks/archive/2026/DK-444.md), `714eb6d5`, `e2d1089a`, `b879b89a`, `a22858b8`, `447a45ae`, `c705b2b4`, `8c626d9d`, `f07b3f55`, `d63b7c40`, `171f2259`, `8cceda2b`, `ecb01dc3`, `0a0911e1`, `1cfccf9e`, `7e5409cf`, `3f82c335`, `d2455bd1`, `a03161a6`, `682bec9c`, `1d4056c6`, `70cf51bc`, `2e43e260`, `4978610e`, `b5d3de16`, `422fd4c8`, `49b187ce`, `9443c0e8`, `44042a3a`, `d9157725` |
| DK-430 | LLD: реестр чатов задачи, адрес реплики, инструмент ожидания и панель чата [приёмка: user] | LLD | P1 | 2026-08-18 | [tasks/archive/2026/DK-430.md](tasks/archive/2026/DK-430.md) |
| DK-395 | Цель: вопрос человеку паркует задачу, а не конвейер | task | P1 | 2026-08-18 | [tasks/archive/2026/DK-395.md](tasks/archive/2026/DK-395.md) |
| DK-431 | Реестр чатов задачи: журнал сессий, хук старта, подпись источника | task | P1 | 2026-08-18 | [tasks/archive/2026/DK-431.md](tasks/archive/2026/DK-431.md) |
| DK-371 | Лента чата выносится в куски с параметрами, цель остаётся на них | task | P2 | 2026-08-18 | [tasks/archive/2026/DK-371.md](tasks/archive/2026/DK-371.md) |
| DK-414 | trackctl: команда правки тикета, оценка, исполнитель, статус, комментарий и поля | task | P2 | 2026-08-18 | [tasks/archive/2026/DK-414.md](tasks/archive/2026/DK-414.md), `06014517`, `5e74f897`, `adbfb78d`, `cf97eb02` |
| DK-451 | Цикл цели в чате встаёт отчётом витка: скилл не называет границу хода | bug | P1 | 2026-08-19 | [tasks/archive/2026/DK-451.md](tasks/archive/2026/DK-451.md) |
| DK-114 | Препятствие в devkit доезжает до live-core с яруса исполнителей | task | P1 | 2026-08-19 | [tasks/archive/2026/DK-114.md](tasks/archive/2026/DK-114.md) |
| DK-438 | Разговор с задачей: правило адресности, выбор дерева, ручка реплики и фильтр адресата у сторожка | task | P1 | 2026-08-19 | [tasks/archive/2026/DK-438.md](tasks/archive/2026/DK-438.md) |
| DK-432 | Инструмент ожидания у задачи: вопрос задаётся из захода, а не последней репликой | task | P2 | 2026-08-19 | [tasks/archive/2026/DK-432.md](tasks/archive/2026/DK-432.md) |
| DK-433 | Задача, ждущая человека, видна в API, в ленте и уведомлением | task | P2 | 2026-08-19 | [tasks/archive/2026/DK-433.md](tasks/archive/2026/DK-433.md) |
| DK-043 | профиль Codex: детект, вклейка правил, headless-делегирование | task | P3 | 2026-08-20 | [tasks/archive/2026/DK-043.md](tasks/archive/2026/DK-043.md) |
| DK-044 | профиль OpenCode: детект, instructions, определения агентов | task | P3 | 2026-08-20 | [tasks/archive/2026/DK-044.md](tasks/archive/2026/DK-044.md) |
| DK-045 | профиль Gemini CLI: детект, контекстный файл, headless-делегирование | task | P3 | 2026-08-20 | [tasks/archive/2026/DK-045.md](tasks/archive/2026/DK-045.md) |
| DK-046 | профиль Cursor: детект, правила в .cursor/rules либо AGENTS.md | task | P3 | 2026-08-20 | [tasks/archive/2026/DK-046.md](tasks/archive/2026/DK-046.md) |
| DK-460 | LLD: в корп-контуре ветка доходит до пуша только через ревью [приёмка: user] | LLD | P1 | 2026-08-22 | [tasks/archive/2026/DK-460.md](tasks/archive/2026/DK-460.md) |
| DK-470 | LLD открывается со строки задачи в show и дашборде [режим: poc] [приёмка: user] | task | P1 | 2026-08-23 | [tasks/archive/2026/DK-470.md](tasks/archive/2026/DK-470.md) |
| DK-269 | Отставание бокового дерева от main не видно и чинится руками | task | P1 | 2026-08-23 | [tasks/archive/2026/DK-269.md](tasks/archive/2026/DK-269.md) |
| DK-448 | Скилл prompt-test: проверка промптов стендом obeycheck и подсказка ворот при промптах в диффе | task | P1 | 2026-08-24 | [tasks/archive/2026/DK-448.md](tasks/archive/2026/DK-448.md) |
| DK-434 | Непрерывная лента чата: подгрузка вверх без кнопки «раньше», позиция держится [приёмка: mixed] | task | P2 | 2026-08-24 | [tasks/archive/2026/DK-434.md](tasks/archive/2026/DK-434.md) |
| DK-435 | Панель чата справа: «Живой статус» сливается с «Чатом», tmux уходит, узкий экран тем же адресом [приёмка: mixed] | task | P1 | 2026-08-24 | [tasks/archive/2026/DK-435.md](tasks/archive/2026/DK-435.md) |
| DK-436 | Вкладка чатов задачи и новый чат с дашборда поверх живой сессии [приёмка: mixed] | task | P1 | 2026-08-24 | [tasks/archive/2026/DK-436.md](tasks/archive/2026/DK-436.md) |
| DK-367 | go test ./... из toplevel не раскрывается в модули tools, merge --test и regcheck падают | bug | P2 | 2026-08-24 | [tasks/archive/2026/DK-367.md](tasks/archive/2026/DK-367.md) |
| DK-172 | Живая сессия переживает обрыв связи: возобновление без ручного ввода | task | P1 | 2026-08-24 | [tasks/archive/2026/DK-172.md](tasks/archive/2026/DK-172.md) |
| DK-181 | Съёмщик остатка второй подписки: quota --harness | task | P2 | 2026-08-24 | [tasks/archive/2026/DK-181.md](tasks/archive/2026/DK-181.md) |
| DK-357 | shipctl: TestCodeOpensWindow красный из сессии второй подписки, тест наследует CLAUDE_CONFIG_DIR | bug | P2 | 2026-08-24 | [tasks/archive/2026/DK-357.md](tasks/archive/2026/DK-357.md), `2225d859` |
| DK-506 | taskctl elapsed: минуты открытого этапа «разработка» против лимита жизненного цикла | task | P2 | 2026-08-24 | [tasks/archive/2026/DK-506.md](tasks/archive/2026/DK-506.md) |
| DK-459 | LLD: режим POC для работы с высокой неопределённостью результата [приёмка: user] | LLD | P1 | 2026-08-24 | [tasks/archive/2026/DK-459.md](tasks/archive/2026/DK-459.md) |
| DK-512 | dashboard: память обхода корней не держит цепочку экрана, main красный | bug | P1 | 2026-08-24 | [tasks/archive/2026/DK-512.md](tasks/archive/2026/DK-512.md), `ff7a4ae7` |
| DK-503 | LLD: лимит жизненного цикла агента и передача хвоста работы [приёмка: user] | LLD | P2 | 2026-08-25 | [tasks/archive/2026/DK-503.md](tasks/archive/2026/DK-503.md) |
| DK-176 | taskctl add и set молча кладут в строку битую ссылку на путь от корня репозитория | bug | P2 | 2026-08-25 | [tasks/archive/2026/DK-176.md](tasks/archive/2026/DK-176.md), `a3cd642c`, `a66102eb`, `f0710f3d` |
| DK-331 | Раскладка скиллов везёт один SKILL.md, вычитка на машине без пар и словаря | bug | P2 | 2026-08-25 | [tasks/archive/2026/DK-331.md](tasks/archive/2026/DK-331.md) |
| DK-481 | dashboard: живой демон не видит токен после secret --rotate | bug | P2 | 2026-08-25 | [tasks/archive/2026/DK-481.md](tasks/archive/2026/DK-481.md) |
| DK-311 | shipctl ship --drain: идемпотентный разлив поезда, не падает на пустом и занятом | task | P2 | 2026-08-25 | [tasks/archive/2026/DK-311.md](tasks/archive/2026/DK-311.md) |
| DK-463 | doctor --fix в дереве задачи заводит болванку deploy.local и сам её находит | bug | P2 | 2026-08-25 | [tasks/archive/2026/DK-463.md](tasks/archive/2026/DK-463.md) |
| DK-514 | taskctl review: замечание с цитатой слова «исправлено» закрывается само | bug | P2 | 2026-08-25 | [tasks/archive/2026/DK-514.md](tasks/archive/2026/DK-514.md), `51c506b1`, `cde635bd`, `6c095574` |
| DK-520 | Метка уровня разбора обязательна при записи черновика | task | P2 | 2026-08-25 | [tasks/archive/2026/DK-520.md](tasks/archive/2026/DK-520.md) |
| DK-521 | Сторож прозы check-prose.py: метрики замера, пороги конфигом, хук записи | task | P2 | 2026-08-25 | [tasks/archive/2026/DK-521.md](tasks/archive/2026/DK-521.md) |
| DK-526 | Мера прозы: десять новых текстов проходят пороги check-prose.py | task | P2 | 2026-08-25 | [tasks/archive/2026/DK-526.md](tasks/archive/2026/DK-526.md) |
| DK-533 | Сторож прозы: хвост не ловит защищённую форму, пороги без RULES v1 | task | P2 | 2026-08-25 | [tasks/archive/2026/DK-533.md](tasks/archive/2026/DK-533.md) |
| DK-313 | devkitctl watch зовёт ship --drain на тике, board-batch и нештат в RULES.board.md | task | P2 | 2026-08-26 | [tasks/archive/2026/DK-313.md](tasks/archive/2026/DK-313.md) |
| DK-522 | Корпус эталонов прозы: фрагменты из журналов и трекеров, словарь, жанры [приёмка: mixed] | task | P2 | 2026-08-27 | [tasks/archive/2026/DK-522.md](tasks/archive/2026/DK-522.md) |
| DK-525 | Мимикрия в правилах шлёт к корпусу эталонов, а не к текстам проекта | task | P2 | 2026-08-27 | [tasks/archive/2026/DK-525.md](tasks/archive/2026/DK-525.md) |
| DK-523 | Скилл prose подмешивает эталоны в момент письма, зовут четыре точки | task | P2 | 2026-08-27 | [tasks/archive/2026/DK-523.md](tasks/archive/2026/DK-523.md) |
| DK-545 | Парсер obeycheck принимает за секцию заголовок внутри блока кода | bug | P1 | 2026-08-27 | [tasks/archive/2026/DK-545.md](tasks/archive/2026/DK-545.md) |
| DK-546 | Стенд кладёт скиллы в дом прогона, сценарии про них перестают молчать | bug | P1 | 2026-08-27 | [tasks/archive/2026/DK-546.md](tasks/archive/2026/DK-546.md) |
| DK-547 | Проверки сценариев стенда краснеют от переноса строк, а не от поведения агента | bug | P1 | 2026-08-27 | [tasks/archive/2026/DK-547.md](tasks/archive/2026/DK-547.md) |
| DK-524 | proofread перекалиброван по замеру: пять пунктов остатка, старые убраны | task | P2 | 2026-08-27 | [tasks/archive/2026/DK-524.md](tasks/archive/2026/DK-524.md) |
| DK-530 | Стенд obeycheck авторизуется сам и ловит негодную затравку до раскладки | bug | P2 | 2026-08-27 | [tasks/archive/2026/DK-530.md](tasks/archive/2026/DK-530.md) |
| DK-548 | Вычитка зовётся не всегда: правило про proofread не срабатывает в живой сессии | task | P1 | 2026-08-27 | [tasks/archive/2026/DK-548.md](tasks/archive/2026/DK-548.md) |
| DK-550 | Сторож прозы считает машинные записи файла задачи наравне с прозой | bug | P2 | 2026-08-28 | [tasks/archive/2026/DK-550.md](tasks/archive/2026/DK-550.md) |
| DK-446 | Цель: долгоживущие тексты пишутся живой прозой с первого раза | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-446.md](tasks/archive/2026/DK-446.md) |
| DK-372 | Лента черновика: LLD DK-354 сводится с чат-контуром, склейка под тестом | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-372.md](tasks/archive/2026/DK-372.md) |
| DK-540 | LLD DK-430 сводится с чат-контуром POC: носитель цели и словарь канала | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-540.md](tasks/archive/2026/DK-540.md) |
| DK-373 | Живой вопрос грумера: вход разговора, draft ask, ручка реплики, блок пачки, скилл | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-373.md](tasks/archive/2026/DK-373.md) |
| DK-374 | Реплика в работающий заход: подхват получает адрес черновика | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-374.md](tasks/archive/2026/DK-374.md) |
| DK-353 | dashboard: черновики свежими вниз, выбора сортировки нет | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-353.md](tasks/archive/2026/DK-353.md) |
| DK-370 | Форма записи: кнопки «Сохранить» и «Сохранить и грумить», промежуточный экран уходит | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-370.md](tasks/archive/2026/DK-370.md) |
| DK-411 | dashboard: экран задачи и колонка проектов пересобираются на каждый фокус окна | bug | P2 | 2026-08-28 | [tasks/archive/2026/DK-411.md](tasks/archive/2026/DK-411.md) |
| DK-539 | Отметка работы по факту правки не доезжает: --touch нет в шаблонах раскладки | bug | P2 | 2026-08-28 | [tasks/archive/2026/DK-539.md](tasks/archive/2026/DK-539.md) |
| DK-549 | Поднятая дашбордом сессия берёт утилиты кита из каталога экземпляра | bug | P1 | 2026-08-28 | [tasks/archive/2026/DK-549.md](tasks/archive/2026/DK-549.md) |
| DK-486 | Интерактивный вопрос клиента виден в чате дашборда и отвечается оттуда [приёмка: mixed] | task | P1 | 2026-08-28 | [tasks/archive/2026/DK-486.md](tasks/archive/2026/DK-486.md) |
| DK-369 | Правка текста черновика: ручка PUT с замком разбора и редактор экрана записи | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-369.md](tasks/archive/2026/DK-369.md) |
| DK-168 | Связь из груминга обязательна: вход без маркера «после» это находка doctor | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-168.md](tasks/archive/2026/DK-168.md) |
| DK-519 | Сессия не узнаёт о завершении фонового субагента | bug | P1 | 2026-08-28 | [tasks/archive/2026/DK-519.md](tasks/archive/2026/DK-519.md) |
| DK-312 | taskctl close зовёт ship --drain после закрытия задачи | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-312.md](tasks/archive/2026/DK-312.md) |
| DK-516 | Тик watch закрывает вид agent из Check, status называет ждущих человека | task | P2 | 2026-08-28 | [tasks/archive/2026/DK-516.md](tasks/archive/2026/DK-516.md) |
| DK-574 | Снимок квоты не встаёт, а отказ не говорит, что делать | bug | P2 | 2026-08-29 | [tasks/archive/2026/DK-574.md](tasks/archive/2026/DK-574.md) |
| DK-366 | Вычитка пропускает блатную лексику: класс типологии, пары и корпус | task | P2 | 2026-08-29 | [tasks/archive/2026/DK-366.md](tasks/archive/2026/DK-366.md) |
| DK-584 | Снимок квоты встаёт без разбивки по моделям и молчит об этом | bug | P2 | 2026-08-29 | [tasks/archive/2026/DK-584.md](tasks/archive/2026/DK-584.md) |
| DK-606 | LLD: приёмы прозы по типу страницы, разметка и счёт переживают правку скиллом prose [приёмка: user] | LLD | P2 | 2026-08-30 | [tasks/archive/2026/DK-606.md](tasks/archive/2026/DK-606.md), `fca4db66`, `5ae964e4`, `ec892c39`, `f8369ffb` |
| DK-466 | Дашборд: истёкший логин чата виден состоянием и чинится перезапуском [приёмка: mixed] | task | P1 | 2026-08-30 | [tasks/archive/2026/DK-466.md](tasks/archive/2026/DK-466.md) |
| DK-577 | Вход в клиент проходит с телефона: ссылка в чате, код отдельным полем [приёмка: mixed] | task | P2 | 2026-08-30 | [tasks/archive/2026/DK-577.md](tasks/archive/2026/DK-577.md) |
| DK-581 | Дашборд: ходы работы второй подписки видны в раздавшем разговоре [приёмка: mixed] | task | P1 | 2026-08-30 | [tasks/archive/2026/DK-581.md](tasks/archive/2026/DK-581.md) |
| DK-583 | Корп-контур одним окном: ссылка .devkit в клоне, боковая директория на контур [приёмка: mixed] | task | P2 | 2026-08-30 | [tasks/archive/2026/DK-583.md](tasks/archive/2026/DK-583.md) |
| DK-605 | Снимок квоты читает кеш клиента, а не разбирает панель | task | P2 | 2026-08-30 | [tasks/archive/2026/DK-605.md](tasks/archive/2026/DK-605.md) |
| DK-397 | Цель: полноценная работа с любыми задачами на дашборде [приёмка: mixed] | task | P1 | 2026-08-31 | [tasks/archive/2026/DK-397.md](tasks/archive/2026/DK-397.md) |
| DK-582 | Хуки на машине зовутся из дерева ветки, а не из main | bug | P1 | 2026-08-31 | [tasks/archive/2026/DK-582.md](tasks/archive/2026/DK-582.md) |
| DK-428 | taskctl: поправки к рангу (бонус за цену, наследование по цели и по «после») считаются из строки | task | P2 | 2026-08-31 | [tasks/archive/2026/DK-428.md](tasks/archive/2026/DK-428.md) |
| DK-637 | shipctl: ветка, вобравшая main слиянием, сливается без ребейза | bug | P1 | 2026-08-31 | [tasks/archive/2026/DK-637.md](tasks/archive/2026/DK-637.md) |
| DK-617 | prose: приёмы страницы по режиму чтения, раздел про правку готового текста и перенос из другого формата | task | P2 | 2026-08-31 | [tasks/archive/2026/DK-617.md](tasks/archive/2026/DK-617.md) |
| DK-452 | Текст замечания ревью уезжает в bash и исполняется подстановкой | bug | P1 | 2026-08-31 | [tasks/archive/2026/DK-452.md](tasks/archive/2026/DK-452.md) |
| DK-599 | Слияние не доставляет правку до бинарей в PATH | task | P1 | 2026-08-31 | [tasks/archive/2026/DK-599.md](tasks/archive/2026/DK-599.md) |
| DK-469 | Слово исхода ищется по всему тексту замечания: «без замечаний» внутри сути даёт чистый вердикт | bug | P1 | 2026-08-31 | [tasks/archive/2026/DK-469.md](tasks/archive/2026/DK-469.md) |
| DK-480 | Чат дашборда доносит реплику до сессии вводом человека, а не соседом | task | P1 | 2026-08-31 | [tasks/archive/2026/DK-480.md](tasks/archive/2026/DK-480.md) |
| DK-666 | Ярус mini второй подписки зовёт glm-5.3-flash вместо ушедшей glm-4.7 | task | P2 | 2026-08-31 | [tasks/archive/2026/DK-666.md](tasks/archive/2026/DK-666.md) |
| DK-633 | Снимок квоты обеих подписок обновляется сам, а не застывает на часы [приёмка: mixed] | bug | P1 | 2026-08-31 | [tasks/archive/2026/DK-633.md](tasks/archive/2026/DK-633.md) |
| DK-649 | Полный прогон тестов dashboard виснет на TestChatStopDropEndsLiveSession | bug | P0 | 2026-08-31 | [tasks/archive/2026/DK-649.md](tasks/archive/2026/DK-649.md) |
| DK-652 | Вопрос агента в чате задачи: виджет с вариантами вместо подсказки [приёмка: mixed] | bug | P1 | 2026-08-31 | [tasks/archive/2026/DK-652.md](tasks/archive/2026/DK-652.md), `a113c76c`, `d2c768b9`, `b4138006`, `ed320a59`, `05c8f890`, `eac18473` |
| DK-644 | Журнал цели несёт время витка, итог называет, где время ушло | task | P1 | 2026-08-31 | [tasks/archive/2026/DK-644.md](tasks/archive/2026/DK-644.md) |
| DK-641 | Слияние гоняет тесты в свежем дереве и чистом окружении | task | P1 | 2026-08-31 | [tasks/archive/2026/DK-641.md](tasks/archive/2026/DK-641.md) |
| DK-642 | Сценарий проверки прогоняет не автор правки, прогонявший записан машинно | task | P1 | 2026-08-31 | [tasks/archive/2026/DK-642.md](tasks/archive/2026/DK-642.md) |
| DK-677 | Тесты notify в hooks падают в чистом окружении прогона merge | bug | P0 | 2026-09-01 | [tasks/archive/2026/DK-677.md](tasks/archive/2026/DK-677.md) |
| DK-643 | Сценарий без выката обкатывается в чистом окружении до перевода в Check | task | P1 | 2026-09-01 | [tasks/archive/2026/DK-643.md](tasks/archive/2026/DK-643.md) |
| DK-678 | Рубеж синхронности: headless-сессии отказано в фоновом ходе | task | P1 | 2026-09-01 | [tasks/archive/2026/DK-678.md](tasks/archive/2026/DK-678.md) |
| DK-682 | Сводка tally слепа к архиву и читает чужие ID из прозы раздела | bug | P2 | 2026-09-01 | [tasks/archive/2026/DK-682.md](tasks/archive/2026/DK-682.md) |
| DK-307 | Цикл цели раздаёт исполнителей на вторую подписку из живого окна | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-307.md](tasks/archive/2026/DK-307.md) |
| DK-161 | Цель: цикл цели ловит дефекты до выката, а человека спрашивает до старта | task | P1 | 2026-09-01 | [tasks/archive/2026/DK-161.md](tasks/archive/2026/DK-161.md) |
| DK-679 | Ступень уезжает в обе стороны: claude-code достижим командой снаружи | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-679.md](tasks/archive/2026/DK-679.md) |
| DK-667 | Ворот тестов shipctl не знает раскладку Cargo и Gradle: интеграционный тест в tests/ и юнит в src/test невидимы, и слияние Rust-задачи с честными тестами отбивается с предложением пометить правку как бестестовую | bug | P1 | 2026-09-01 | [tasks/archive/2026/DK-667.md](tasks/archive/2026/DK-667.md) |
| DK-661 | Полоса base забирает задачи ценой M, а просадку ловят счётчики | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-661.md](tasks/archive/2026/DK-661.md), `d572696b`, `bc7bbeff`, `5c727692`, `d21cb8d8`, `18cf67b8`, `460482fb` |
| DK-471 | Чистый вердикт ревью записывается командой taskctl review | task | P1 | 2026-09-01 | [tasks/archive/2026/DK-471.md](tasks/archive/2026/DK-471.md) |
| DK-658 | Разбор в RULES.md называет, чему в комментарии не место | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-658.md](tasks/archive/2026/DK-658.md) |
| DK-655 | Тексты в корп-гит, трекер и вики без специфики devkit | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-655.md](tasks/archive/2026/DK-655.md) |
| DK-693 | Лента: реплика стоп-хука ложится портянкой текста | bug | P2 | 2026-09-01 | [tasks/archive/2026/DK-693.md](tasks/archive/2026/DK-693.md) |
| DK-656 | Список чатов: текущий сверху, порядок замер, уборка переключает панель [приёмка: mixed] | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-656.md](tasks/archive/2026/DK-656.md) |
| DK-702 | Чат: единая шапка свёрнутых блоков ленты и слова групп списка [приёмка: mixed] | task | P2 | 2026-09-01 | [tasks/archive/2026/DK-702.md](tasks/archive/2026/DK-702.md) |
| DK-691 | Конвейер задачи умирает на ожидании и прибивает фоновую работу | bug | P1 | 2026-09-02 | [tasks/archive/2026/DK-691.md](tasks/archive/2026/DK-691.md) |
| DK-697 | git push доски виснет на osxkeychain, коммиты копятся локально | bug | P1 | 2026-09-02 | [tasks/archive/2026/DK-697.md](tasks/archive/2026/DK-697.md) |
| DK-608 | Сторож повторного чтения отбивает субагенту первое чтение файла | bug | P1 | 2026-09-02 | [tasks/archive/2026/DK-608.md](tasks/archive/2026/DK-608.md) |
| DK-602 | Мелочь в main без пуша запирает пуш доски всем сессиям | task | P1 | 2026-09-02 | [tasks/archive/2026/DK-602.md](tasks/archive/2026/DK-602.md) |
| DK-684 | Чистый прогон обкатки и слияния не видит утилит devkit и тулчейнов | bug | P1 | 2026-09-02 | [tasks/archive/2026/DK-684.md](tasks/archive/2026/DK-684.md) |
| DK-527 | Планы параллельных субагентов пачки пишутся в один файл и трутся | bug | P2 | 2026-09-02 | [tasks/archive/2026/DK-527.md](tasks/archive/2026/DK-527.md) |
| DK-704 | Род первого лица агента задаёт настройка пользователя | task | P2 | 2026-09-02 | [tasks/archive/2026/DK-704.md](tasks/archive/2026/DK-704.md) |
| DK-711 | Кольцо хода работ в разговоре замирает до обновления страницы | bug | P1 | 2026-09-02 | [tasks/archive/2026/DK-711.md](tasks/archive/2026/DK-711.md) |
| DK-720 | Слияние и отметка выката не спотыкаются о чужое незакоммиченное | task | P1 | 2026-09-02 | [tasks/archive/2026/DK-720.md](tasks/archive/2026/DK-720.md) |
| DK-718 | Выкат без человека сам поднимает прогон сценария и доводит до Done | task | P1 | 2026-09-02 | [tasks/archive/2026/DK-718.md](tasks/archive/2026/DK-718.md) |
| DK-673 | Панель подаёт реплику в чужую tmux-сессию, а уборка её снимает | bug | P1 | 2026-09-02 | [tasks/archive/2026/DK-673.md](tasks/archive/2026/DK-673.md) |
| DK-728 | Молчаливая смерть поднятой сессии видна в ленте разговора | bug | P1 | 2026-09-03 | [tasks/archive/2026/DK-728.md](tasks/archive/2026/DK-728.md) |
| DK-726 | Поиск чата достаёт разговор из архива и из-за окна списка | task | P1 | 2026-09-03 | [tasks/archive/2026/DK-726.md](tasks/archive/2026/DK-726.md) |
| DK-733 | Тесты группировки чатов падают от времени суток, а не от кода | bug | P1 | 2026-09-03 | [tasks/archive/2026/DK-733.md](tasks/archive/2026/DK-733.md) |
| DK-660 | Уборка чата груминга снимает чужую tmux-сессию, а осиротевший чат молчит | bug | P1 | 2026-09-03 | [tasks/archive/2026/DK-660.md](tasks/archive/2026/DK-660.md) |
| DK-727 | Кончившийся разговор без задачи принимает реплику и оживает резюмом | task | P1 | 2026-09-03 | [tasks/archive/2026/DK-727.md](tasks/archive/2026/DK-727.md) |
