#!/usr/bin/env python3
"""devkitctl: обвязка devkit в проекте одной командой.

  devkitctl new --prefix XX [--name "..."] [--no-board] [-C dir]
      подключить проект: AGENTS.md из шаблона, тонкие файлы правил под
      включённые харнесы, git-хуки, доска через taskctl init, болванка
      .devkit/deploy.local для shipctl; --no-board для внешнего трекера

  devkitctl corp [--prefix XX] [--name "..."] [--local dir] [-C dir]
      подключить корп-проект: первый прогон спрашивает недостающее сам (префикс
      доски с предложением по имени клона, адрес трекера и пользователя нового
      контура компании), без tty вопросов нет и недоделанное едет в хвост;
      боковая директория со скелетом доски (по умолчанию общая на контур
      ../<контур>-local/<проект>, репозиторий один на всю директорию контура),
      ссылка .devkit из клона на неё, редирект git config devkit.local, тонкий
      файл контекста харнеса с импортом на AGENTS.md боковой директории и
      строкой в .git/info/exclude, обёртки хуков на pre-commit и commit-msg.
      Прогон повторяемый: боковая директория не трогается, доводится обвязка
      клона, поэтому он же восстанавливает обвязку после переклонирования

  devkitctl doctor [--fix] [-C dir]
      проверить обвязку проекта; корень не под git это находка, а не отказ:
      проектная половина (файлы правил, git-хуки, доска, обвязка выката,
      markdown-ссылки) по дереву вниз не идёт, машинный контур ниже
      печатается как обычно (DK-160). AGENTS.md на месте, генерённые файлы
      правил свежи и не правлены руками, их импорты разворачиваются, git-хуки
      подключены, инварианты доски (taskctl lint), обвязка выката
      (.devkit/deploy.local есть, с командой и гитигнорнута; рядом с чужим
      go.work, где проект не перечислен, го-команды в ней обёрнуты GOWORK=off;
      в линкованном дереве задачи, worktree от shipctl start, конфиг выката не
      заводится и не разбирается: он гитигнорнут и логически принадлежит
      основному чекауту, DK-463),
      локальные markdown-ссылки не битые; в корп-контуре (задан devkit.local) рабочие
      файлы берутся из боковой директории, а сверх обычных проверок идут
      корп-: чистота корп-индекса, exclude-строка, цепочки на обоих хуках,
      remote боковой директории и свежесть sync, и прогнанный в боковой
      директории доктор идёт по ключу repo привязки и проверяет обвязку
      названного клона; и машинный контур: PostToolUse-хуки,
      SessionStart-хук освежения квоты, хуки уведомлений вместе с бэкендом,
      которым их слать, права машинного контура в permissions.allow (без них
      сессия без человека встаёт на первом отказе), бинари утилит devkit в
      PATH и на одной версии с чекаутом (список из дерева tools/*/go.mod,
      коммит со слов --version против последней правки кода утилиты, а не
      против HEAD, рядом называется режим чекаута),
      вышедший релиз двумя находками (тег чекаута против новейшего в клоне и
      давность похода за тегами, обе по локальному клону, без сети), обёртка
      devkitctl рядом с ними и сам каталог назначения в PATH,
      определения агентов ([delegate] agents_dir),
      скиллы devkit ([skills] dir), глобальная точка правил ([rules]
      global_file) свежа и не правлена руками, и всё это по разу на каждый
      включённый харнес, каждому по его профилю,
      tmux и снимки квоты в ~/.devkit/quota (снимка нет, а хук SessionStart и
      tmux на месте, значит находки нет: снимок появится сам первой сессией),
      носитель сторожка цикла цели (launchd-агент ru.devkit.goal-watch положен,
      показывает на основной чекаут, поднят и оставляет свежий след в
      ~/.devkit/goal-watch.log);
      носитель дашборда (launchd-агент ru.devkit.dashboard держит dashboard
      serve с KeepAlive, показывает на бинарь из PATH, /healthz отвечает и не
      несёт ошибок конфига);
      профили харнесов devkit/kit/harness
      прогоняются через тот же валидатор, каким их читает agentctl; вес
      резидента по карманам печатается всегда, а под порогом в чекауте devkit
      его собственные карманы (ядро, ядро доски, общий потолок токенов) и тело
      скилла, с предложением резать его надвое (DK-029), в подключённом проекте
      проектная часть, AGENTS.md со своим тонким файлом (DK-190); импорты файла
      правил проекта проверяются на полный текст вместо ядра и на путь наружу,
      по которому правила до контекста не доезжают;
      Чекаут devkit, стоящий на релизном теге, это машина потребителя, и то,
      что адресовано правящему devkit, там молчит: git-хуки, инварианты доски,
      обвязка выката и вес резидента (DK-163). Содержимое самого выпуска
      (файлы правил, ссылки, профили харнесов) сверяется и там: находка по нему
      это дефект релиза, а не недоделка машины;
      --fix additive доводит обвязку (хуки, болванка deploy.local либо
      недостающие в ней ключи, .gitignore, бинари версии чекаута: на теге
      ставятся бинари релиза, иначе собираются на месте, копия определений
      агентов и скиллов, права машинного контура, глобальная точка правил,
      переезд одиночного снимка квоты в директорию, генерация файлов правил,
      недостающие tmux и terminal-notifier тем пакетным менеджером, который на
      машине уже есть),
      заполненное не трогает, неоднозначное оставляет находкой

  devkitctl update [--pin] [--check]
      поставить или обновить devkit готовыми бинарями релиза, без тулчейна:
      перевести чекаут на новейший тег, скачать тарболл своей платформы,
      сверить SHA256, подменить бинари в каталоге назначения, положить рядом
      обёртку devkitctl и разложить машинный контур. Зовётся из основного
      чекаута; на ветке отказывает и называет обычный путь (git pull и
      doctor --fix), а первая установка разрешает перевод свежего клона на тег
      ключом --pin. --check тянет только релизные теги и только рассказывает:
      на каком теге машина, какой вышел, что поставит обычный update

  devkitctl build [--release] [--out dir]
      собрать бинари devkit с зашитой версией: что собирать, выводится из
      дерева (каталоги tools/*/ с go.mod), версия и коммит из git этого
      чекаута. Без ключей сборка под текущую машину в каталог назначения
      (DEVKIT_BIN, иначе первый из ~/go/bin и ~/.local/bin, который уже стоит
      в PATH, иначе ~/.local/bin) и самопроверка запуском: каждый свежий
      бинарь обязан напечатать по --version ту строку, которую в него
      зашивали, а после укладки каждая утилита сверяется по победителю PATH,
      и чужая копия, заслоняющая свежую сборку, роняет команду (DK-599).
      --release собирает четыре пары GOOS/GOARCH, пакует тарболл на
      пару (devkit-<версия>-<os>-<arch>.tar.gz) и кладёт рядом SHA256SUMS;
      живьём там проверяется пара своей машины, остальные три байтовым
      поиском зашитой строки. Каталог назначения по умолчанию dist

  devkitctl weigh [-C dir] [--runs N] [--limit T] [--model M] [--prompt "..."]
      живой замер веса резидента: два headless-прогона claude -p с одинаковым
      запросом (базовый без раскладки devkit, целевой с ней), разница это цена
      текста devkit в токенах. Рядом расчёт по карманам в символах и токенах и
      расхождение расчёта с замером; код 1, когда замер выше потолка. Прогон
      стоит денег и требует сети, поэтому команда отдельная, а не проверка
      доктора; несвежая раскладка на машине это отказ мерить

  devkitctl stats [--context] [-C dir]
      сводка по журналу запусков .devkit/log: частота команд (утилита, команда),
      доля ошибок, отсортировано по частоте убыванием, в конце итоговая строка
      по всему журналу; битые строки молча пропускаются.
      --context берёт второй источник, журналы сессий харнеса
      (~/.claude/projects/<слепок пути проекта>/*.jsonl), и печатает, куда ушёл
      объём: старт против истории, перезаписи префикса с их ценой, топ тулов по
      объёму результатов; правило подписи перезаписи в context.py

  devkitctl drain [-C dir] [--all]
      регулярный замер расхода контекста по журналам сессий харнеса
      (DK-148): куда уходит вывод инструментов и токены. По умолчанию разбирает
      сессии текущего проекта (тот же слепок пути, что у stats --context), а
      под --all весь ~/.claude/projects, как разовый скрипт tstats.py из файла
      задачи. Печатает вызовы и объём по инструментам, разбор Bash по головной
      команде конвейера, sed (правка файла против чтения фрагмента), чтения
      файлом целиком против куска, повторные чтения внутри сессии,
      перцентили размера одного вывода и хвост самых жирных; разбор jsonl
      общий со stats --context (sessions.py), второй парсер не заводится.
      Выход 2, если журналов сессий нет или в них не нашлось вызовов

  devkitctl watch [--idle <минуты>]
      сторожок цикла цели: обойти реестр целей ~/.devkit/goals и позвать
      громким уведомлением по тем, где движения нет дольше порога. Обычно его
      будит launchd-агент, положенный doctor --fix, а руками команда зовётся
      для проверки; порог по умолчанию в watch.py, перебивается строкой
      idle = <минуты> в ~/.devkit/watch.local и флагом. Выход 1 значит, что
      нашёлся вставший цикл

  devkitctl selfcheck
      живой круг связки после установки, одной командой (DK-158): во временном
      каталоге заводится проект (new), в нём строка доски (taskctl add), вердикт
      pick, ветка задачи в своём дереве (shipctl start), слияние с тестами и
      подставным выкатом (shipctl merge) и закрытие (taskctl close). Отчёт по
      шагам, каждый со своим исходом; провал шага называет шаг и печатает его
      вывод. Настоящие проекты не трогаются, дом у круга свой временный:
      утилиты devkit пишут в ~/.devkit без ведома вызвавшего, поэтому
      сабпроцессы круга получают подменённый HOME. Утилиты зовутся по имени
      из PATH, как их видит человек, круг работает в своём временном каталоге
      и убирает его за собой. Находки доктора по машине это не провал круга:
      доктор тут отдельный шаг, его отчёт печатается, а судится сам шаг

Выход 0 всё в порядке, 1 есть находки, 2 ошибка запуска.
"""
import argparse
import board
import build
import codemap
import context
import corp
import dashboard
import describe
import drain
import entry
import harness
import importlib.util
import json
import leak
import layout
import os
import perms
import re
import rules
import say
import selfcheck
import shutil
import subprocess
import sys
import update
import watch
import weigh
import workflow
from datetime import datetime
from say import human_age
from pathlib import Path

DEVKIT = Path(__file__).resolve().parent.parent.parent
POST_SCRIPTS = ("check-symbols.py", "check-memory.py", "check-sensitive.py",
                "check-prose.py")
# Конфиг порогов сторожа прозы (DK-521). Полноту его смотрит сам хук режимом
# --config: список метрик живёт в коде хука, и второй копии тут не заводится.
PROSE_HOOK = "check-prose.py"
# Рубежи на PreToolUse Bash: чтение секретов (DK-228) и подстановка в
# свободном тексте у утилит devkit (DK-452). Записей на матчере Bash две, и
# сообщение в hook_gaps у каждой своё: неподключённый check-subst это дыра
# инъекции, а не чтение секретов.
PRE_SCRIPTS = ("check-read-secret.py", "check-subst.py")
PRE_GAPS = {
    "check-read-secret.py": "чтение секретов через Bash идёт мимо хука",
    "check-subst.py": "подстановка в текстовом аргументе утилит devkit "
                      "исполняется до их вызова (DK-452)",
}
# Рубежи на PreToolUse Read. Категория отдельная от проверок текстов и от чтения
# секретов через Bash, и сообщение про каждый своё. Записей на одном матчере
# сколько угодно: check-reread режет повторы, check-longfile режет чтение длинного
# файла целиком, и каждая блокирует Read своим выходом 2 со своей подсказкой.
PRE_READ_SCRIPTS = ("check-reread.py", "check-longfile.py")
# Что идёт не так, когда конкретный рубеж на Read не подключён. Доктор говорит
# это находкой, и сообщение обязано отличать один рубеж от другого: «повторные
# чтения не режутся» про отсутствующий check-longfile это ложь.
PRE_READ_GAPS = {
    "check-reread.py": "повторные чтения файлов не режутся, и контекст съедает "
                       "перечитывание уже прочитанного",
    "check-longfile.py": "чтение длинного файла целиком не режется, и контекст "
                         "съедает разовый большой вывод Read",
}
SESSION_HOOK = "quota-refresh.sh"
# Реестр чатов задачи (DK-431): SessionStart на пустом матчере, потому что
# записать «эта сессия ведёт задачу» можно только в момент её рождения, когда ID
# сессии уже есть. Категория сообщения в hook_gaps своя: без записи дашборд
# возвращается к угадыванию по транскрипту, а это не то же самое, что протухший
# снимок квоты. Тот же файл вторым хуком стоит на PostToolUse с флагом --touch
# (DK-539): правка файла в боковом дереве задачи дописывает привязку по факту
# работы, когда утилиты доски не звались вовсе и заказ дашборда взять неоткуда.
# Матчер пустой, как у CHAT_HOOK: событие фильтрует список WORK_TOOLS сам
# скрипт, а матчер Edit|Write|NotebookEdit без MultiEdit срезал бы часть правок
# молча.
TASK_HOOK = "session-task.py"
NOTIFY_HOOK = "notify.py"
# Догон бокового дерева доски (DK-269): тоже SessionStart, потому что чинить
# дерево надо до того, как сессия прочла из него устаревшую доску. Категория
# сообщения в hook_gaps своя: без хука отставание не видно вовсе, а это не то
# же самое, что пустой реестр чатов.
BOARD_HOOK = "board-catchup.sh"
# Подхват реплики (DK-341): PostToolUse на пустом матчере, потому что реплику
# надо доставлять на любом ходе идущего витка, а не на записи файла. Категория
# сообщения в hook_gaps своя: своё событие, свой матчер и своё «что идёт не
# так», иначе неподключённый канал чата остаётся неотличим от штатной тишины.
CHAT_HOOK = "chat-in.py"
# Сторож фоновых субагентов (DK-519): три события, потому что счёт работам
# ведётся с их запуска (PostToolUse на инструменте делегирования), закрывается
# их концом (SubagentStop), а сдаётся сессии на конце хода (Stop), пока она не
# ушла спать с незабранным отчётом. Категория сообщения в hook_gaps своя:
# потерянный отчёт субагента виден иначе, чем молчащий баннер уведомителя.
WATCH_HOOK = "agent-watch.py"
WATCH_EVENTS = ("PostToolUse", "SubagentStop", "Stop")
# Рубеж синхронности (DK-678): PreToolUse на Bash и на инструменте
# делегирования, потому что фоном зовутся оба, и признак фона у обоих лежит во
# входе. Матчер свой, поэтому и категория сообщения в hook_gaps своя: без этого
# рубежа headless-сессия уводит долгое дело в фон, а харнес добивает его через
# десять минут, и это не то же самое, что чтение секретов мимо рубежа.
SYNC_HOOK = "check-background.py"
# Хуки, переименованные в devkit: прежнее имя файла и нынешнее (DK-440). Строка
# с прежним именем зовёт файл, которого в чекауте уже нет, и харнес спотыкается
# на ней каждым ходом, поэтому доктор не дополняет раскладку новой строкой, а
# сперва убирает старую.
RETIRED_HOOKS = {"inbox.py": CHAT_HOOK}
NOTIFY_EVENTS = ("Notification", "Stop", "StopFailure", "SubagentStop", "UserPromptSubmit")
# Ретрай-вотчдог харнеса (DK-172): найден strings бинаря 2.1.241 рядом с
# CLAUDE_CODE_MAX_RETRIES, CLAUDE_ENABLE_STREAM_WATCHDOG и
# CLAUDE_ENABLE_BYTE_WATCHDOG, публичной докой не описан. Разделяющий замер
# стенда (ключ=1, ключ=0, ключа нет вовсе, один и тот же обрыв, подробности в
# docs/tasks/DK-172.md) разницы в поведении не нашёл: харнес ретраит часть
# сбоев сам и без ключа. Кладём его безвредным заделом на случай другой версии
# или платформы, а не как подтверждённое решение проблемы (hooks/README.md,
# «Возобновление после обрыва связи»). Ключ кладётся в секцию env тех же
# настроек, где лежат хуки; значение, вписанное человеком (хоть "0"), доктор
# не трогает: это его решение, а не пробел раскладки.
WATCHDOG_KEY = "CLAUDE_CODE_RETRY_WATCHDOG"
WATCHDOG_VALUE = "1"
NOTIFY_MATCHER = "permission_prompt|agent_needs_input|elicitation_dialog|idle_prompt"
POST_MATCHER = "Edit|Write|NotebookEdit"
PRE_MATCHER = "Bash"
PRE_READ_MATCHER = "Read"
SYNC_MATCHER = "Bash|Agent"
# Раскладка хуков харнеса: событие, матчер, команда с местом под чекаут devkit.
# Тот же перечень нарисован в hooks/README.md, но раскладывает его отсюда
# доктор: список ручных шагов в README это перекладывание раскладки на человека.
HOOK_LAYOUT = (
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-symbols.py --hook"),
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-memory.py --hook"),
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-sensitive.py --hook"),
    ("PostToolUse", POST_MATCHER, "python3 %s/hooks/check-prose.py --hook"),
    ("PreToolUse", PRE_MATCHER, "python3 %s/hooks/check-read-secret.py --hook"),
    ("PreToolUse", PRE_MATCHER, "python3 %s/hooks/check-subst.py --hook"),
    ("PreToolUse", SYNC_MATCHER, "python3 %s/hooks/check-background.py --hook"),
    ("PreToolUse", PRE_READ_MATCHER, "python3 %s/hooks/check-reread.py --hook"),
    ("PreToolUse", PRE_READ_MATCHER, "python3 %s/hooks/check-longfile.py --hook"),
    ("PostToolUse", "", "python3 %s/hooks/chat-in.py --hook claude-code"),
    ("PostToolUse", "Agent", "python3 %s/hooks/agent-watch.py --hook claude-code"),
    ("SubagentStop", "", "python3 %s/hooks/agent-watch.py --hook claude-code"),
    ("Stop", "", "python3 %s/hooks/agent-watch.py --hook claude-code"),
    ("SessionStart", "", "sh %s/hooks/quota-refresh.sh"),
    ("SessionStart", "", "python3 %s/hooks/session-task.py --hook claude-code"),
    ("PostToolUse", "", "python3 %s/hooks/session-task.py --touch claude-code"),
    ("SessionStart", "", "sh %s/hooks/board-catchup.sh"),
    ("Notification", NOTIFY_MATCHER, "python3 %s/hooks/notify.py --hook claude-code"),
    ("Stop", "", "python3 %s/hooks/notify.py --hook claude-code"),
    ("StopFailure", "", "python3 %s/hooks/notify.py --hook claude-code"),
    ("SubagentStop", "", "python3 %s/hooks/notify.py --hook claude-code"),
    ("UserPromptSubmit", "", "python3 %s/hooks/notify.py --hook claude-code"),
)
# Пакетный менеджер, которым доводка ставит недостающее (tmux, terminal-notifier).
# Он один: brew стоит и на macOS, и на linux у тех, кто его туда поставил, а
# системные менеджеры просят sudo, и молча звать их из доводки нельзя.
PACKAGER = "brew"
# Три формы слова для счёта в отчёте о сделанном: один, два, пять.
AGENT_WORD = ("определение агента", "определения агентов", "определений агентов")
SKILL_WORD = ("скилл", "скилла", "скиллов")
HOOK_WORD = ("хук харнеса", "хука харнеса", "хуков харнеса")
# Оси машинного хозяйства харнеса и ключи его профиля, откуда берётся путь:
# название оси, секция, ключ. Констант ~/.claude тут больше нет, вторая
# подписка считает те же пути от своего каталога
# (docs/lld/DK-090-heterogeneous-ladder.md, «Раскладка второй подписки»).
AXIS_HOOKS = ("хуки", "hooks", "config")
AXIS_AGENTS = ("определения агентов", "delegate", "agents_dir")
AXIS_SKILLS = ("скиллы", "skills", "dir")
# Снимок остатка лимитов лежит по файлу на харнес. Одиночный quota.local это
# как было до директории: его переезд делает --fix, читатель до тех пор смотрит
# старый путь (tools/agentctl/quota.go).
QUOTA_DIR = "~/.devkit/quota"
QUOTA_LEGACY = "~/.devkit/quota.local"
# В какой файл директории переезжает старый снимок: снять его было нечем, кроме
# панели Claude Code.
QUOTA_LEGACY_HARNESS = "claude-code"
# Порог свежести снимка держит agentctl (snapshotMaxAge в tools/agentctl/quota.go), тут
# его копия: доктор про снимок говорит то же самое, что pick, иначе одна утилита
# звала бы переснимать, а вторая молчала.
QUOTA_MAX_AGE = 45 * 60
QUOTA_TIME_FORMATS = ("%Y-%m-%dT%H:%M", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%d %H:%M")
# Харнес второй подписки. Каталог его конфига девкит из себя не берёт: он лежит
# в машинном слое ключом home секции [glm-code], и оттуда же его читают
# раскладка хозяйства ({home} в путях профиля), окружение подпроцесса
# делегирования и окно редактора shipctl code. Пути под один каталог в двух
# местах разъезжаются, а разъехавшись, дают самую дорогую поломку из возможных,
# сессию на дорогой подписке, считающую себя дешёвой.
ALT_SUB_HARNESS = "glm-code"
ALT_SUB_SETTINGS = "settings.json"
# Ключи секции env, без которых окно второй подписки не работает: endpoint,
# токен и модель. Болванка кладётся с пустыми значениями, потому что берутся они
# в кабинете подписки и придумать их автоматике нечем; сам токен в находки не
# печатается никогда, у него только признак «есть» или «нет».
ALT_SUB_KEYS = ("ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL")
# Копия окна второй подписки: вечное рабочее дерево ../<проект>-<поставщик>, в
# котором живёт окно редактора (shipctl code, DK-192). Окружение подписки лежит
# там в настройках самой директории, потому окно и работает, как бы его ни
# открыли. Хозяин ключей при этом машинный слой, и разъехавшись с ним, копия
# молча ходит в старый endpoint со старым токеном, а видно это по счёту в конце
# недели. Суффикс тот же, что у shipctl (windowSuffix в tools/shipctl/editor.go).
WINDOW_SUFFIX = ALT_SUB_HARNESS[:-len("-code")] if ALT_SUB_HARNESS.endswith("-code") else ALT_SUB_HARNESS
WINDOW_ENV_FILE = ".claude/settings.local.json"
WINDOW_HARNESS_KEY = "DEVKIT_HARNESS"
DEPLOY_CONFIG = ".devkit/deploy.local"
DEPLOY_IGNORE = ".devkit/*.local"
RUN_LOG = ".devkit/log"
# Машинные записи .devkit, которые автоматика подключения раскладывает в
# .gitignore, а отсутствие доктор помечает находкой и чинит по --fix, как журнал
# запусков. Путь path уходит в git check-ignore (для cmdout завершающий слэш
# обязателен: без него при отсутствующем на диске каталоге git игнор не
# подтверждает и выйдет ложная находка; путь с шаблоном проверяется как есть,
# звёздочку git сверяет с самим паттерном), comment встаёт в .gitignore строкой
# выше паттерна, а why поясняет находку. Список общий для
# scaffold_machine_gitignore (new, corp) и check_machine_ignore (doctor), иначе
# две копии разойдутся в первый же раз.
MACHINE_IGNORE_ENTRIES = (
    (".devkit/cmdout/",
     "# Полные выводы команд под обёрткой cmdout (DK-264), живут только на машине.",
     "полные выводы команд замусорят status"),
    (".devkit/ship.lock",
     "# Замок конвейера shipctl, живёт только на машине.",
     "замок конвейера замусорит status"),
    (".devkit/goal-*",
     "# Рабочее состояние цикла цели: журнал витков, отметки доставки и замки, живут только на машине.",
     "журнал витков и отметки цели замусорят status"),
    (".devkit/chat/",
     "# Реплики человека живой сессии (DK-345): ждут свою сессию в дереве работы, живут только на машине.",
     "лежащие реплики замусорят status"),
)
# Ключи болванки выката: имя, комментарий (со своим завершающим \n) и значение
# для файла, заводимого с нуля. Один источник для DEPLOY_TEMPLATE (новый файл)
# и для дописывания недостающего в уже существующий: комментарий у ключа один,
# держать его в двух местах значило бы дать им разойтись.
DEPLOY_FIELDS = (
    ("deploy",
     "# Обвязка выката для shipctl (гитигнорнут: в команде выката обычно адрес\n"
     "# или роль машины, её место в локальном, а не в коммитимом). shipctl merge\n"
     "# берёт команду отсюда, --deploy на каждый вызов передавать не нужно.\n"
     "# Объект выката бывает не только серверным: для приложения сюда идёт\n"
     "# сборка, подпись и заливка в канал обновлений, а не серверный рестарт.\n",
     ""),
    ("test",
     "# test это команда тестов проекта, её берёт shipctl merge, когда не передан\n"
     "# --test: сочинять её под каждый репозиторий процедуре пачки нечем.\n",
     ""),
    ("autonomous",
     "# autonomous=true отдаёт агенту весь конвейер: готовую задачу (тесты\n"
     "# зелёные, ревью чистое) он сам сливает, пушит в origin и катит на прод.\n"
     "# false оставляет слияние, пуш и выкат за пользователем.\n",
     "false"),
)
DEPLOY_TEMPLATE = "".join("%s%s =%s\n" % (comment, key, " %s" % default if default else "")
                          for key, comment, default in DEPLOY_FIELDS)
SKIP_DIRS = {".git", "node_modules", "vendor", "target", "local-docs",
             ".venv", "venv", "__pycache__", ".idea", ".vscode"}
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
CODE_SPAN_RE = re.compile(r"`+[^`]*`+")
GOWORK_FILENAME = "go.work"
GOWORK_ENV = "GOWORK=off"
# Команда, в которой го зовётся напрямую: отдельное слово go со своим первым
# аргументом. Слово нужно чтобы отличить от «lego», «django», «golangci» и иже
# с ними: \b отделит go от соседних букв. «python3 build.py» под это не подходит
# (сборка спрятана за обёрткой и GOWORK=off ставит внутри себя), «go test» и
# «cd x && go build» подходят.
GO_INVOCATION_RE = re.compile(r"\bgo\s+\S")


def run(args, cwd=None):
    p = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    return p.returncode, (p.stdout + p.stderr).strip()


def ensure_package(name, why, fix):
    """Недостающий пакет: доводка ставит его сама, раз человек позвал установку.

    Ставит только тем менеджером, который на машине уже есть: чужого devkit не
    подставляет, и без менеджера остаётся находка с причиной и командой. Отдаёт
    (находки, сделанное).
    """
    manager = shutil.which(PACKAGER)
    if not manager:
        # Чужой менеджер тут не называется: какой на этой машине, devkit не
        # знает, а придуманная команда хуже её отсутствия.
        return ["%s; поставить нечем: пакетного менеджера %s на машине нет, ставить %s руками"
                % (why, PACKAGER, name)], []
    if not fix:
        return ["%s; поставит devkitctl doctor --fix, руками %s install %s"
                % (why, PACKAGER, name)], []
    rc, out = run([manager, "install", name])
    if rc != 0:
        # Ответ менеджера идёт в находку целиком последней строкой, а когда он
        # промолчал, так и говорим: пустые скобки в тексте это не ответ.
        tail = (out.strip().splitlines() or [""])[-1].strip()
        return ["%s; %s install %s ответил ошибкой%s, ставить руками"
                % (why, PACKAGER, name, (": %s" % tail) if tail else " и ничего не сказал")], []
    if not shutil.which(name):
        # Второй исход, и он не то же самое: команда отработала нулём, а пакета
        # в PATH нет. Так выглядит сломанный менеджер, и сказать про это надо
        # ровно то, что случилось, а не «не прошёл».
        return ["%s; %s install %s отработал, а %s в PATH так и не появился: ставить руками"
                % (why, PACKAGER, name, name)], []
    return [], ["установлен %s (%s install %s)" % (name, PACKAGER, name)]


def project_root(start):
    rc, out = run(["git", "-C", start, "rev-parse", "--show-toplevel"])
    if rc == 0:
        return Path(out), True
    return Path(start).resolve(), False


def check_links(root):
    # Проверяется живая дока: корневые тексты, README инструментов, kit/ и
    # hooks/. docs/ стареет по правилу и не проверяется: файл задачи описывает
    # состояние на момент решения и после закрытия не правится, так что ссылка
    # на уехавший путь там законна, а находка по ней зовёт чинить то, что чинить
    # запрещено (DK-140, DK-144).
    findings = []
    for dirpath, dirnames, filenames in os.walk(root):
        dp = Path(dirpath)
        # Исключение считается от корня репозитория, которому принадлежит
        # файл, а не от каталога запуска: при обходе, зашедшем во вложенный
        # репозиторий (свой .git прямо тут), его docs/ исключается по тому же
        # правилу, что и docs/ каталога запуска (DK-160). .exists(), а не
        # .is_dir(): у worktree и submodule .git это файл со строкой
        # "gitdir: ...", не каталог, и is_dir() такой репозиторий бы пропустил.
        if dp == Path(root) or (dp / ".git").exists():
            dirnames[:] = [d for d in dirnames if d != "docs"]
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if not fn.endswith(".md"):
                continue
            md = dp / fn
            rel = os.path.relpath(md, root)
            try:
                lines = md.read_text(encoding="utf-8", errors="replace").splitlines()
            except OSError:
                continue
            fence = ""
            for i, ln in enumerate(lines, 1):
                fm = FENCE_RE.match(ln)
                if fm:
                    # Закрывает только забор из того же символа и не короче
                    # открывающего, иначе вложенный ``` оборвал бы внешний ````.
                    if not fence:
                        fence = fm.group(1)
                    elif fm.group(1)[0] == fence[0] and len(fm.group(1)) >= len(fence) \
                            and not ln[fm.end():].strip():
                        fence = ""
                    continue
                if fence:
                    continue
                # Инлайн-код это текст: [текст](путь) в примере команды не ссылка.
                for m in LINK_RE.finditer(CODE_SPAN_RE.sub(lambda c: " " * len(c.group(0)), ln)):
                    target = m.group(1).split("#")[0]
                    if not target or "://" in target or target.startswith("mailto:"):
                        continue
                    if not (md.parent / target).exists():
                        findings.append("%s:%d: битая ссылка (%s)" % (rel, i, m.group(1)))
    return findings


def read_deploy(root):
    # Тройка (deploy, test, autonomous). None вместо deploy это «файла нет»,
    # иначе значения ключей (могут быть пустыми) и флаг autonomous. Пустая
    # строка тут значит и «ключа нет», и «ключ есть, но пустой» одинаково: за
    # различением этих двух случаев (нужно для дописывания недостающего) идти
    # в deploy_present_keys, не сюда.
    f = root / DEPLOY_CONFIG
    if not f.exists():
        return None, None, None
    deploy = ""
    test = ""
    autonomous = False
    for ln in f.read_text(encoding="utf-8", errors="replace").splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#") or "=" not in ln:
            continue
        key, _, val = ln.partition("=")
        key_stripped = key.strip()
        val_stripped = val.strip().strip("\"'")
        if key_stripped == "deploy":
            deploy = val_stripped
        elif key_stripped == "test":
            test = val_stripped
        elif key_stripped == "autonomous":
            autonomous = val_stripped.lower() in ("1", "true", "t", "yes", "y", "on")
    return deploy, test, autonomous


def deploy_present_keys(text):
    # Ключи, реально написанные в файле, вне зависимости от значения (в том
    # числе пустого). read_deploy для отсутствующего и пустого ключа отдаёт
    # одно и то же "", а дописыванию нужно отличать одно от другого.
    keys = set()
    for ln in text.splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#") or "=" not in ln:
            continue
        key, _, _ = ln.partition("=")
        keys.add(key.strip())
    return keys


def ensure_gitignore(root, pattern, comment="# Локальная обвязка выката, живёт только на машине."):
    gi = root / ".gitignore"
    lines = gi.read_text(encoding="utf-8").splitlines() if gi.exists() else []
    if pattern in (ln.strip() for ln in lines):
        return False
    sep = "\n" if lines and lines[-1].strip() else ""
    with gi.open("a", encoding="utf-8") as f:
        f.write("%s%s\n%s\n" % (sep, comment, pattern))
    return True


LOG_IGNORE_COMMENT = "# Журнал запусков инструментов devkit, живёт только на машине."


def scaffold_deploy(root):
    dep = root / DEPLOY_CONFIG
    done = []
    if dep.exists():
        done.append("%s уже есть, не трогаю" % DEPLOY_CONFIG)
    else:
        dep.parent.mkdir(parents=True, exist_ok=True)
        dep.write_text(DEPLOY_TEMPLATE, encoding="utf-8")
        done.append("%s создан: вписать команду выката, autonomous при готовности" % DEPLOY_CONFIG)
    if ensure_gitignore(root, DEPLOY_IGNORE):
        done.append(".gitignore: добавлен %s" % DEPLOY_IGNORE)
    if ensure_gitignore(root, RUN_LOG, LOG_IGNORE_COMMENT):
        done.append(".gitignore: добавлен %s" % RUN_LOG)
    return done


def scaffold_machine_gitignore(root):
    # Машинные гитигнор-записи .devkit (cmdout/, ship.lock, goal-*)
    # раскладываются при любом подключении проекта, в том числе без доски: cmdout
    # и shipctl работают и в проекте без доски, а цель ведут не все, и doctor
    # проверяет их в блоке in_git, а не доски.
    # Список MACHINE_IGNORE_ENTRIES общий с check_machine_ignore, иначе две
    # копии разойдутся в первый же раз.
    done = []
    for path, comment, _why in MACHINE_IGNORE_ENTRIES:
        if ensure_gitignore(root, path, comment):
            done.append(".gitignore: добавлен %s" % path)
    return done


def patch_deploy(root):
    # Дописывает в конец уже существующего deploy.local ключи болванки,
    # которых в файле нет, каждый со своим комментарием. Чужой текст (значения
    # имеющихся ключей, шапка, их порядок) не трогается, только добавление в
    # хвост. Дописанный ключ остаётся пустым (не берётся значение по
    # умолчанию из DEPLOY_FIELDS): находка про пустой test= после починки
    # обязана остаться, а пустой autonomous и так штатно читается как false.
    dep = root / DEPLOY_CONFIG
    text = dep.read_text(encoding="utf-8", errors="replace")
    present = deploy_present_keys(text)
    missing = [(key, comment) for key, comment, _ in DEPLOY_FIELDS if key not in present]
    if not missing:
        return []
    sep = "\n" if text and not text.endswith("\n") else ""
    addition = "".join("%s%s =\n" % (comment, key) for key, comment in missing)
    with dep.open("a", encoding="utf-8") as f:
        f.write(sep + addition)
    return ["%s: дописан недостающий ключ %s" % (DEPLOY_CONFIG, key) for key, _ in missing]


def nearest_gowork(start):
    """Ближайший вверх от start go.work либо None.

    Подъём идёт по тем же правилам, что у самого го: первый встреченный по
    дороге вверх. Каталог go.work нужен, чтобы пути из его use-директив
    разрешать от него, а не от проекта.
    """
    cur = Path(start).resolve()
    while True:
        candidate = cur / GOWORK_FILENAME
        if candidate.is_file():
            return candidate, cur
        if cur.parent == cur:
            return None
        cur = cur.parent


def gowork_use_directories(gowork_path, gowork_dir):
    """Каталоги, которые go.work подключает через use.

    Пути в use пишутся относительно каталога go.work и обязаны начинаться с
    ./, ../ либо быть абсолютными; use без префикса ./, ../ или / го в
    синтаксисе не допускает. Блочная форма «use ( ... )» раскрывается
    построчно, однострочная «use ./path» разбирается прямо в ней. Комментарии
    //, допустимые в любой строке use-блока, выкидываются до разбора пути.
    """
    uses = []
    try:
        text = gowork_path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return uses
    in_block = False
    for ln in text.splitlines():
        s = ln.strip()
        if in_block:
            if s.startswith(")"):
                in_block = False
                continue
            if s.startswith("//"):
                continue  # строка-комментарий, го её из use выкидывает
            tok = s.split()[0] if s.split() else ""
            if tok.startswith(("./", "../", "/")):
                uses.append((gowork_dir / tok).resolve())
            continue
        if not s.startswith("use"):
            continue
        rest = s[3:].strip()
        if rest.startswith("("):
            rest = rest[1:].strip()
            if rest.startswith(")"):
                continue  # пустой use ()
            in_block = True
            if not rest:
                continue
            tok = rest.split()[0]
            if tok.startswith(("./", "../", "/")):
                uses.append((gowork_dir / tok).resolve())
            continue
        if not rest:
            continue
        tok = rest.split()[0]
        if tok.startswith(("./", "../", "/")):
            uses.append((gowork_dir / tok).resolve())
    return uses


def project_in_gowork(project_root, gowork_path, gowork_dir):
    """True, если проект покрыт use-директивами go.work.

    Покрытым считается проект, чей корень сам лежит в use, или лежит ниже
    use-пути (use указывает на него или его предка), или содержит один из
    use-путей (один из его модулей охвачен). Полное покрытие всех модулей тут
    не проверяется: находка доктора подсказывает приём GOWORK=off, а не
    досматривает редкий случай много-модульного проекта с частичным покрытием.
    """
    proot = Path(project_root).resolve()
    for use_dir in gowork_use_directories(gowork_path, gowork_dir):
        try:
            use_dir.relative_to(proot)
            return True  # use внутри проекта: один из модулей охвачен
        except ValueError:
            pass
        try:
            proot.relative_to(use_dir)
            return True  # use выше проекта: весь проект внутри use
        except ValueError:
            pass
    return False


def has_go_mod(root):
    """Есть ли в проекте go.mod. Без него находка про go.work лезла бы в
    нерелевантный проект (python, rust и пр.), случайно лежащий рядом с чужим
    go.work."""
    root = Path(root)
    for dp, dirnames, filenames in os.walk(root):
        if "go.mod" in filenames:
            return True
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
    return False


def check_gowork(root, deploy, test):
    """Находка про чужой go.work рядом с го-проектом (DK-115).

    go.work, в котором проект не перечислен, отказывает го-командам из его
    каталога ответом «directory prefix . does not contain modules listed in
    go.work». Для shipctl merge это выглядит как красные тесты после ребейза на
    зелёном коде, и слияние встаёт на ровном месте. Починка называется в самом
    тексте: GOWORK=off перед го-командой в deploy.local гасит рабочее
    пространство го для этого вызова. Молчит, когда проекта нет ни в одном
    предке, проекта в go.work нет, го-команд в обвязке выката нет, либо они уже
    обёрнуты GOWORK=off.
    """
    findings = []
    proot = Path(root).resolve()
    near = nearest_gowork(proot)
    if near is None:
        return findings
    if not has_go_mod(proot):
        return findings
    gowork_path, gowork_dir = near
    if project_in_gowork(proot, gowork_path, gowork_dir):
        return findings
    bare = []
    for key, value in (("test", test), ("deploy", deploy)):
        if value and GO_INVOCATION_RE.search(value) and GOWORK_ENV not in value:
            bare.append(key)
    if not bare:
        return findings
    keys = " и ".join("%s=" % k for k in bare)
    rel = os.path.relpath(gowork_path, proot)
    findings.append(
        "рядом лежит %s, где проект не перечислен в use: го-команды из каталога "
        "проекта отказывают тем же ответом «directory prefix . does not contain "
        "modules listed in go.work», для shipctl merge это красные тесты после "
        "ребейза на ровном месте; дописать «export %s; » в начало ключа %s "
        "файла %s"
        % (rel, GOWORK_ENV, keys, DEPLOY_CONFIG)
    )
    return findings


def log_run(root, cmd, code):
    # Журнал запусков, общий с tools/taskctl/shipctl/regcheck: статистика, какие
    # команды реально гоняются и как часто падают. Только там, где есть
    # .devkit, провал записи работу не ломает.
    d = root / ".devkit"
    if not d.is_dir():
        return
    try:
        with (d / "log").open("a", encoding="utf-8") as f:
            f.write("%s\tdevkitctl\t%s\t%d\n"
                    % (datetime.now().strftime("%Y-%m-%dT%H:%M:%S"), cmd, code))
    except OSError:
        pass


def worktree_top(root):
    """Вершина рабочего дерева. Относительный core.hooksPath git считает от неё,
    а корень проекта devkit бывает и подкаталогом: боковая директория контура
    лежит внутри его репозитория (DK-583)."""
    rc, out = run(["git", "rev-parse", "--show-toplevel"], cwd=root)
    return Path(out.strip()) if rc == 0 and out.strip() else Path(root)


def check_git_hooks(root):
    hooks_dir = (DEVKIT / "hooks").resolve()
    top = worktree_top(root)
    rc, hp = run(["git", "config", "core.hooksPath"], cwd=root)
    if rc == 0 and hp:
        cand = Path(os.path.expanduser(hp))
        if not cand.is_absolute():
            cand = top / cand
        if cand.exists() and cand.resolve() == hooks_dir:
            return None
    else:
        rc, gp = run(["git", "rev-parse", "--git-path", "hooks"], cwd=root)
        pre = (Path(gp) if os.path.isabs(gp) else root / gp) / "pre-commit"
        if pre.exists() and Path(os.path.realpath(pre)).parent == hooks_dir:
            return None
    rel = os.path.relpath(hooks_dir, top)
    return "git-хуки devkit не подключены; из корня проекта: git config core.hooksPath %s" % rel


def connect_git_hooks(root):
    # Подключить хуки devkit. Возврат (сделано, находка): либо одно непустое,
    # либо оба None, когда хуки уже на месте. Свои хуки проекта не трогаются,
    # это остаётся находкой (симлинки ставит человек). Мутирует.
    if check_git_hooks(root) is None:
        return None, None
    rc, gp = run(["git", "rev-parse", "--git-path", "hooks"], cwd=root)
    hdir = Path(gp) if os.path.isabs(gp) else root / gp
    custom = [p.name for p in hdir.glob("*") if p.is_file() and not p.name.endswith(".sample")]
    if custom:
        return None, ("в хуках проекта уже есть свои (%s), core.hooksPath не трогается; "
                      "симлинки на хуки devkit по hooks/README.md" % ", ".join(custom))
    hooks_rel = os.path.relpath((DEVKIT / "hooks").resolve(), worktree_top(root))
    run(["git", "config", "core.hooksPath", hooks_rel], cwd=root)
    return "git-хуки: core.hooksPath = %s" % hooks_rel, None


def quota_taken(path):
    # Момент снятия из снимка. None и когда строки taken нет, и когда она не
    # разобрана: для доктора это один случай, снимку нельзя верить по возрасту.
    for ln in path.read_text(encoding="utf-8", errors="replace").splitlines():
        key, sep, val = ln.partition("=")
        if not sep or key.strip() != "taken":
            continue
        val = val.strip()
        for fmt in QUOTA_TIME_FORMATS:
            try:
                return datetime.strptime(val, fmt)
            except ValueError:
                pass
        return None
    return None


def devkit_checkout():
    # Основной чекаут devkit и признак, что скрипт запущен из него. Версия
    # бинарей сверяется с HEAD основного чекаута, а сборка из worktree ветки
    # задачи не запускается вовсе: машинный бинарь с непроверенной ветки уехал
    # бы во все проекты сразу.
    rc, out = update.git(DEVKIT, "rev-parse", "--git-common-dir")
    if rc != 0:
        return DEVKIT, True
    common = Path(out)
    if not common.is_absolute():
        common = DEVKIT / common
    main = common.resolve().parent
    return main, main == DEVKIT.resolve()


def path_winner(name, target):
    """Чем плох свежий бинарь с точки зрения PATH: его там не видно вовсе либо
    выигрывает чужая копия. Отдаёт каталог победителя, пустую строку, когда в
    PATH утилиты нет совсем, и None, когда всё в порядке.

    Судится тут одна утилита, а говорит про них доктор пачкой (path_findings):
    каталог назначения мимо PATH это один факт на все шесть, и строка на утилиту
    превращала бы его в простыню.
    """
    found = shutil.which(name)
    if not found:
        return ""
    if os.path.realpath(found) != os.path.realpath(str(target)):
        return os.path.dirname(found) or found
    return None


def grouped_binaries(broken, how, join="; "):
    """Находки про бинари: одна строка на общую беду, а не на утилиту.

    На голой машине повод у всех шести один и тот же («не в PATH»), и шесть
    одинаковых строк подряд человек читает как одну. Беды, у которых текст
    разный (свой коммит, своя версия), остаются каждая своей строкой: их и надо
    прочитать порознь.
    """
    groups = {}
    for name, why in broken:
        groups.setdefault(why, []).append(name)
    findings = []
    for why, names in groups.items():
        if len(names) == 1:
            findings.append("%s %s%s%s" % (names[0], why, join, how))
            continue
        findings.append("%s devkit %s: %s%s%s" % (say.counted(len(names), update.UTILS), why,
                                                  ", ".join(names), join, how))
    return findings


def path_findings(unseen, shadow, gobin):
    """Находки про PATH одной строкой на повод, а не на утилиту.

    unseen это утилиты, которых в PATH не видно вовсе, shadow это каталог
    чужой копии -> имена, которые она перебивает.
    """
    findings = []
    if unseen:
        # Команду для профиля тут не повторяем: её называет общая находка про
        # каталог мимо PATH, а эта говорит, что именно осталось незваным.
        findings.append("в %s лежит %s devkit, а самого каталога нет в PATH: %s"
                        % (gobin, say.how_many(unseen, update.UTILS), ", ".join(unseen)))
    for where in sorted(shadow):
        names = shadow[where]
        findings.append("в %s лежит %s devkit, а в PATH выигрывает чужая копия из %s: %s; убрать "
                        "старое либо поднять %s выше в PATH"
                        % (gobin, say.how_many(names, update.UTILS), where,
                           ", ".join(names), gobin))
    return findings


def unsaved_go(main):
    """Утилиты с несохранёнными правками go-кода: имя -> mtime самого свежего файла.

    Это единственный хвост, где сверка версий остаётся без опоры: коммит бинаря
    равен HEAD, а исходники уже другие, и сравнивать нечего. Только тут доктор
    досравнивает mtime, как делал до сверки по коммиту.
    """
    rc, out = update.git(main, "status", "--porcelain", "--", "*.go")
    dirty = set()
    if rc != 0:
        return {}
    for line in out.splitlines():
        parts = Path(line[3:].strip().strip('"')).parts
        if len(parts) > 2 and parts[0] == "tools":
            dirty.add(parts[1])
    return {name: max((p.stat().st_mtime for p in (Path(main) / "tools" / name).glob("*.go")),
                      default=0) for name in dirty}


def code_commit(main, name):
    """Последний коммит, тронувший go-код утилиты.

    Ожидание считается по коду, а не по HEAD. Доска devkit живёт в том же
    репозитории, и коммит «DK-000 в Check» уводит HEAD вперёд, не меняя в бинаре
    ни байта: сверка с HEAD краснела бы после каждого такого коммита, а их за
    день десятки. Проверка, красная всегда, читаться перестаёт, и --fix на ней
    гоняет сборку впустую. Берутся только файлы, из которых собирается бинарь:
    рядом в каталоге утилиты лежит ещё и README, и по нему сверяться нельзя.
    """
    where = "tools/%s/" % name
    rc, out = update.git(main, "log", "-1", "--format=%H", "--",
                         where + "*.go", where + "go.mod", where + "go.sum")
    return out.strip().splitlines()[0] if rc == 0 and out.strip() else ""


def version_gap(main, code, commit):
    """Отстал ли бинарь от кода: текст причины либо None.

    Свежим считается бинарь, собранный из коммита, в котором последняя правка
    кода утилиты уже есть, то есть из неё самой либо из любого более позднего:
    сборка с HEAD годится, как и сборка сразу после правки.
    """
    if not code:
        return None
    rc, _ = update.git(main, "rev-parse", "--verify", "--quiet",
                       "%s^{commit}" % commit)
    if rc != 0:
        return "этого коммита в клоне devkit нет"
    rc, _ = update.git(main, "merge-base", "--is-ancestor", code, commit)
    if rc != 0:
        return "а код утилиты правился позже, в %s" % code[:12]
    return None


def binary_trouble(path, main, code, mode, stale_after):
    """Что не так с бинарём: текст причины либо None, если всё сходится.

    Судится по коммиту, зашитому в бинарь: правило сходимости из DK-149 держится
    на коммитах, а не на времени файла, и «немного разошлись» тут не бывает.
    """
    if not path or not os.path.exists(str(path)):
        return "не в PATH"
    stamp = update.binary_stamp(path)
    if stamp is None:
        return "не отвечает на --version: собран до релизной схемы версий"
    version, commit = stamp
    gap = version_gap(main, code, commit)
    if gap:
        return "собран из коммита %s (версия %s), %s; чекаут devkit %s" % (commit, version, gap, mode)
    if stale_after and os.path.getmtime(str(path)) < stale_after:
        return "старее несохранённых правок go-кода в devkit"
    return None


def check_binaries(fix):
    """Бинари devkit в PATH и на одной версии с чекаутом.

    Список проверяемых выводится из дерева (tools/*/go.mod), а не из перечня в
    коде: по правилу языка это ровно то, что ставится на машину, и новая утилита
    попадает под проверку тем, что она есть.
    """
    findings, fixed = [], []
    gobin = update.bin_dir()
    main, from_main = devkit_checkout()
    # Точка сборки одна на машину и на CI, поэтому доктор и советует, и собирает
    # тем же devkitctl build: своя копия go build разошлась бы с ним ключами
    # (версия зашивается линковкой) в первый же раз.
    cmd = "python3 %s/tools/devkitctl/devkitctl.py build" % main
    src = main if (main / "tools").is_dir() else DEVKIT
    mode = update.checkout_mode(main)
    stale = unsaved_go(main)
    broken, unseen, shadow = [], [], {}
    for name in build.tools(src):
        target = gobin / name
        code = code_commit(main, name)
        # Судится то, что выигрывает в PATH: пользоваться человек будет им, а не
        # тем, что лежит в каталоге назначения.
        why = binary_trouble(shutil.which(name), main, code, mode, stale.get(name, 0))
        if why is None:
            continue
        if binary_trouble(target, main, code, mode, stale.get(name, 0)) is None:
            # Годная сборка уже лежит на месте, дело за PATH.
            where = path_winner(name, target)
            if where == "":
                unseen.append(name)
            elif where:
                shadow.setdefault(where, []).append(name)
            continue
        broken.append((name, why))
    if not broken:
        return findings + path_findings(unseen, shadow, gobin), fixed
    if not from_main:
        # Машинный бинарь с непроверенной ветки уехал бы во все проекты сразу,
        # поэтому чинится расхождение только из основного чекаута.
        findings += grouped_binaries(broken, "а devkit тут выложен worktree ветки задачи: "
                                             "собирать машинный бинарь с непроверенной ветки "
                                             "нельзя; из основного чекаута: %s" % cmd, ", ")
        return findings + path_findings(unseen, shadow, gobin), fixed
    tag = update.head_tag(main)
    go = shutil.which("go")
    if tag:
        # Чекаут стоит на релизе: бинари этого тега выложены готовыми, и тулчейна
        # на машине потребителя может не быть вовсе.
        how = "поставить бинари релиза %s: devkitctl update" % tag
    elif go:
        how = cmd
    else:
        how = ("собирать нечем, go в PATH нет: Go ставится пакетным менеджером "
               "(brew install go), потом %s" % cmd)
    if not fix or not (tag or go):
        findings += grouped_binaries(broken, how)
        return findings + path_findings(unseen, shadow, gobin), fixed
    if tag:
        # Отчёт установки идёт в свой список: доктор говорит про машинный контур
        # одной строкой на находку, а «сумма сошлась» это подробность команды.
        said = []
        update.install(main, tag, said.append, lambda m: findings.append(str(m)))
    else:
        version, commit = build.stamp(main)
        gobin.mkdir(parents=True, exist_ok=True)
        for name, _ in broken:
            err = (build.compile_one(main, name, gobin / name, version, commit)
                   or build.check_run(name, gobin / name, version, commit))
            if err:
                findings.append(err)
    done, left = [], []
    for name, why in broken:
        if binary_trouble(gobin / name, main, code_commit(main, name), mode,
                          stale.get(name, 0)) is not None:
            left.append((name, why))
            continue
        done.append((name, update.binary_stamp(gobin / name)))
        where = path_winner(name, gobin / name)
        if where == "":
            unseen.append(name)
        elif where:
            shadow.setdefault(where, []).append(name)
    findings += grouped_binaries(left, how)
    findings += path_findings(unseen, shadow, gobin)
    if done:
        # Утилиты приезжают пачкой, и строка на каждую это одно и то же слово
        # шесть раз подряд. Версия у них общая, а если нет, она называется у
        # каждой: разнобой версий в PATH это то, что как раз надо увидеть.
        versions = sorted({v for _, (v, _) in done})
        names = [n if len(versions) == 1 else "%s (%s)" % (n, s[0]) for n, s in done]
        whence = (" релиза %s" % tag if tag else
                  " версии %s" % versions[0] if len(versions) == 1 else "")
        verbs = ("установлена", "установлено") if tag else ("собрана", "собрано")
        fixed.append(say.folded(verbs, tuple(w + whence for w in update.UTILS), names, gobin))
    return findings, fixed


def fix_hint(main, from_main, what):
    """Чем чинится раскладка машинного контура, одной командой на всю пачку.

    Копировать файлы руками человеку незачем, это работа доводки, поэтому в
    находке стоит она, а не строка cp на каждый файл. Из worktree ветки задачи
    команда зовётся в основном чекауте: на машину, видную каждой сессии, с
    непроверенной ветки не едет ничего.
    """
    if from_main:
        return "разложить: devkitctl doctor --fix"
    return ("devkit тут выложен worktree ветки задачи, класть на машину %s с непроверенной "
            "ветки нельзя; из основного чекаута: python3 %s/tools/devkitctl/devkitctl.py "
            "doctor --fix" % (what, main))


def is_agent_def(path):
    # Определение субагента опознаётся по frontmatter с effort: именно оттуда
    # харнес берёт глубину размышления, и файл без него агентом не работает.
    head = path.read_text(encoding="utf-8", errors="replace").split("\n", 40)
    if not head or head[0].strip() != "---":
        return False
    for line in head[1:]:
        if line.strip() == "---":
            return False
        if line.startswith("effort:"):
            return True
    return False


def check_agent_defs(fix, dst_dir):
    # Эталон берётся из основного чекаута, а не из того, откуда запущен doctor:
    # определение с ветки задачи уехало бы на машину во все проекты сразу.
    # Каталог назначения приходит из профиля харнеса ([delegate] agents_dir), а
    # не из константы: у второй подписки он свой, и раскладка ей нужна такая же.
    findings, fixed = [], []
    laid, again, none, stale = [], [], [], []
    main, from_main = devkit_checkout()
    src_dir = main / "kit" / "agents" if (main / "kit" / "agents").is_dir() else DEVKIT / "kit" / "agents"
    how = fix_hint(main, from_main, "определения агентов")
    # Директория перебирается целиком, а не по префиксу: набор растёт ролями
    # (exec-* для исполнения, review-* для ревью), и новая роль должна
    # раскладываться сама, без правки доктора. Отбор идёт по frontmatter, иначе
    # на машину как агент уехал бы любой соседний markdown вроде README.
    for src in sorted(src_dir.glob("*.md")):
        if not is_agent_def(src):
            continue
        dst = dst_dir / src.name
        if dst.exists():
            if dst.read_text(encoding="utf-8", errors="replace") != src.read_text(encoding="utf-8"):
                # devkit источник правды для промптов агентов: правка, сделанная в
                # репозитории, обязана доехать на машину сама, а не остаться
                # находкой навсегда. Ручную правку на машине --fix затирает, но не
                # молча: отчёт называет, что именно переложил.
                if fix and from_main:
                    shutil.copyfile(src, dst)
                    again.append(src.stem)
                else:
                    stale.append(src.stem)
            continue
        if fix and from_main:
            dst_dir.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(src, dst)
            laid.append(src.stem)
            continue
        none.append(src.stem)
    if none:
        findings.append("%s; effort из вердикта pick применять нечем, спавн уйдёт на дефолтного "
                        "агента; %s"
                        % (say.folded(("не разложено", "не разложено"), AGENT_WORD, none, dst_dir),
                           how))
    if stale:
        findings.append("%s; на машине лежит старое, а devkit ушёл вперёд; %s"
                        % (say.folded(("разошлось", "разошлось"), AGENT_WORD, stale, dst_dir), how))
    if laid:
        fixed.append(say.folded(("установлено", "установлено"), AGENT_WORD, laid, dst_dir))
    if again:
        fixed.append(say.folded(("обновлено", "обновлено"), AGENT_WORD, again, dst_dir,
                                ", devkit ушёл вперёд"))
    return findings, fixed


def skill_files(skill):
    """Файлы скилла относительными путями: каталог едет на машину целиком.

    Разбирать `SKILL.md` в поисках имён спутников доктор не берётся: имена
    названы там прозой, и парсер прозы отставал бы от каждой правки скилла, а
    отставание видно только в чужом проекте, где чекаута devkit нет. Каталог
    как единица раскладки проверяется тем, что файл в нём лежит.

    Служебное (`__pycache__`, точечные файлы вроде `.DS_Store`) отсеивается:
    оно заводится прогоном тестов и файловым менеджером, а не автором скилла, и
    уехав на машину, давало бы находку про расхождение на ровном месте.
    """
    out = []
    for path in sorted(skill.rglob("*")):
        if not path.is_file():
            continue
        rel = path.relative_to(skill)
        if any(part == "__pycache__" or part.startswith(".") for part in rel.parts):
            continue
        out.append(rel)
    return out


def check_skills(fix, dst_dir):
    # Скиллы едут на машину тем же каналом, что определения субагентов: эталон
    # это основной чекаут, из worktree ветки задачи идёт только сверка. Скилл
    # это директория с SKILL.md, по нему он и опознаётся, а едет директория
    # целиком: у proofread рядом со SKILL.md лежат пары, словарь и корпус, и без
    # них вычитка в чужом проекте идёт по одним названиям пунктов (DK-331).
    # Каталог назначения из профиля харнеса ([skills] dir), у каждого
    # включённого он свой.
    findings, fixed = [], []
    laid, again, none, stale = [], [], [], []
    main, from_main = devkit_checkout()
    src_dir = main / "kit" / "skills" if (main / "kit" / "skills").is_dir() else DEVKIT / "kit" / "skills"
    how = fix_hint(main, from_main, "скиллы")
    for src in sorted(src_dir.glob("*/SKILL.md")):
        skill = src.parent
        name = skill.name
        dst = dst_dir / name
        # Скилл, у которого нет на машине даже SKILL.md, не разложен вовсе;
        # у остального недостающий или разошедшийся спутник это то же
        # расхождение с devkit, что правка самого SKILL.md руками. Сравнение
        # побайтовое: в каталоге скилла лежит и python (оболочка goal-loop).
        fresh = not (dst / "SKILL.md").exists()
        apart = [rel for rel in skill_files(skill)
                 if not (dst / rel).is_file() or (dst / rel).read_bytes() != (skill / rel).read_bytes()]
        if not apart:
            continue
        if fix and from_main:
            for rel in apart:
                (dst / rel).parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(skill / rel, dst / rel)
            (laid if fresh else again).append(name)
            continue
        (none if fresh else stale).append(name)
    if none:
        findings.append("%s; сессия соберёт процедуру на глаз, а не по скиллу devkit; %s"
                        % (say.folded(("не разложен", "не разложено"), SKILL_WORD, none, dst_dir),
                           how))
    if stale:
        findings.append("%s; на машине лежит старое, а devkit ушёл вперёд; %s"
                        % (say.folded(("разошёлся", "разошлось"), SKILL_WORD, stale, dst_dir), how))
    if laid:
        fixed.append(say.folded(("установлен", "установлено"), SKILL_WORD, laid, dst_dir))
    if again:
        fixed.append(say.folded(("обновлён", "обновлено"), SKILL_WORD, again, dst_dir,
                                ", devkit ушёл вперёд"))
    return findings, fixed


def _hooks_table(data):
    """Событие хука -> список команд из настроек харнеса. None значит, что hooks
    в настройках структурно необычен и сверять раскладку нельзя: settings.json
    правят и человек, и чужие инструменты, и доктор на таком обязан находкой, а
    не стеком, как и на битый JSON. Элемент группы не-словарь, строка вместо
    списка групп или число вместо объекта hooks всё дают None целиком, а не
    падают на первом же шаге итерации."""
    hooks = data.get("hooks")
    if hooks is None:
        return {}
    if not isinstance(hooks, dict):
        return None
    table = {}
    for event, groups in hooks.items():
        if not isinstance(groups, list):
            return None
        cmds = []
        for group in groups:
            if not isinstance(group, dict):
                return None
            for h in group.get("hooks") or []:
                if isinstance(h, dict):
                    cmds.append(h.get("command") or "")
        table[event] = cmds
    return table


def hook_events(text, script):
    # События, на которые в настройках повешен скрипт. None значит, настройки не
    # разобрались (битый JSON или структурно необычный hooks), и судить остаётся
    # по подстроке.
    try:
        data = json.loads(text or "{}")
    except ValueError:
        return None
    if not isinstance(data, dict):
        return None
    table = _hooks_table(data)
    if table is None:
        return None
    return {event for event, cmds in table.items() if any(script in c for c in cmds)}


def hook_gaps(text, settings):
    """Чего не хватает в хуках харнеса. Возврат (пробелы, находки, отставные):
    пробел это строка HOOK_LAYOUT, которую кладёт --fix, находка это тот же
    пробел словами для человека, отставной это имя переименованного хука,
    строку про который --fix из настроек убирает."""
    gaps, findings, stale = [], [], []
    try:
        data = json.loads(text or "{}")
    except ValueError:
        data = None
    if isinstance(data, dict) and data.get("hooks") is not None and _hooks_table(data) is None:
        # JSON цел, но hooks не той формы (элемент группы не словарь, группа не
        # список или сам hooks не объект): чинить раскладку поверх нельзя, и
        # доктор называет это находкой, а не стеком, как уже делал для битого JSON.
        findings.append("структура hooks в %s необычна: элемент группы не словарь, "
                        "группа не список или сам hooks не объект; сверка раскладки "
                        "хуков пропущена, править по hooks/README.md" % settings)
        return gaps, findings, stale
    notify_events = hook_events(text, NOTIFY_HOOK)
    if notify_events is None:
        # Настройки не разобрались, судить остаётся по подстроке: тогда либо
        # уведомитель там есть на всех событиях, либо нет ни на одном.
        notify_events = set(NOTIFY_EVENTS) if NOTIFY_HOOK in text else set()
    watch_events = hook_events(text, WATCH_HOOK)
    if watch_events is None:
        watch_events = set(WATCH_EVENTS) if WATCH_HOOK in text else set()
    missing_notify, missing_post, missing_pre, missing_pre_read = [], [], [], []
    missing_watch = []
    for event, matcher, cmd in HOOK_LAYOUT:
        parts = cmd.split()
        script = os.path.basename(parts[1])
        # Ключ отличает вызовы одного файла на разных событиях (session-task.py
        # стоит на SessionStart с --hook и на PostToolUse с --touch, DK-539):
        # проверка по голому имени файла сочла бы второй вызов уже включённым
        # по первому.
        key = "%s %s" % (script, parts[2]) if len(parts) > 2 else script
        if script == NOTIFY_HOOK:
            if event in notify_events:
                continue
            missing_notify.append(event)
        elif script == WATCH_HOOK:
            if event in watch_events:
                continue
            missing_watch.append(event)
        elif key in text:
            continue
        gaps.append((event, matcher, cmd))
        if script in POST_SCRIPTS:
            # Проверки текстов висят на одном событии втроём, и три одинаковые
            # строки про них это один пробел: хуки харнеса не подключены.
            missing_post.append(script)
        elif script in PRE_SCRIPTS:
            missing_pre.append(script)
        elif script in PRE_READ_SCRIPTS:
            missing_pre_read.append(script)
        elif script == SYNC_HOOK:
            findings.append("рубеж %s не подключён на событии PreToolUse в %s: headless-сессия "
                            "уводит долгое дело в фон, харнес добивает фонового ребёнка через "
                            "десять минут после конца хода, и провал выходит тихим "
                            "(hooks/README.md)" % (SYNC_HOOK, settings))
        elif script == CHAT_HOOK:
            findings.append("подхват реплики %s не подключён на событии PostToolUse в %s: реплика "
                            "человека из чата цели ждёт следующего витка вместо идущего "
                            "(hooks/README.md)" % (CHAT_HOOK, settings))
        elif script == TASK_HOOK and event == "PostToolUse":
            findings.append("PostToolUse-хук %s --touch не подключён в %s: правка файла в боковом "
                            "дереве задачи не оставляет отметку в журнале сессий, и работа вне "
                            "утилит доски остаётся для дашборда невидимой (hooks/README.md)"
                            % (TASK_HOOK, settings))
        elif script == TASK_HOOK:
            findings.append("SessionStart-хук %s не подключён в %s: реестр чатов пуст, и дашборд "
                            "возвращается к угадыванию задачи по транскрипту, а разговор о задаче "
                            "горит её живой работой (hooks/README.md)" % (TASK_HOOK, settings))
        elif script == SESSION_HOOK:
            findings.append("SessionStart-хук %s не подключён в %s: снимок квоты сам не освежается, "
                            "и корректор pick рано или поздно останется с протухшим "
                            "(hooks/README.md)" % (SESSION_HOOK, settings))
        elif script == BOARD_HOOK:
            findings.append("SessionStart-хук %s не подключён в %s: боковое дерево доски не "
                            "догоняется на старте сессии, и устаревшая доска читается как свежая "
                            "(hooks/README.md)" % (BOARD_HOOK, settings))
    if missing_post:
        findings.append("%s; правки текстов идут мимо проверок (hooks/README.md)"
                        % say.folded(("не подключён", "не подключено"), HOOK_WORD, missing_post,
                                     settings, " на событии PostToolUse"))
    if missing_pre:
        # У каждого рубежа на Bash своя дыра, и сообщение называет её, а не
        # общее «чтение секретов»: отсутствующий check-subst про инъекцию.
        for script in missing_pre:
            findings.append("%s на PreToolUse Bash; %s (hooks/README.md)"
                            % (say.folded(("не подключён", "не подключено"), HOOK_WORD,
                                          [script], settings, ""),
                               PRE_GAPS.get(script, "команды Bash идут мимо рубежа")))
    if missing_pre_read:
        # Каждый рубеж на Read режет свою дыру, и сообщение про него обязано
        # называть именно её, а не общим «повторные чтения»: иначе отсутствующий
        # check-longfile выдаётся за повторные чтения, которых на машине нет.
        for script in missing_pre_read:
            findings.append("%s на PreToolUse Read; %s (hooks/README.md)"
                            % (say.folded(("не подключён", "не подключено"), HOOK_WORD,
                                          [script], settings, ""),
                               PRE_READ_GAPS.get(script, "контекст расходуется впустую")))
    if missing_notify:
        findings.append("хук %s не подключён на события %s в %s: сессия молча стоит, когда ждёт "
                        "разрешения, и не говорит, что закончила ход или что субагент "
                        "отработал (hooks/README.md)"
                        % (NOTIFY_HOOK, ", ".join(missing_notify), settings))
    if missing_watch:
        findings.append("сторож %s не подключён на события %s в %s: отчёт фонового субагента "
                        "теряется по дороге, и сессия уходит спать, считая его работающим "
                        "(hooks/README.md)"
                        % (WATCH_HOOK, ", ".join(missing_watch), settings))
    for old in sorted(n for n in RETIRED_HOOKS if n in text):
        stale.append(old)
        findings.append("хук %s в %s переименован в %s: строка зовёт файл, которого в чекауте "
                        "уже нет, и харнес спотыкается на ней каждым ходом (hooks/README.md)"
                        % (old, settings, RETIRED_HOOKS[old]))
    return gaps, findings, stale


def hook_script(command):
    """Имя файла хука из команды раскладки: «python3 <путь> --hook ...». Пустая
    строка значит, что команда на хук devkit не похожа вовсе."""
    parts = (command or "").split()
    return os.path.basename(parts[1]) if len(parts) > 1 else ""


def hook_tree(command):
    """Дерево devkit, из которого зовётся команда хука, и имя файла хука. Пустая
    пара значит, что команда на хук devkit не похожа вовсе: у своего скрипта
    человека каталога hooks на пути может и не быть."""
    for token in (command or "").split():
        head, sep, name = token.rpartition("/hooks/")
        if sep and head and name and "/" not in name:
            return head, name
    return "", ""


def home_short(path):
    """Путь с ~ вместо домашней директории: команды хуков в настройках харнеса
    пишутся так же, как их пишет человек."""
    home = os.path.expanduser("~")
    path = str(path)
    return "~" + path[len(home):] if path.startswith(home + os.sep) else path


def stray_hooks(text, main):
    """Команды хуков, зовущие файл из чужого дерева devkit: дерево -> имена
    хуков. Судится только тот скрипт, который в выкаченном дереве есть, свои
    скрипты человека доктор не трогает."""
    try:
        data = json.loads(text or "{}")
    except ValueError:
        return {}
    if not isinstance(data, dict):
        return {}
    table = _hooks_table(data)
    if table is None:
        return {}
    want = Path(main).resolve()
    strays = {}
    for cmds in table.values():
        for cmd in cmds:
            tree, script = hook_tree(cmd)
            if not tree or not (want / "hooks" / script).exists():
                continue
            if Path(os.path.expanduser(tree)).resolve() == want:
                continue
            strays.setdefault(tree, set()).add(script)
    return strays


def stray_findings(strays, settings, main, from_main):
    """Находка про хуки не из выкаченного дерева. Правка хука в основном чекауте
    до сессий машины не доезжает вовсе, пока её не возьмёт то дерево, на которое
    смотрит настройка, и увидеть это по молчанию канала нельзя."""
    if from_main:
        how = "перенацелить: devkitctl doctor --fix"
    else:
        how = ("devkit тут выложен worktree ветки задачи, пути хуков с непроверенной ветки "
               "на машину не едут; из основного чекаута: python3 "
               "%s/tools/devkitctl/devkitctl.py doctor --fix" % main)
    return ["%s; правка хука в основном чекауте до сессий машины не доезжает, пока её не "
            "возьмёт то дерево; %s (hooks/README.md)"
            % (say.folded(("зовётся", "зовутся"), HOOK_WORD, sorted(strays[tree]), settings,
                          " из %s, а не из выкаченного дерева %s" % (tree, main)), how)
            for tree in sorted(strays)]


def repoint_hooks(settings, strays, main):
    """Перенацелить команды хуков на выкаченное дерево. Правка точечная: меняется
    только путь до каталога хуков, а событие, матчер, порядок и хвост команды
    остаются как были."""
    data, bad = perms.load(settings)
    if bad is not None:
        return []
    hooks = data.get("hooks")
    if not isinstance(hooks, dict):
        return []
    path = home_short(str(main))
    moved = []
    for groups in hooks.values():
        if not isinstance(groups, list):
            continue
        for group in groups:
            if not isinstance(group, dict):
                continue
            for h in group.get("hooks") or []:
                if not isinstance(h, dict):
                    continue
                cmd = h.get("command") or ""
                tree, script = hook_tree(cmd)
                if not tree or tree not in strays or script not in strays[tree]:
                    continue
                h["command"] = cmd.replace("%s/hooks/%s" % (tree, script),
                                           "%s/hooks/%s" % (path, script), 1)
                moved.append(script)
    if not moved:
        return []
    tmp = settings.with_name(settings.name + ".devkit-tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(str(tmp), str(settings))
    n = len(moved)
    return ["%s %s в %s на выкаченное дерево %s: %s"
            % ("перенацелен" if n == 1 else "перенацелено", say.counted(n, HOOK_WORD),
               settings, path, ", ".join(sorted(set(moved))))]


def drop_hooks(hooks, names):
    """Убрать из раскладки строки переименованных хуков. Отставная строка зовёт
    файл, которого в чекауте нет, и оставить её рядом с новой значит поменять
    молчание канала на ругань харнеса каждым ходом. Пустая группа после уборки
    уходит следом: матчер без команд ничего не значит, а в файле мозолит глаз."""
    done = []
    for event, groups in list(hooks.items()):
        if not isinstance(groups, list):
            continue
        for group in list(groups):
            if not isinstance(group, dict) or not isinstance(group.get("hooks"), list):
                continue
            kept = [h for h in group["hooks"]
                    if not (isinstance(h, dict) and hook_script(h.get("command")) in names)]
            if len(kept) == len(group["hooks"]):
                continue
            done.append(event)
            group["hooks"] = kept
            if not kept:
                groups.remove(group)
        if event in done and not groups:
            hooks.pop(event, None)
    return done


def install_hooks(settings, gaps, devkit, stale=()):
    """Дописать недостающие хуки в настройки харнеса и убрать отставные. Правка
    additive, как у прав: чужие группы и порядок остаются, команда встаёт в
    группу со своим матчером либо заводит её. Отдаёт строки о сделанном."""
    data, bad = perms.load(settings)
    if bad is not None:
        return []
    path = home_short(devkit)
    hooks = data.setdefault("hooks", {})
    if not isinstance(hooks, dict):
        # Структурно необычный hooks раскладкой чинить нельзя: doctor --fix на
        # таком даёт находку раньше, но и прямой вызов не должен ронять стек.
        return []
    done = []
    for event, matcher, tpl in gaps:
        cmd = tpl % path
        groups = hooks.setdefault(event, [])
        group = None
        for g in groups:
            if isinstance(g, dict) and (g.get("matcher") or "") == matcher:
                group = g
                break
        if group is None:
            group = {"matcher": matcher} if matcher else {}
            group["hooks"] = []
            groups.append(group)
        group.setdefault("hooks", []).append({"type": "command", "command": cmd})
        done.append((os.path.basename(tpl.split()[1]), event))
    retired = drop_hooks(hooks, set(stale))
    settings.parent.mkdir(parents=True, exist_ok=True)
    tmp = settings.with_name(settings.name + ".devkit-tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(str(tmp), str(settings))
    said = []
    if retired:
        said.append("убрано из %s: %s, хук переименован в %s"
                    % (settings, ", ".join(sorted(set(stale))),
                       ", ".join(RETIRED_HOOKS[n] for n in sorted(set(stale)))))
    if not done:
        return said
    # Уведомитель висит на пяти событиях сразу, и пять строк про него это
    # одна и та же новость: хуки подключены. Поэтому события собираются к своему
    # скрипту, а строка остаётся одна на всю раскладку.
    events = {}
    for script, event in done:
        events.setdefault(script, []).append(event)
    what = "; ".join("%s на %s" % (s, ", ".join(e)) for s, e in events.items())
    n = len(done)
    return said + ["%s %s в %s: %s" % ("включён" if n == 1 else "включено",
                                       HOOK_WORD[0] if n == 1 else say.counted(n, HOOK_WORD),
                                       settings, what)]


def watchdog_gap(text, settings):
    """Стоит ли в настройках харнеса env-ключ ретрай-вотчдога. Возврат
    (пробел, находка): пробел чинит install_watchdog, находка без пробела
    значит, что вписать ключ некуда и файл правится руками."""
    try:
        data = json.loads(text or "{}")
    except ValueError:
        # Про нечитаемый файл говорят проверки того же файла рубежом раньше,
        # вторая строка о том же не нужна.
        return False, ""
    if not isinstance(data, dict):
        return False, ""
    env = data.get("env")
    if isinstance(env, dict) and WATCHDOG_KEY in env:
        return False, ""
    if env is not None and not isinstance(env, dict):
        return False, ("секция env в %s не объект json: ключ %s вписать некуда, поправить "
                       "руками (hooks/README.md, «Возобновление после обрыва связи»)"
                       % (settings, WATCHDOG_KEY))
    return True, ("в %s нет env-ключа %s: недокументированный задел ретрай-вотчдога "
                  "харнеса, стенд DK-172 разницы в поведении с ним не нашёл, но вреда от "
                  "лишнего ключа тоже нет; вписать: devkitctl doctor --fix (hooks/README.md, "
                  "«Возобновление после обрыва связи»)" % (settings, WATCHDOG_KEY))


def install_watchdog(settings):
    """Вписать env-ключ ретрай-вотчдога в настройки харнеса. Правка additive,
    как у хуков и прав: чужие ключи и порядок остаются."""
    data, bad = perms.load(settings)
    if bad is not None:
        return []
    env = data.setdefault("env", {})
    if not isinstance(env, dict) or WATCHDOG_KEY in env:
        return []
    env[WATCHDOG_KEY] = WATCHDOG_VALUE
    settings.parent.mkdir(parents=True, exist_ok=True)
    tmp = settings.with_name(settings.name + ".devkit-tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(str(tmp), str(settings))
    return ["вписан env-ключ %s=%s в %s: недокументированный задел ретрай-вотчдога харнеса "
            "(hooks/README.md, «Возобновление после обрыва связи»)"
            % (WATCHDOG_KEY, WATCHDOG_VALUE, settings)]


def check_notify_hook(fix=False):
    findings = []
    # Выбор бэкенда живёт в самом уведомителе, второй его копии тут нет.
    src = DEVKIT / "hooks" / NOTIFY_HOOK
    spec = importlib.util.spec_from_file_location("devkit_notify", src)
    if not src.exists() or spec is None:
        return findings, []
    mod = importlib.util.module_from_spec(spec)
    # Уведомитель берёт разбор входа из соседнего hookio, а загрузка по пути его
    # директорию в sys.path не кладёт.
    sys.path.insert(0, str(src.parent))
    try:
        spec.loader.exec_module(mod)
    except (OSError, SyntaxError, ImportError):
        return findings, []
    finally:
        sys.path.pop(0)
    backend = mod.pick_backend()
    if backend:
        # Слать есть чем, но клик по баннеру уводит в Finder. Отправителя с
        # переходом ставит доводка: человек, позвавший установку, за пакетом на
        # эту мелочь не пойдёт, а без перехода уведомления наполовину бесполезны.
        if sys.platform == "darwin" and os.path.basename(backend) == "osascript":
            return ensure_package("terminal-notifier",
                                  "уведомления идут, но клик по баннеру ведёт не в окно сессии: "
                                  "osascript постит от имени Script Editor, и клик открывает "
                                  "Finder; переход даёт terminal-notifier", fix)
        return findings, []
    if sys.platform == "darwin":
        how = "на macOS шлём terminal-notifier или osascript, а их нет в PATH"
    elif sys.platform.startswith("linux"):
        how = "notify-send не в PATH, ставится пакетным менеджером (apt install libnotify-bin)"
    else:
        how = "бэкенда под платформу %s в %s пока нет" % (sys.platform, NOTIFY_HOOK)
    findings.append("уведомлять нечем: %s; проверка канала: python3 %s --self-test" % (how, src))
    return findings, []


def contour_paths(name, profile, homes):
    """Куда раскладывается машинное хозяйство харнеса: (пути по осям, находки).

    Три оси и три ключа профиля, четвёртая (глобальная точка правил) своя у
    rules.check_global. Пустой ключ значит, что оси у инструмента нет, и это не
    пробел: раскладывать ему по этой оси просто нечего.
    """
    paths, bads = {}, []
    for axis, section, key in (AXIS_HOOKS, AXIS_AGENTS, AXIS_SKILLS):
        path, bad = rules.harness_path(name, profile.str_of(section, key), homes)
        paths[axis] = path
        if bad and bad not in bads:
            # Пробел у трёх осей один и тот же (машинного каталога нет), и
            # сказать про него один раз честнее, чем трижды.
            bads.append(bad)
    return paths, bads


def check_harness_contour(name, profile, homes, fix, main, from_main):
    """Машинное хозяйство одного включённого харнеса: хуки, права сессии без
    человека, определения агентов и скиллы. Пути берутся из профиля ([hooks]
    config, [delegate] agents_dir, [skills] dir), а не из констант ~/.claude:
    вторая подписка это та же раскладка того же инструмента, только в своём
    каталоге, и обслуживать её надо тем же контуром.

    Возврат это (находки, починенное, стоит ли хук SessionStart).
    """
    findings, fixed = [], []
    paths, bads = contour_paths(name, profile, homes)
    if bads:
        return bads, [], False
    settings, text = paths[AXIS_HOOKS[0]], ""
    if settings is not None:
        text = settings.read_text(encoding="utf-8") if settings.exists() else ""
        # Хуки харнеса раскладываются тем же рубежом, что права и скиллы: с ветки
        # задачи на машину они не едут, там их сверяют и чинят из основного чекаута.
        gaps, gap_findings, stale = hook_gaps(text, settings)
        if (gaps or stale) and fix and from_main:
            fixed += install_hooks(settings, gaps, main, stale)
            text = settings.read_text(encoding="utf-8") if settings.exists() else ""
        else:
            findings += gap_findings
        # Дерево, из которого хук зовётся, тем же рубежом: строка с путём чужого
        # дерева выглядит подключённым хуком, а работает там своя копия файла, и
        # правка в основном чекауте до машины не доезжает вовсе (DK-582).
        strays = stray_hooks(text, main)
        if strays and fix and from_main:
            fixed += repoint_hooks(settings, strays, main)
            text = settings.read_text(encoding="utf-8") if settings.exists() else ""
        elif strays:
            findings += stray_findings(strays, settings, main, from_main)
        # Ретрай-вотчдог тем же файлом и тем же рубежом from_main: ключ виден
        # каждой сессии на машине сразу, и ехать туда с непроверенной ветки ему
        # нельзя так же, как хукам и правам.
        wgap, wfinding = watchdog_gap(text, settings)
        if wgap and fix and from_main:
            fixed += install_watchdog(settings)
            text = settings.read_text(encoding="utf-8") if settings.exists() else ""
        elif wfinding:
            findings.append(wfinding)
        pf, pd = perms.check(settings, fix, None if from_main else main)
        findings += pf
        fixed += pd
        # Литеральные токены в allow-правилах и резервные копии settings.json:
        # тот же файл, тем же рубежом. Цель DK-207 держит контекст модели чистым
        # от секретов, а значение в теле правила ездит в сессию как любая строка.
        lf, ld = leak.check(settings, fix, None if from_main else main)
        findings += lf
        fixed += ld
    if paths[AXIS_AGENTS[0]] is not None:
        f, d = check_agent_defs(fix, paths[AXIS_AGENTS[0]])
        findings += f
        fixed += d
    if paths[AXIS_SKILLS[0]] is not None:
        f, d = check_skills(fix, paths[AXIS_SKILLS[0]])
        findings += f
        fixed += d
    return findings, fixed, SESSION_HOOK in text


def check_machine(fix):
    # Машинный контур, общий для всех проектов: хуки харнесов, права сессии
    # без человека, бинари devkit, определения агентов, скиллы, глобальная точка
    # правил, tmux и снимок квоты. Хозяйство раскладывается каждому включённому
    # харнесу по его профилю, а машинное на всех одно.
    findings, fixed = [], []
    main, from_main = devkit_checkout()
    devkit_src = main if (main / "kit" / "harness").is_dir() else DEVKIT
    profiles, hf = rules.enabled_harnesses(None, devkit_src / "kit" / "harness")
    findings += hf
    homes = rules.machine_homes()
    session_hook = False
    for name, profile in profiles:
        f, d, took = check_harness_contour(name, profile, homes, fix, main, from_main)
        findings += f
        fixed += d
        session_hook = session_hook or took
    # Уведомлять есть чем или нет, это свойство машины, а не харнеса: бэкенд
    # один на всех, и спрашивать про него по разу на харнес значило бы
    # повторять одну находку.
    nf, nd = check_notify_hook(fix)
    findings += nf
    fixed += nd
    # Носитель сторожка цикла цели: тем же рубежом, что хуки и права, потому
    # что launchd-агент показывает на чекаут, и с ветки задачи ему на машину
    # ехать нельзя.
    watchf, watchd = watch.check(fix, main, from_main)
    findings += watchf
    fixed += watchd
    f, d = check_binaries(fix)
    findings += f
    fixed += d
    # Носитель дашборда после бинарей: агент показывает на бинарь dashboard из
    # PATH, и на прогоне с --fix тот успевает встать на место строкой выше.
    dashf, dashd = dashboard.check(fix, main, from_main)
    findings += dashf
    fixed += dashd
    # Обёртка devkitctl рядом с бинарями: без неё python-часть зовётся длинным
    # путём, а кладут её одинаково update и доктор.
    wf, wd = update.check_wrapper(main, fix, from_main)
    findings += wf
    fixed += wd
    # Про вышедший релиз машине потребителя говорит только доктор: git pull в
    # отвязанном чекауте не работает, и другого канала у неё нет.
    findings += update.check_release(main)
    whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, класть на машину "
                                   "правила с непроверенной ветки нельзя; из основного чекаута %s: "
                                   % main)
    gf, gd = rules.check_global(devkit_src, fix and from_main, whence=whence)
    findings += gf
    fixed += gd
    if not shutil.which("tmux"):
        tf, td = ensure_package("tmux", "tmux не в PATH: agentctl quota refresh не снимет "
                                        "панель /usage, и корректор pick останется без снимка", fix)
        findings += tf
        fixed += td
    # Снимок квоты снимает хук SessionStart при первой же сессии, и мешает ему
    # только нехватка tmux. Пока и хук на месте, и tmux есть, отсутствие снимка
    # на свежей машине это не пробел, а ещё не наступившее событие.
    if not session_hook:
        blocked = "хук SessionStart, который его снимает, не подключён"
    elif not shutil.which("tmux"):
        blocked = "хук SessionStart снимает его панелью /usage, а tmux на машине нет"
    else:
        blocked = ""
    f, d = check_quota(fix, blocked)
    findings += f
    fixed += d
    f, d = check_alt_sub(fix)
    findings += f
    fixed += d
    # Нехватку машинного каталога видят и раскладка хозяйства, и глобальная
    # точка правил: пробел один, и повторять его человеку незачем.
    return list(dict.fromkeys(findings)), fixed


def check_quota(fix, blocked=""):
    # Снимки квоты: переезд одиночного файла в директорию и возраст каждого
    # снимка. Директория заведена по харнесу, потому что лимиты у инструментов
    # свои, и одним файлом их не описать. blocked это причина, по которой хук
    # SessionStart снимок сам не снимет; пустая причина значит снимет, и звать
    # человека снимать его руками тогда незачем.
    findings, fixed = [], []
    quota_dir = Path(os.path.expanduser(QUOTA_DIR))
    legacy = Path(os.path.expanduser(QUOTA_LEGACY))
    moved = quota_dir / ("%s.local" % QUOTA_LEGACY_HARNESS)
    if legacy.exists():
        if moved.exists():
            # Переезд уже сделан, а старый файл остался: удалять его --fix не
            # вправе (правки строго additive), но молчать про два снимка нельзя.
            findings.append("старый снимок квоты %s лежит рядом с новым %s: читается новый, "
                            "старый убрать руками (rm %s)" % (legacy, moved, legacy))
        elif fix:
            quota_dir.mkdir(parents=True, exist_ok=True)
            legacy.replace(moved)
            fixed.append("снимок квоты переехал из %s в %s" % (legacy, moved))
        else:
            findings.append("снимок квоты лежит по старому пути %s: снимок стал директорией по файлу "
                            "на харнес; переложить: devkitctl doctor --fix (в %s)" % (legacy, moved))
    snaps = sorted(quota_dir.glob("*.local")) if quota_dir.is_dir() else []
    if not snaps and not legacy.exists() and blocked:
        findings.append("нет снимка квоты в %s, и сам он не появится: %s; пока его нет, "
                        "корректор pick вердикт двигать не будет, снять руками: "
                        "agentctl quota refresh" % (quota_dir, blocked))
    for quota in snaps:
        taken = quota_taken(quota)
        if taken is None:
            findings.append("в снимке квоты %s не разобран момент снятия (строка taken =), "
                            "возраст не проверить; переснять: agentctl quota refresh" % quota)
            continue
        age = (datetime.now() - taken).total_seconds()
        if age > QUOTA_MAX_AGE:
            findings.append("снимок квоты %s протух (возраст %s при пороге %s): профицит по нему уже не считается, "
                            "сдвиг вверх потерян; переснять: agentctl quota refresh"
                            % (quota, human_age(age), human_age(QUOTA_MAX_AGE)))
    return findings, fixed


def check_alt_sub(fix):
    # Конфиг второй подписки в машинном слое: без него ни подпроцесс
    # делегирования не поднимется, ни окно shipctl code не откроется, а с
    # недописанным endpoint сессия молча уйдёт на первую подписку, и промах виден
    # только по счёту в конце недели. Раскладку каталога делает --fix, значения
    # оставляет пользователю: endpoint и токен берутся в кабинете подписки, и
    # автоматике их взять неоткуда.
    #
    # Каталога в машинном слое нет, значит второй подписки на этой машине нет
    # вовсе, и говорить про неё нечего: звать вписывать endpoint того, чего у
    # человека не заведено, значит выдумать ему работу.
    findings, fixed = [], []
    home = rules.machine_homes().get(ALT_SUB_HARNESS)
    if not home:
        return [], []
    d = Path(home)
    settings = d / ALT_SUB_SETTINGS
    doc, env, broken = {}, {}, ""
    if settings.exists():
        try:
            doc = json.loads(settings.read_text(encoding="utf-8"))
        except ValueError as e:
            broken = "не читается как json (%s)" % e
        if not broken and not isinstance(doc, dict):
            broken = "лежит не объектом json"
        if not broken:
            env = doc.get("env") or {}
            if not isinstance(env, dict):
                broken = "держит секцию env не объектом json"
        if broken:
            # Чинить содержимое --fix не берётся: там мог остаться дописанный
            # руками токен, и переписать файл значило бы его потерять.
            return ["конфиг второй подписки %s %s: окно shipctl code не откроется; поправить "
                    "руками либо снести файл и переразложить болванку (devkitctl doctor --fix)"
                    % (settings, broken)], []
    missing = [k for k in ALT_SUB_KEYS if k not in env]
    if missing and not fix:
        return ["%s: окно на второй подписке (shipctl code) не откроется, разложить болванку: "
                "devkitctl doctor --fix, дальше вписать в неё endpoint и токен"
                % ("нет конфига второй подписки %s" % settings if not settings.exists() else
                   "в конфиге второй подписки %s нет ключей %s" % (settings, ", ".join(missing)))], []
    if missing:
        for key in missing:
            env[key] = ""
        doc["env"] = env
        # Права сужаются сразу при раскладке, а не проверкой ниже: свежая
        # болванка иначе отчитывалась бы о починке того, что сама и завела.
        d.mkdir(parents=True, exist_ok=True, mode=0o700)
        settings.write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        settings.chmod(0o600)
        fixed.append("разложена болванка конфига второй подписки %s (ключи %s), "
                     "права сужены: там будет лежать токен" % (settings, ", ".join(missing)))
    # В файле лежит токен, и читать его посторонним ни к чему. Права сужаются
    # и на каталоге: settings.json в нём не единственный, клиент кладёт рядом
    # своё.
    for path, want in ((d, 0o700), (settings, 0o600)):
        mode = path.stat().st_mode & 0o777
        if not mode & 0o077:
            continue
        if fix:
            path.chmod(want)
            fixed.append("сужены права %s до %o: там лежит токен второй подписки" % (path, want))
        else:
            findings.append("у %s права %o: там лежит токен второй подписки, сузить: "
                            "devkitctl doctor --fix (chmod %o %s)" % (path, mode, want, path))
    empty = [k for k in ALT_SUB_KEYS if not str(env.get(k) or "").strip()]
    if empty:
        findings.append("в конфиге второй подписки %s пустые ключи %s: пока они пусты, окно "
                        "shipctl code либо не откроется, либо уйдёт на первую подписку молча; "
                        "вписать значения второй подписки (в devkit они не едут, машинный слой "
                        "лежит вне репозиториев)" % (settings, ", ".join(empty)))
    return findings, fixed


def window_env(settings):
    # Секция env из settings.json: и у машинного слоя, и у копии окна она одна
    # и та же. Возврат это (документ целиком, env, беда). Документ нужен потому,
    # что рядом с env лежит написанное человеком (разрешения сессии), и правка
    # ключей подписки его не трогает; битый файл разбирать наугад нельзя, там мог
    # остаться вписанный руками токен.
    if not settings.exists():
        return {}, {}, ""
    try:
        doc = json.loads(settings.read_text(encoding="utf-8"))
    except ValueError as e:
        return {}, {}, "не читается как json (%s)" % e
    if not isinstance(doc, dict):
        return {}, {}, "лежит не объектом json"
    env = doc.get("env") or {}
    if not isinstance(env, dict):
        return {}, {}, "держит секцию env не объектом json"
    return doc, env, ""


def check_window_copy(root, fix):
    # Окружение копии окна против машинного слоя. Копии у проекта может и не
    # быть (окном второй подписки работают не везде), и тогда говорить не о чем;
    # доктор, позванный из самой копии, проверяет её же, а не соседа с тем же
    # суффиксом.
    home = rules.machine_homes().get(ALT_SUB_HARNESS)
    if not home:
        return [], []
    root = Path(root)
    tail = "-" + WINDOW_SUFFIX
    copy = root if root.name.endswith(tail) else root.parent / (root.name + tail)
    # .git у линкованного дерева это файл с редиректом: без него рядом лежит
    # просто одноимённая директория, и писать в неё доктору нечего.
    if not (copy / ".git").exists():
        return [], []
    machine = Path(home) / ALT_SUB_SETTINGS
    _, machine_env, broken = window_env(machine)
    if broken or not machine_env:
        # Про машинный слой говорит check_alt_sub, и повторять его находку
        # второй строкой незачем.
        return [], []
    want = {k: machine_env[k] for k in ALT_SUB_KEYS if str(machine_env.get(k) or "").strip()}
    want[WINDOW_HARNESS_KEY] = ALT_SUB_HARNESS
    settings = copy / WINDOW_ENV_FILE
    doc, env, broken = window_env(settings)
    if broken:
        return ["окружение копии окна %s %s: окно уйдёт на первую подписку молча; поправить "
                "руками либо убрать файл, его переразложит shipctl code" % (settings, broken)], []
    stale = sorted(k for k, v in want.items() if env.get(k) != v)
    findings, fixed = [], []
    if stale and not fix:
        gap = ("в копии окна %s нет окружения второй подписки" % copy if not settings.exists() else
               "окружение копии окна %s разошлось с машинным слоем %s по ключам %s"
               % (settings, machine, ", ".join(stale)))
        findings.append("%s: окно в этой копии ходит по старым ключам, а то и на первую "
                        "подписку молча; переписать: devkitctl doctor --fix" % gap)
    elif stale:
        env.update(want)
        doc["env"] = env
        settings.parent.mkdir(parents=True, exist_ok=True)
        settings.write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        settings.chmod(0o600)
        fixed.append("окружение копии окна %s переписано из машинного слоя (ключи %s)"
                     % (settings, ", ".join(stale)))
    if settings.exists():
        mode = settings.stat().st_mode & 0o777
        if mode & 0o077 and fix:
            settings.chmod(0o600)
            fixed.append("сужены права %s до 600: там лежит токен второй подписки" % settings)
        elif mode & 0o077:
            findings.append("у %s права %o: там лежит токен второй подписки, сузить: "
                            "devkitctl doctor --fix (chmod 600 %s)" % (settings, mode, settings))
    return findings, fixed


def corp_profiles(local):
    # Тонкие файлы харнесов корп-клона считаются по конфигу боковой директории:
    # там лежит .devkit и там же решается, какие харнесы включены.
    profiles, findings = rules.enabled_harnesses(local, DEVKIT / "kit" / "harness")
    imports = [(n, p) for n, p in profiles if rules.mode_of(p) == "import"]
    return imports, findings


def corp_thin_names(imports):
    return [p.str_of("rules", "file") for _, p in imports]


def corp_thin(clone, local, imports, fix):
    # Тонкий файл харнеса обязан лежать в корне клона, иначе харнес его не
    # прочитает, а весь текст живёт в боковой директории: проверка та же, что у
    # домашнего проекта, только AGENTS.md берётся оттуда.
    findings, fixed = [], []
    board = (Path(local) / "docs" / "TASKS.md").exists()
    depths = {name: rules.actual_depth(DEVKIT, local, board, rules.declared_depth(profile)[0])
              for name, profile in imports}
    # Наружу у клона ведут оба импорта, и оба зовутся через свою ссылку в
    # .devkit клона: дерево devkit и боковая директория с AGENTS.md.
    texts = [rules.thin_text(profile, Path(clone), DEVKIT, board, False, depths[name],
                             agents_root=Path(local))
             for name, profile in imports]
    lf, ld = rules.check_links(Path(clone), texts,
                               rules.thin_links(clone, DEVKIT, agents_root=local), fix)
    findings += lf
    fixed += ld
    for name, profile in imports:
        tf, td = rules.check_thin(name, profile, Path(clone), DEVKIT, board, False, depths[name],
                                  fix, agents_root=Path(local))
        findings += tf
        fixed += td
    return findings, fixed


def check_machine_ignore(root, fix):
    """Машинные записи .devkit в .gitignore (cmdout/, ship.lock, goal-*).

    Список MACHINE_IGNORE_ENTRIES общий с scaffold_machine_gitignore:
    подключение нового проекта (new, corp) раскладывает записи тем же списком, а
    здесь доктор подхватывает проект, подключённый до их появления. Проверка зовётся из блока in_git, а не
    из блока доски: cmdout и shipctl работают и в проекте без доски. Путь
    cmdout/ сверяется со слэшом: без него git check-ignore по отсутствующему на
    диске каталогу ответ не подтверждает, и выходит ложная находка.
    """
    findings, fixed = [], []
    for path, comment, why in MACHINE_IGNORE_ENTRIES:
        rc, _ = run(["git", "-C", str(root), "check-ignore", "-q", path])
        if rc != 0:
            if fix and ensure_gitignore(root, path, comment):
                fixed.append(".gitignore: добавлен %s" % path)
            else:
                findings.append("%s не гитигнорнут: %s, добавить %s в .gitignore"
                                % (path, why, path))
    return findings, fixed


def check_cmdout(root, fix):
    """Скопление устаревших выводов в .devkit/cmdout.

    Порог возраста и правило удаления живут в internal/frame, и доктор их не
    дублирует: он наблюдает сухой прогон «cmdout clean --dry-run», который
    списком отдаёт каталоги, подпавшие под порог. Список непустой это находка,
    а «cmdout clean» подпроцессом чинит её. Так порог в одном языке (Go), а не
    в двух. Бинаря cmdout нет или нет .devkit/cmdout, доктор молчит: про бинарь
    уже говорит машинный раздел, а пустой каталог чистить нечего.
    """
    cmdout = shutil.which("cmdout")
    if not cmdout or not (Path(root) / ".devkit" / "cmdout").is_dir():
        return [], []
    rc, out = run([cmdout, "clean", "--dry-run"], cwd=str(root))
    if rc != 0:
        return [], []
    stale = [ln for ln in out.splitlines() if ln.strip()]
    if not stale:
        return [], []
    if fix:
        rc, _ = run([cmdout, "clean"], cwd=str(root))
        if rc == 0:
            return [], ["почищено %d устаревших выводов в .devkit/cmdout" % len(stale)]
        return ["cmdout clean не прошёл; почистить руками"], []
    return [".devkit/cmdout скопилось %d устаревших выводов; почистить: cmdout clean"
            % len(stale)], []


def check_prose_config():
    """Конфиг порогов сторожа прозы (DK-521).

    Смотрит не доктор, а сам хук режимом --config: перечень метрик и их ключи
    живут в hooks/check-prose.py, и второй список тут разъехался бы с ним на
    первой же новой метрике. Выход 1 это неполный конфиг, и хук печатает
    строками, чего в нём нет. Без конфига сторож молчит на каждой записи, и
    отличить это молчание от чистой прозы можно только отсюда.
    """
    hook = DEVKIT / "hooks" / PROSE_HOOK
    if not hook.is_file():
        return []
    rc, out = run([sys.executable, str(hook), "--config"])
    if rc == 0:
        return []
    # В выводе хука первая строка это шапка, а пробелы идут пунктами перечня:
    # находке нужны пункты, шапку доктор говорит своими словами.
    lines = [ln.strip()[2:] for ln in out.splitlines() if ln.strip().startswith("- ")]
    return ["сторож прозы молчит на каждой записи, %s (hooks/README.md)"
            % ("; ".join(lines) if lines else "конфиг порогов не читается")]


def check_map_freshness(root, fix=False):
    """Проверка свежести карты проекта (DK-375).

    Возвращает (findings, fixed): три находки из решения 7.
    """
    findings, fixed = [], []
    map_path = Path(root) / "docs" / "map.md"

    # Генерируем карту в памяти
    full_text, hash_val = codemap.render_map(root)
    _, body, _ = codemap.generate_map(root)  # Тело для сравнения

    # 1. Нет файла при живых компонентах. Индекс решений телом считается, а
    # компонентом не является: без строк карты файл не заводится.
    components = body.split("# Решения по docs/lld", 1)[0].strip()
    if not map_path.is_file():
        if components:
            if fix:
                map_path.parent.mkdir(parents=True, exist_ok=True)
                map_path.write_text(full_text, encoding="utf-8")
                fixed.append("сгенерирована карта: %s" % map_path)
                return findings, fixed
            findings.append("карты нет, компоненты есть; сгенерировать: devkitctl doctor --fix")
        return findings, fixed

    # Читаем существующий файл
    try:
        current_content = map_path.read_text(encoding="utf-8")
    except OSError:
        findings.append("карта не читается: %s" % map_path)
        return findings, fixed

    # 2. Хеш разошёлся с телом
    current_lines = current_content.split("\n")
    if current_lines:
        first_line = current_lines[0].strip()
        hash_match = re.match(r'<!--\s*devkit:generated\s+map\s+body=(\w+)\s*-->', first_line)
        if hash_match:
            current_hash = hash_match.group(1)
            if current_hash != hash_val:
                findings.append("карта правлена руками, хеш разошёлся; перегенерировать: "
                               "devkitctl doctor --fix")
                if fix:
                    map_path.write_text(full_text, encoding="utf-8")
                    fixed.append("перегенерирована карта: %s" % map_path)
                return findings, fixed
        else:
            # Файл есть, но маркера нет - считаем, что он правлен руками
            findings.append("карта без маркера, правлена руками; перегенерировать: "
                           "devkitctl doctor --fix")
            if fix:
                map_path.write_text(full_text, encoding="utf-8")
                fixed.append("перегенерирована карта: %s" % map_path)
            return findings, fixed

    # 3. Тело разошлось с деревом
    # Сравниваем полный файл (без первой строки-маркера) с ожидаемым полным текстом (без маркера)
    current_full = "\n".join(current_lines[1:]) if len(current_lines) > 1 else ""
    expected_full = "\n".join(full_text.split("\n")[1:])  # Без маркера

    if current_full != expected_full:
        findings.append("карта разошлась с деревом; перегенерировать: devkitctl doctor --fix")
        if fix:
            map_path.write_text(full_text, encoding="utf-8")
            fixed.append("перегенерирована карта: %s" % map_path)

    return findings, fixed


def doctor(start, fix=False):
    findings, fixed = [], []
    root, in_git = project_root(start)
    local = None
    release_self = False
    if not in_git:
        # Без репозитория нет ни проекта, ни обвязки: проектная половина
        # (правила, git-хуки, доска, обвязка выката, ссылки) по дереву не
        # ходит, а не то дальше вниз лежат чужие репозитории (каталог
        # проектов), и обход их доктором был бы шумом их находками, а не
        # диагностикой (DK-160). Машинный контур ниже от root не зависит и
        # печатается как обычно: человек, позвавший доктора из каталога
        # проектов, часто спрашивает ровно про машину.
        findings.append("не git-репозиторий: %s; звать из подключённого проекта "
                        "либо из чекаута devkit" % root)
    else:
        clone, local = corp.pair(root, DEVKIT)
        if local:
            # Рабочие файлы devkit в корп-контуре лежат не в дереве проекта, а
            # рядом, и обычные проверки идут по ним: в самом клоне ни доски, ни
            # AGENTS.md нет и быть не должно.
            root = Path(local)
            imports, hf = corp_profiles(local)
            findings += hf
            cf, cd = corp.check(clone, local, DEVKIT, corp_thin_names(imports), fix)
            findings += cf
            fixed += cd
            if corp.local_dir(clone, DEVKIT):
                tf, td = corp_thin(clone, local, imports, fix)
                findings += tf
                fixed += td
        # Вклейка правил сверяется всегда: она разбирает содержимое самого
        # чекаута (сошлась ли AGENTS.md с RULES-файлами), и на исправном
        # релизе молчит сама, а на сломанном это дефект самого выпуска, о нём
        # стоит узнать и потребителю. На релизном чекауте самого devkit
        # (детач на теге v*, машина потребителя из CONNECT.md) молчат только
        # находки ниже: git-хуки нужны для коммита, которого потребитель не
        # делает. Режим чекаута доктор уже различает и печатает строкой ниже
        # (DK-149, решение 3), а на ветке (машина разработчика) признак ложный
        # и состав проверок не меняется.
        # Свежесть карты идёт раньше тонких файлов: её импорт входит в тонкий
        # файл, и сгенерированная в этом же прогоне карта должна попасть в него
        # сразу, а не со второго прогона doctor.
        cf, cd = check_map_freshness(root, fix)
        findings += cf
        fixed += cd
        rfindings, rfixed = rules.check(root, DEVKIT, fix, SKIP_DIRS)
        findings += rfindings
        fixed += rfixed
        wf, wd = check_window_copy(root, fix)
        findings += wf
        fixed += wd
        release_self = Path(root).resolve() == DEVKIT.resolve() and update.on_release(DEVKIT)
        if not release_self and check_git_hooks(root):
            if fix:
                done, residual = connect_git_hooks(root)
                (fixed if done else findings).append(done or residual)
            else:
                findings.append(check_git_hooks(root))
    # Профили харнесов проверяются все, а не только активный: битый профиль
    # находится до того, как кто-то на него переключится, а починить его
    # автоматике нечем, это правка в devkit.
    findings += harness.check_profiles(str(DEVKIT / "kit" / "harness"))
    # Конфиг порогов прозы того же формата и той же судьбы: чинится он правкой
    # в devkit, а не автоматикой, поэтому идёт находкой рядом с профилями.
    findings += check_prose_config()
    # Вес резидента считается и проекту (DK-190): карманы одни и те же, а судятся
    # в них разные. В чекауте devkit это его собственные карманы (ядро, ядро
    # доски, итог) и тело скилла, всё общее для всех проектов и проекту не
    # чинимое. В подключённом проекте это его проектная часть, AGENTS.md со своим
    # тонким файлом: рукописный CLAUDE.md, оставшийся от подключения до переезда,
    # весит десятки тысяч символов и едет в каждый запрос сессии. Таблица
    # печатается в обоих случаях, иначе распухание видно единственный день, тот,
    # в который карман переполз через порог. На релизном чекауте devkit таблица
    # читается потребителем как шум: бюджет контекста считает тот, кто пишет
    # скиллы и правила, не тот, кто devkit только поставил.
    if in_git and not release_self:
        own = Path(root).resolve() == DEVKIT.resolve()
        wlines, wfindings = weigh.pockets_report(root, DEVKIT, project=not own)
        for ln in wlines:
            print(ln)
        findings += wfindings
        if own:
            findings += weigh.skill_findings(DEVKIT)
        # Импорты файла правил проекта: полный текст вместо ядра и импорт,
        # который лежит на диске, а до контекста не доезжает.
        findings += rules.check_project_imports(root, DEVKIT)
    # Раскладка проверяется только на самом devkit: в чужом проекте она своя.
    findings += ["раскладка: %s" % m for m in layout.check(root)]
    # Карта переходов работы тоже документ самого devkit (DK-400, решение 8):
    # сторож проверяет полноту упоминаний скиллов, статусов доски и рубежей,
    # а сам текст пишется руками и автоматике не принадлежит.
    findings += workflow.check(root)
    # Режим чекаута называется всегда, а не только рядом с находкой: на машине
    # разработчика бинарь впереди последнего релиза это норма, на машине
    # потребителя поломка, и по одному номеру версии одно от другого не отличить.
    print("чекаут devkit: %s" % update.checkout_mode(devkit_checkout()[0]))
    mfindings, mfixed = check_machine(fix)
    findings += ["машина: %s" % m for m in mfindings]
    fixed += mfixed
    if in_git and not release_self and (root / "docs" / "TASKS.md").exists():
        tc = shutil.which("taskctl")
        if tc:
            # Про отсутствие бинаря уже сказал машинный раздел, тут только lint.
            rc, out = run([tc, "-C", str(root), "lint"])
            if rc != 0:
                findings.append("taskctl lint: %s" % out)
        # Связи, названные входом в файле задачи, обязаны стоять маркером
        # «после», иначе диспетчер их не видит (DK-168). Разбор свой, а не
        # через taskctl: находка нужна и там, где бинаря в PATH нет.
        findings += board.check(root)
        # Линкованное дерево (worktree от shipctl start) отличается тем же
        # способом, что check_agent_defs различает исполнение из ветки задачи:
        # main_checkout это родитель git-common-dir, from_main_checkout ложно,
        # когда root не совпадает с ним. Конфиг выката логически принадлежит
        # основному чекауту (.devkit гитигнорнута и не переезжает в worktree),
        # и весь блок (чтение, заведение болванки, дописывание ключей, разбор
        # пустых deploy=/test=) стоит только на нём: замечание ревью DK-463
        # показало, что gate только на scaffold_deploy не спасает от находок,
        # когда deploy.local в worktree уже физически лежит (доехал с прежнего
        # бага либо положен руками): блок целиком не выполняется вне основного
        # чекаута, независимо от того, есть там файл или нет.
        main_checkout = corp.checkout(root)
        from_main_checkout = not main_checkout or Path(main_checkout).resolve() == Path(root).resolve()
        # Боковая директория корп-контура лежит подкаталогом репозитория контура
        # (DK-583), и родитель git-common-dir там не она: линкованным деревом
        # она от этого не становится, конфиг выката её собственный.
        from_main_checkout = from_main_checkout or bool(local)
        if from_main_checkout:
            deploy, test, autonomous = read_deploy(root)
            if deploy is None and fix:
                fixed += scaffold_deploy(root)  # заводит файл и строку в .gitignore
                deploy, test, autonomous = read_deploy(root)  # теперь файл есть, команды пустые
            elif deploy is not None and fix:
                patched = patch_deploy(root)  # дописывает недостающие ключи болванки
                if patched:
                    fixed += patched
                    deploy, test, autonomous = read_deploy(root)
            if deploy is None:
                findings.append("нет %s: команда выката не задана, shipctl merge оставит "
                                "выкат пользователю (болванку заводит devkitctl new или doctor --fix)"
                                % DEPLOY_CONFIG)
            else:
                # В корп-контуре слияние и выкат ведёт процесс компании, shipctl там
                # отказывает честной строкой, и пустой deploy= это норма, а не
                # находка: требовать команду выката значило бы звать чинить исправное.
                if deploy == "" and local:
                    pass
                elif deploy == "" and not autonomous:
                    findings.append("%s: пустой deploy=, shipctl нечего выкатывать; "
                                    "вписать команду выката" % DEPLOY_CONFIG)
                elif deploy == "" and autonomous:
                    findings.append("%s: autonomous = true при пустом deploy= (агенту доверен конвейер, "
                                    "а катить нечего); вписать команду выката либо снять autonomous"
                                    % DEPLOY_CONFIG)
                if test == "":
                    findings.append("%s: пустой test=, shipctl merge будет требовать --test на каждый "
                                    "вызов, а процедура пачки сочинять его не умеет; вписать команду "
                                    "тестов проекта" % DEPLOY_CONFIG)
                findings += check_gowork(root, deploy, test)
                rc, _ = run(["git", "-C", str(root), "check-ignore", "-q", DEPLOY_CONFIG])
                if rc != 0:
                    if fix and ensure_gitignore(root, DEPLOY_IGNORE):
                        fixed.append(".gitignore: добавлен %s" % DEPLOY_IGNORE)
                    else:
                        findings.append("%s не гитигнорнут: адрес и доступы из команды выката "
                                        "утекут в git, добавить %s в .gitignore"
                                        % (DEPLOY_CONFIG, DEPLOY_IGNORE))
        if (root / ".devkit").is_dir():
            rc, _ = run(["git", "-C", str(root), "check-ignore", "-q", RUN_LOG])
            if rc != 0:
                if fix and ensure_gitignore(root, RUN_LOG, LOG_IGNORE_COMMENT):
                    fixed.append(".gitignore: добавлен %s" % RUN_LOG)
                else:
                    findings.append("%s не гитигнорнут: журнал запусков замусорит status, "
                                    "добавить %s в .gitignore" % (RUN_LOG, RUN_LOG))
    if in_git:
        findings += check_links(root)
        # Полнота доки идёт рядом с проверкой её ссылок: битая ссылка это
        # дока про отсутствующее, а тут ищется отсутствующая дока целиком
        # (DK-071). Корень без манифеста и не под git молчит: без репозитория
        # проектной половины нет вовсе.
        findings += describe.check(root)
        # Дока есть, но начинается не с того: у README утилиты первым разделом
        # идёт вход, иначе читатель, впервые видящий фичу, до запуска не
        # доходит (DK-421). Порядок разделов машиноразличим, вид текста нет,
        # и `--fix` эту находку не чинит: вход пишется руками.
        findings += entry.check(root)
        cf, cd = check_machine_ignore(root, fix)
        findings += cf
        fixed += cd
        cf, cd = check_cmdout(root, fix)
        findings += cf
        fixed += cd
    for m in fixed:
        print("починено: %s" % m)
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("обвязка в порядке")
    return 0


def layout_only(start):
    """Одна проверка раскладки, без машинной обвязки.

    Полный doctor смотрит на машинное хозяйство харнесов, собранные бинари, tmux
    и снимок квоты, и в CI такой прогон дал бы десяток находок про
    ненастроенную машину. Тут
    печатаются только находки раскладки, и код выхода стоит по ним.
    """
    root, in_git = project_root(start)
    if not in_git:
        sys.stderr.write("не git-репозиторий: %s\n" % root)
        return 2
    if not layout.is_devkit(root):
        print("раскладка: правило действует на самом devkit, тут проверять нечего")
        return 0
    findings = layout.check(root)
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("раскладка в порядке")
    return 0


def machine_build(devkit, target, log=print):
    """Сборка на машину: бинари в каталог назначения плюс сверка победителя PATH.

    Этой дорогой едет выкат devkit (deploy в .devkit/deploy.local зовёт
    devkitctl build), и до DK-599 она молчала, когда в PATH выигрывала чужая
    копия: слияние отчитывалось зелёным, а команда на машине оставалась старой
    сборкой. Теперь после укладки каждая утилита сверяется по победителю PATH,
    и расхождение роняет сборку, а через неё и выкат.
    """
    findings = build.local(devkit, target, log=log)
    if findings:
        return findings
    version, commit = build.stamp(devkit)
    return update.path_divergence(
        target, {name: (version, commit) for name in build.tools(devkit)})


def build_binaries(release, out):
    """Сборка бинарей devkit, та же командой с машины и с раннера.

    Собирается тот чекаут devkit, из которого запущен сам devkitctl: версия
    зашивается из его git, и собирать чужое дерево этой команде незачем.
    """
    if not shutil.which("go"):
        sys.stderr.write("go в PATH нет: собирать нечем, Go ставится пакетным менеджером "
                         "(brew install go)\n")
        return 2
    if release:
        findings = build.release(DEVKIT, out or "dist")
    elif out:
        # Явный --out это сборка в сторону (стенд, ручная проверка), PATH ей
        # не судья.
        findings = build.local(DEVKIT, Path(out))
    else:
        findings = machine_build(DEVKIT, update.bin_dir())
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("сборка в порядке")
    return 0


def update_devkit(pin, check, restarted):
    """Установка и обновление devkit готовыми бинарями релиза.

    Раскладку машинного контура делает тот же код, что зовёт doctor --fix:
    второй копии этой работы у установки нет, иначе она разошлась бы с
    доктором в первый же раз.
    """
    main, from_main = devkit_checkout()
    return update.run(main, from_main, pin=pin, check=check, restarted=restarted,
                      machine=lambda: check_machine(True))


def run_map(start, write=False):
    """Генератор карты проекта: печатать в stdout либо писать в файл."""
    root, _ = project_root(start)
    full_text, hash_val = codemap.render_map(root)

    if write:
        map_path = root / "docs" / "map.md"
        map_path.parent.mkdir(parents=True, exist_ok=True)
        map_path.write_text(full_text, encoding="utf-8")
        print("Карта записана: %s" % map_path)
        return 0
    else:
        # Сухой прогон в stdout
        print(full_text)
        return 0


def weigh_resident(start, runs, limit, model, prompt):
    # Гейт свежести: замер меряет раскладку devkit на машине, а кладёт её
    # doctor --fix. По вчерашней раскладке замер соврал бы молча, поэтому сперва
    # прогоняются те же проверки доктора, что за раскладку и отвечают. Остальной
    # машинный контур (снимок квоты, tmux) на вес резидента не влияет и мерить
    # не мешает.
    root, _ = project_root(start)
    main, _ = devkit_checkout()
    devkit_src = main if (main / "kit" / "harness").is_dir() else DEVKIT
    profiles, hf = rules.enabled_harnesses(None, devkit_src / "kit" / "harness")
    homes = rules.machine_homes()
    findings = ["машина: %s" % m for m in hf]
    for name, profile in profiles:
        paths, bads = contour_paths(name, profile, homes)
        findings += ["машина: %s" % m for m in bads]
        for axis, check in ((AXIS_AGENTS[0], check_agent_defs), (AXIS_SKILLS[0], check_skills)):
            if bads or paths[axis] is None:
                continue
            f, _ = check(False, paths[axis])
            findings += ["машина: %s" % m for m in f]
    rfindings, _ = rules.check(root, DEVKIT, False, SKIP_DIRS)
    findings += rfindings
    return weigh.measure(root, DEVKIT, findings, runs, limit, prompt, model)


def stats_context(start):
    root, _ = project_root(start)
    directory = context.logs_dir(root)
    if not directory.is_dir():
        sys.stderr.write("журналы сессий не найдены: %s\n"
                         "харнес пишет их по слепку пути проекта, и для %s такой директории нет: "
                         "сессий отсюда не было либо проект открывали из другой директории\n"
                         % (directory, root))
        return 2
    if not context.report(directory, sys.stdout):
        sys.stderr.write("в журналах сессий нет запросов с расходом: %s\n" % directory)
        return 2
    return 0


def drain_run(start, all_projects=False):
    # --all ходит по всему ~/.claude/projects, как разовый скрипт tstats.py;
    # без него разбирается слепок пути текущего проекта, тот же, что у stats
    # --context. Журналы сессий харнес кладёт по слепку пути, а сессия живёт в
    # клоне, поэтому корень здесь остаётся клоном и в корп-контуре.
    directory = context.projects_dir() if all_projects else context.logs_dir(project_root(start)[0])
    label = "весь корпус" if all_projects else str(directory)
    if not directory.is_dir():
        sys.stderr.write("журналы сессий не найдены: %s\n"
                         "харнес пишет их по слепку пути проекта, и для сессий отсюда "
                         "такой директории нет\n" % label)
        return 2
    if not drain.report(directory, sys.stdout):
        sys.stderr.write("в журналах сессий нет вызовов инструментов: %s\n" % label)
        return 2
    return 0


def stats(start, ctx=False):
    if ctx:
        # Журналы сессий харнес кладёт по слепку пути проекта, а сессия живёт в
        # клоне, поэтому тут корень остаётся клоном и в корп-контуре.
        return stats_context(start)
    root, _ = project_root(start)
    root = Path(corp.pair(root, DEVKIT)[1] or root)
    log_file = root / RUN_LOG
    if not log_file.exists() or log_file.stat().st_size == 0:
        sys.stderr.write("журнал запусков не найден: %s\n" % RUN_LOG)
        return 2

    runs = {}
    total_runs, total_errors = 0, 0

    for ln in log_file.read_text(encoding="utf-8", errors="replace").splitlines():
        parts = ln.split('\t')
        if len(parts) != 4:
            continue
        try:
            code = int(parts[3])
        except ValueError:
            continue

        tool = parts[1]
        cmd = parts[2]
        key = (tool, cmd)

        if key not in runs:
            runs[key] = [0, 0]
        runs[key][0] += 1
        if code != 0:
            runs[key][1] += 1
        total_runs += 1
        if code != 0:
            total_errors += 1

    if total_runs == 0:
        sys.stderr.write("журнал пуст: %s\n" % RUN_LOG)
        return 2

    sorted_runs = sorted(runs.items(), key=lambda x: x[1][0], reverse=True)

    max_len = max(len(f"{t} {c}") for (t, c), _ in sorted_runs)
    for (tool, cmd), (count, errors) in sorted_runs:
        key_str = f"{tool} {cmd}"
        error_pct = round(100 * errors / count)
        print(f"{key_str:<{max_len}}  {count:>3}   ошибок {errors} ({error_pct}%)")

    total_pct = round(100 * total_errors / total_runs)
    print(f"{'итого':<{max_len}}  {total_runs:>3}   ошибок {total_errors} ({total_pct}%)")

    return 0


def new(start, prefix, name, no_board):
    if not no_board and not prefix:
        # Аргументы разбираются до раскладки: отказ не должен оставлять за собой
        # заведённую наполовину директорию.
        sys.stderr.write("нужен --prefix для доски либо --no-board, когда задачи во внешнем трекере\n")
        return 2
    root, in_git = project_root(start)
    # Подключение доводит проект до рабочего состояния само, как corp заводит
    # боковую директорию: пустого места и не-репозитория оно не боится. Иначе
    # первый же шаг инструкции падал бы на записи AGENTS.md, а до shipctl дело
    # доходило бы без git.
    started = []
    if not root.is_dir():
        try:
            root.mkdir(parents=True)
        except OSError as e:
            sys.stderr.write("не завёл директорию %s: %s\n" % (root, e))
            return 1
        started.append("директория %s заведена" % root)
    if not in_git:
        rc, out = run(["git", "init", "-q", str(root)])
        if rc != 0:
            sys.stderr.write("git init %s: %s\n" % (root, out))
            return 1
        in_git = True
        started.append("git-репозиторий заведён: ветку и дерево задачи shipctl берёт отсюда")
    agents = root / rules.AGENTS_FILE
    if agents.exists():
        sys.stderr.write("%s уже есть, проект подключён; проверка: devkitctl doctor\n"
                         % rules.AGENTS_FILE)
        return 2
    text = (DEVKIT / "kit" / "templates" / "AGENTS.project.md").read_text(encoding="utf-8")
    if text.startswith("<!--"):
        text = text[text.index("-->") + 3:].lstrip("\n")
    name = name or root.name
    text = text.replace("<название проекта>", name).replace("<XX>", prefix or "XX")
    agents.write_text(text, encoding="utf-8")
    done = started + ["%s создан из шаблона" % rules.AGENTS_FILE]
    applied, residual = connect_git_hooks(root)
    done.append(applied or residual or "git-хуки уже подключены")
    done += scaffold_machine_gitignore(root)
    if no_board:
        done.append("доска не заводилась: вписать в %s, какой это трекер" % rules.AGENTS_FILE)
    else:
        tc = shutil.which("taskctl")
        if tc:
            rc, out = run([tc, "-C", str(root), "init", "--prefix", prefix, "--name", name])
            if rc != 0:
                sys.stderr.write("taskctl init: %s\n" % out)
                return 1
            done.append(out)
        else:
            done.append("taskctl не в PATH; доска заводится после сборки: "
                        "python3 %s/tools/devkitctl/devkitctl.py build && taskctl -C %s init --prefix %s"
                        % (DEVKIT, root, prefix))
        done += scaffold_deploy(root)
    # Тонкие файлы генерятся последними: доска к этому моменту уже заведена, и в
    # импорты попадает RULES.board.md.
    _, generated = rules.check(root, DEVKIT, fix=True, skip_dirs=SKIP_DIRS)
    done += generated
    steps = []
    # Свежий репозиторий стоит на неродившейся ветке, и ветку задачи от неё
    # shipctl не заводит («не нашёл ветку main или master»). Первый коммит
    # поэтому делает подключение, и делает пустым: чужие файлы проекта в него не
    # едут, а ветка рождается. Спрашивается тут HEAD, а не то, кто завёл
    # репозиторий: пустым он бывает и заведённым заранее руками.
    if run(["git", "-C", str(root), "rev-parse", "--verify", "HEAD"])[0] != 0:
        rc, out = run(["git", "-C", str(root), "commit", "--allow-empty", "-q",
                       "-m", "chore: подключение devkit"])
        if rc == 0:
            done.append("первый коммит проекта сделан: без него shipctl start ветку задачи "
                        "не заведёт")
        else:
            steps.append("первый коммит проекта: git -C %s commit --allow-empty "
                         "-m \"chore: подключение devkit\"; сейчас он не прошёл (%s), а без "
                         "коммита ветка не рождается и shipctl start её не найдёт"
                         % (root, out.replace("\n", " ")[:120]))
    print("\n".join(done))
    steps += deploy_steps(root)
    if no_board:
        steps.append("%s: вписать, какой это трекер и как в нём ведутся задачи"
                     % (root / rules.AGENTS_FILE))
    print_remaining(steps, "devkitctl doctor -C %s" % root)
    return 0


def deploy_steps(root, corp_contour=False):
    """Незаполненное в обвязке выката словами шага, а не находкой доктора:
    подключение печатает это в одном списке с остальным недоделанным. В
    корп-контуре команду выката не спрашивают: выкат там ведёт процесс
    компании."""
    deploy, test, _ = read_deploy(root)
    if deploy is None:
        return []
    keys = (("test", test),) if corp_contour else (("test", test), ("deploy", deploy))
    miss = [k for k, v in keys if not v]
    if not miss:
        return []
    return ["обвязка выката %s: вписать %s"
            % (root / DEPLOY_CONFIG, ", ".join("%s =" % k for k in miss))]


def print_remaining(steps, check):
    """Хвост подключения: что осталось человеку и чем это проверяется. Конец без
    дел печатается тоже, и говорит он о сделанном, а не об отсутствии дел:
    молчание команды и законченная работа снаружи выглядят одинаково, а значат
    разное."""
    if steps:
        print("\nосталось сделать:")
        for i, s in enumerate(steps, 1):
            print("%d. %s" % (i, s))
    else:
        print("\nподключение завершено, дописывать руками нечего.")
    print("проверить обвязку можно командой %s" % check)


PREFIX_CLASH = ("префикс доски %s совпадает с ключом проекта в трекере: рубеж следов не отличит "
                "локальный ID доски от ключа тикета и правило про ID на этом проекте работать "
                "не будет")
# Сколько раз первый прогон переспрашивает префикс. Дальше он отступает с
# отказом: заколдованный вопрос без выхода хуже отказа с командой в руках.
PREFIX_TRIES = 3


def ask_prefix(clone_name, bound_key):
    """Префикс доски первого прогона: предложение из имени клона, которое
    подтверждается пустым вводом либо заменяется на месте. Отдаёт пусто, когда
    спрашивать некого (без tty) или человек так и не назвал годного."""
    if not corp.interactive():
        return ""
    hint = corp.prefix_hint(clone_name)
    if bound_key and hint == bound_key.upper():
        hint = ""
    for _ in range(PREFIX_TRIES):
        got = corp.ask("префикс ID задач доски, заглавными буквами", hint).upper()
        if not got:
            print("без префикса доску заводить не с чем.")
        elif not corp.prefix_ok(got):
            print("префикс пишется одними заглавными буквами, без цифр и знаков.")
        elif bound_key and got == bound_key.upper():
            print(PREFIX_CLASH % got + ".")
        else:
            return got
    return ""


def ask_contour(contour):
    """Ответы про контур компании: адрес трекера и имя пользователя. Секретов
    тут нет, токен приезжает переменной окружения, поэтому и ввод обычный. Без
    tty вопросов нет вовсе, и контур ложится болванкой, как раньше."""
    if not corp.interactive() or os.path.exists(corp.contour_path(contour)):
        return {}
    print("контур компании %s заводится заново: адрес трекера и имя пользователя не секреты, "
          "токен спрашивается не тут, а переменной окружения." % contour)
    return {"base_url": corp.ask("адрес трекера, https://tracker.example"),
            "user": corp.ask("имя пользователя в трекере, от него идут assign и ворклоги")}


def corp_connect(start, prefix, name, contour="", key="", branch="", remote="", local_arg=""):
    # Подключение корп-проекта и оно же восстановление обвязки: заведённое не
    # переписывается, доводится только недостающее, поэтому повторный прогон
    # после переклонирования возвращает клон в рабочее состояние, а доску в
    # боковой директории не трогает.
    root, in_git = project_root(start)
    if not in_git:
        sys.stderr.write("%s не git-репозиторий: corp подключает клон корп-репозитория, "
                         "обычный проект подключает devkitctl new\n" % root)
        return 2
    clone = Path(corp.checkout(root) or root)
    # Путь боковой директории: сказанный флагом, иначе тот, куда уже ведёт
    # редирект (повторный прогон и восстановление обвязки), иначе общая
    # директория контура. Подключённый до DK-583 клон остаётся на своей
    # ../<проект>-local: редирект называет её явно, и переезжать ему незачем.
    # Путь разрешается по диску: сказанный флагом приезжает как есть, а клон
    # git уже отдаёт разрешённым, и относительный редирект между ними иначе
    # выходит через корень файловой системы.
    local = Path(os.path.realpath(local_arg or corp.local_dir(clone, DEVKIT)
                                  or corp.contour_local(clone, contour)))
    board = local / "docs" / "TASKS.md"
    # Про место рабочих файлов первый прогон говорит до вопросов, а не строкой
    # отчёта в конце: человек зовёт corp из клона и вправе ждать, что доска
    # заведётся тут же, а она ложится сбоку. Печатается это тому, кому есть что
    # ответить, а в headless-прогоне откладывается до места, где ясно, что
    # прогон продолжится: отказ без --prefix там молчит в stdout, как раньше.
    first = not board.exists()
    announce = ("доска и файлы задач лягут в боковую директорию %s, а не в клон: рабочим файлам "
                "devkit в корп-репозитории не место, в клоне остаётся одна обвязка." % local)
    if first and corp.interactive():
        print(announce)
    # Префикс доски, совпавший с ключом проекта в трекере, снимает правило про
    # локальный ID: рубеж следов не отличает ID строки доски от ключа тикета
    # (DK-124). На заведённой доске это уже находка доктора, а на незаведённой
    # префикс ещё выбирается, и дешевле остановиться здесь.
    bound_key = key or corp.tracker_value(local, "key", DEVKIT)
    if first and not prefix:
        prefix = ask_prefix(clone.name, bound_key)
        if not prefix and corp.interactive():
            sys.stderr.write("годный префикс доски так и не назван, повторить corp "
                             "с --prefix\n")
            return 2
        if not prefix:
            sys.stderr.write("нужен --prefix для доски боковой директории (%s): без tty первый "
                             "прогон его не спрашивает\n" % local)
            return 2
    if prefix and bound_key and prefix == bound_key.upper():
        if first:
            sys.stderr.write(PREFIX_CLASH % prefix + ", взять другой --prefix\n")
            return 2
        sys.stderr.write("предупреждение: префикс доски %s совпадает с ключом проекта в трекере, "
                         "рубеж следов на этой паре правило про локальный ID снимает\n" % prefix)
    if first and not corp.interactive():
        print(announce)
    answers = ask_contour(contour) if contour else {}
    done = []
    if not local.is_dir():
        local.mkdir(parents=True)
        done.append("боковая директория %s заведена" % local)
    # Репозиторий заводится один на контур, а не на проект: боковая директория
    # контура держит проекты подкаталогами, и второй git внутри чужого рабочего
    # дерева был бы вложенным репозиторием (DK-583). Уже накрытый репозиторием
    # путь не трогается вовсе.
    top = corp.repo_top(local)
    if not top:
        # Общая раскладка кладёт проект подкаталогом ../<контур>-local, и
        # репозиторий заводится на самой директории контура; прежняя
        # ../<проект>-local лежит сиблингом клона, и репозиторий её собственный.
        init_at = local if corp.same_path(local.parent, clone.parent) else local.parent
        rc, out = run(["git", "init", "-q", str(init_at)])
        if rc != 0:
            sys.stderr.write("git init %s: %s\n" % (init_at, out))
            return 1
        done.append("git-репозиторий %s заведён: доска пушится в личный приватный remote, "
                    "а не в корп-origin" % init_at)
    elif not corp.same_path(top, str(local)):
        done.append("доска ложится подкаталогом репозитория %s: он один на весь контур" % top)
    (local / "docs" / "tasks").mkdir(parents=True, exist_ok=True)
    (local / ".devkit").mkdir(exist_ok=True)
    if not (local / rules.AGENTS_FILE).exists():
        text = (DEVKIT / "kit" / "templates" / "AGENTS.project.md").read_text(encoding="utf-8")
        if text.startswith("<!--"):
            text = text[text.index("-->") + 3:].lstrip("\n")
        text = text.replace("<название проекта>", name or clone.name).replace("<XX>", prefix or "XX")
        (local / rules.AGENTS_FILE).write_text(text, encoding="utf-8")
        done.append("%s создан из шаблона в боковой директории" % rules.AGENTS_FILE)
    if not board.exists():
        tc = shutil.which("taskctl")
        if not tc:
            sys.stderr.write("taskctl не в PATH, доску заводить нечем: собрать утилиты "
                             "(python3 %s/tools/devkitctl/devkitctl.py build) и повторить\n" % DEVKIT)
            return 1
        # Доску заводит --here: боковая директория контура это подкаталог его
        # репозитория, а init без флага поднялся бы к вершине и положил доску
        # одну на все проекты контура.
        rc, out = run([tc, "-C", str(local), "init", "--here",
                       "--prefix", prefix, "--name", name or clone.name])
        if rc != 0:
            sys.stderr.write("taskctl init: %s\n" % out)
            return 1
        done.append(out)
    done += scaffold_deploy(local)
    done += scaffold_machine_gitignore(local)
    applied, residual = connect_git_hooks(local)
    if applied or residual:
        done.append(applied or residual)
    _, generated = rules.check(local, DEVKIT, fix=True, skip_dirs=SKIP_DIRS)
    done += ["боковая директория: %s" % g for g in generated]
    rel = corp.ensure_redirect(clone, local)
    done.append("редирект корня: git config devkit.local %s" % rel)
    linked = corp.ensure_tree_link(clone, local)
    if linked:
        done.append(linked)
    for bkey, bval in corp.ensure_binding(local, clone,
                                          {"contour": contour, "key": key, "branch": branch}):
        why = (" (по нему обвязка клона находится после переклонирования)"
               if bkey == "repo" else "")
        done.append("%s: вписан %s = %s%s" % (corp.TRACKER, bkey, bval, why))
    if contour:
        cpath = corp.ensure_contour(contour, answers)
        if cpath:
            miss = [k for k in ("base_url", "user") if not corp.contour_value(contour, k)]
            done.append("контур компании %s заведён %s: %sтаблицу [status] сверить с трекером"
                        % (cpath, "болванкой" if miss else "с ответами первого прогона",
                           "адрес и пользователя вписать, " if miss else ""))
    if remote and not corp.git(local, "remote"):
        rc, out = run(["git", "-C", str(local), "remote", "add", "origin", remote])
        if rc != 0:
            sys.stderr.write("git remote add: %s\n" % out)
            return 1
        done.append("remote доски: origin %s" % remote)
    imports, hfindings = corp_profiles(local)
    _, thin_fixed = corp_thin(clone, local, imports, fix=True)
    done += thin_fixed
    for n in corp.ensure_exclude(clone, corp.hidden_names(corp_thin_names(imports))):
        done.append(".git/info/exclude: спрятан %s" % n)
    for n in corp.drop_exclude(clone, [corp.LINK_DIR]):
        done.append(".git/info/exclude: строка %s убрана, доска видна поиску клона" % n)
    for hook, state, chained in corp.ensure_hooks(clone, DEVKIT):
        done.append("хук %s: цепочка %s%s"
                    % (hook, state, " (чужой хук переехал в %s.chained)" % hook if chained else ""))
    print("\n".join(done + hfindings))
    print_remaining(corp.remaining(clone, local, DEVKIT) + deploy_steps(local, corp_contour=True),
                    "devkitctl doctor -C %s, дальше trackctl status -C %s" % (clone, local))
    return 0


def main(argv):
    ap = argparse.ArgumentParser(prog="devkitctl", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    d = sub.add_parser("doctor", help="проверить обвязку проекта")
    d.add_argument("-C", dest="dir", default=".", help="директория проекта")
    d.add_argument("--fix", action="store_true",
                   help="доводить обвязку до актуальной (additive, заполненное не трогает)")
    d.add_argument("--layout", action="store_true",
                   help="только раскладка devkit, без машинной обвязки (шаг CI)")
    n = sub.add_parser("new", help="подключить новый проект")
    n.add_argument("-C", dest="dir", default=".", help="директория проекта")
    n.add_argument("--prefix", default="", help="префикс ID задач доски, заглавными (XR)")
    n.add_argument("--name", default="", help="название проекта, по умолчанию имя директории")
    n.add_argument("--no-board", action="store_true", help="без доски, задачи во внешнем трекере")
    c = sub.add_parser("corp", help="подключить корп-проект (боковая директория и обвязка клона)")
    c.add_argument("-C", dest="dir", default=".", help="директория корп-клона")
    c.add_argument("--prefix", default="",
                   help="префикс ID задач доски, заглавными (XR); первый прогон спрашивает "
                        "его сам, предложив по имени клона")
    c.add_argument("--name", default="", help="название проекта, по умолчанию имя директории клона")
    c.add_argument("--contour", default="", help="имя контура компании ~/.devkit/tracker/<имя>.local")
    c.add_argument("--key", default="", help="ключ проекта в трекере (ABC), отличный от --prefix")
    c.add_argument("--branch", default="", help="шаблон ветки, по умолчанию решает shipctl")
    c.add_argument("--remote", default="", help="личный приватный remote доски боковой директории")
    c.add_argument("--local", default="",
                   help="путь боковой директории с доской; по умолчанию общая на контур "
                        "../<контур>-local/<проект>, а без --contour прежняя ../<проект>-local")
    b = sub.add_parser("build", help="собрать бинари devkit с зашитой версией")
    b.add_argument("--release", action="store_true",
                   help="четыре пары GOOS/GOARCH, тарболлы и SHA256SUMS")
    b.add_argument("--out", default="",
                   help="каталог назначения: по умолчанию тот же, куда ставит update, "
                        "а под --release dist")
    u = sub.add_parser("update", help="поставить или обновить devkit бинарями релиза")
    u.add_argument("--pin", action="store_true",
                   help="перевести чекаут с ветки на новейший тег (первая установка)")
    u.add_argument("--check", action="store_true",
                   help="только рассказать про версии, ничего не двигая")
    u.add_argument(update.RESTART_FLAG, dest="restarted", action="store_true",
                   help=argparse.SUPPRESS)
    s = sub.add_parser("stats", help="сводка по журналу запусков")
    s.add_argument("-C", dest="dir", default=".", help="директория проекта")
    s.add_argument("--context", action="store_true",
                   help="разбивка объёма по журналам сессий вместо журнала запусков")
    dr = sub.add_parser("drain", help="замер расхода контекста по журналам сессий")
    dr.add_argument("-C", dest="dir", default=".", help="директория проекта")
    dr.add_argument("--all", action="store_true",
                   help="разобрать весь ~/.claude/projects, как разовый скрипт tstats.py")
    g = sub.add_parser("watch", help="сторожок цикла цели: позвать по вставшим")
    g.add_argument("--idle", type=int, default=0,
                   help="порог простоя в минутах, по умолчанию %d" % (watch.IDLE // 60))
    w = sub.add_parser("weigh", help="живой замер веса резидента")
    w.add_argument("-C", dest="dir", default=".", help="директория проекта")
    w.add_argument("--runs", type=int, default=weigh.RUNS,
                   help="сколько пар прогонов, по ним считается разброс (по умолчанию %d)" % weigh.RUNS)
    w.add_argument("--limit", type=int, default=weigh.LIMIT,
                   help="потолок резидента в токенах, выше него код 1 (по умолчанию %d)" % weigh.LIMIT)
    w.add_argument("--model", default="", help="модель прогона, по умолчанию модель клиента")
    w.add_argument("--prompt", default=weigh.PROMPT, help="запрос прогона, у обоих он один")
    m = sub.add_parser("map", help="генератор карты проекта и индекса решений")
    m.add_argument("-C", dest="dir", default=".", help="директория проекта")
    m.add_argument("--write", action="store_true",
                   help="писать docs/map.md, иначе печатать в stdout")
    sub.add_parser("selfcheck",
                   help="живой круг связки во временном проекте, с уборкой за собой")
    a = ap.parse_args(argv)
    if a.cmd == "doctor":
        # Один прогон спрашивает у git одно и то же по многу раз (сводка режима,
        # проверка бинарей, находка про релиз), поэтому чтения кешатся. Меняют
        # репозиторий devkit только fetch и checkout в update.run, а их доктор
        # не зовёт: установка бинарей из его --fix идёт без git-команд вовсе.
        update.git_cache(True)
        rc = layout_only(a.dir) if a.layout else doctor(a.dir, a.fix)
    elif a.cmd == "map":
        rc = run_map(a.dir, a.write)
    elif a.cmd == "new":
        rc = new(a.dir, a.prefix.upper(), a.name, a.no_board)
    elif a.cmd == "corp":
        rc = corp_connect(a.dir, a.prefix.upper(), a.name, a.contour, a.key.upper(),
                          a.branch, a.remote, a.local)
    elif a.cmd == "weigh":
        rc = weigh_resident(a.dir, a.runs, a.limit, a.model, a.prompt)
    elif a.cmd == "build":
        rc = build_binaries(a.release, a.out)
    elif a.cmd == "update":
        rc = update_devkit(a.pin, a.check, a.restarted)
    elif a.cmd == "watch":
        rc = watch.run(idle=a.idle * 60 if a.idle else None)
    elif a.cmd == "selfcheck":
        rc = selfcheck.main()
    elif a.cmd == "drain":
        rc = drain_run(a.dir, a.all)
    else:
        rc = stats(a.dir, a.context)
    # Журнал запусков в корп-контуре лежит там же, где остальные рабочие файлы,
    # то есть в боковой директории: в дереве клона .devkit нет. У build своего
    # -C нет, он собирает чекаут devkit, и запуск ложится в его же журнал.
    # Сторожок в журнал не пишет: по нему он и меряет движение цикла, и своя
    # строка раз в пять минут выглядела бы движением там, где всё стоит.
    # Самопроверка тоже: её круг сам зовёт утилиты, и их строки в журнале
    # живого проекта читались бы как работа человека, а не как прогон.
    if a.cmd not in ("watch", "selfcheck"):
        root = project_root(getattr(a, "dir", str(DEVKIT)))[0]
        log_run(Path(corp.pair(root, DEVKIT)[1] or root), a.cmd, rc)
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
