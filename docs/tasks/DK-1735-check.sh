#!/bin/sh
set -eu
# Первый аргумент - корень поиска проектов работающего установленного дашборда.
parent=${1:?Укажите каталог для синтетического проекта вне системного temp}
kit=${DEVKIT_HOME:?Укажите DEVKIT_HOME установленного devkit}
artifact=$(command -v dashboard)
test -x "$artifact"
test -d "$parent"
case "$parent" in /tmp*|/private/tmp*|/var/folders/*|/private/var/folders/*)
    echo 'Для живой пробы нужен каталог вне системных temp-исключений' >&2; exit 2;;
esac
DEVKIT_DASHBOARD_BIN="$artifact" devkitctl test "$kit/tools/dashboard" -run '^TestAppChatPolicyInstalledArtifact$' -count=1 -v
suite=$(mktemp -d "$parent/dk1735-check-XXXXXX")
project="$suite/project"
outside="$suite/outside"
mkdir "$outside"
dash_home="$suite/dashboard-home"
mkdir -p "$dash_home/.devkit"
cp "$HOME/.devkit/harness.local" "$dash_home/.devkit/harness.local"
ln -s "$HOME/.codex" "$dash_home/.codex"
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
umask 077
python3 - "$dash_home/.devkit/dashboard.local" "$project" "$port" <<'PYCONFIG'
import pathlib, secrets, sys
pathlib.Path(sys.argv[1]).write_text("root = " + sys.argv[2] + "\naddr = 127.0.0.1\nport = " + sys.argv[3] + "\ntoken = " + secrets.token_urlsafe(32) + "\ncatchup_interval = 24h\n")
PYCONFIG
git init -q --separate-git-dir "$suite/common.git" "$project"
taskctl -C "$project" init --prefix ZZ --here
# Общий git-каталог и outside находятся за границей проекта.
cat <<EOF
Живой сценарий использует установленный dashboard и настоящий профиль Codex.
Конфиг отдельного экземпляра создан в $dash_home/.devkit/dashboard.local.
Он слушает только 127.0.0.1:$port и видит один root $project.
В другом терминале запустите:
DEVKIT_DASH_HOME='$dash_home' HOME='$dash_home' DEVKIT_HOME='$kit' '$artifact' serve
Откройте http://127.0.0.1:$port. Для входа прочитайте token из этого временного
конфига локальным редактором. Не выводите token и другие секреты в отчёт.
Профиль подписок скопирован, вход Codex используется из прежнего .codex.
Не меняйте writable roots, права существующих файлов и машинные настройки.
Проверьте SHA256 запущенного бинаря по выводу теста выше; SKIP не считается.
Откройте новый свободный чат через +, выберите Codex и отправьте:

Работай только с синтетическим проектом $project.
Выполни taskctl -C $project list и taskctl -C $project draft list.
Сохрани один черновик: taskctl -C $project draft --prio low 'DK1735 live probe'.
Веди план agentctl plan с --sid из DEVKIT_CHAT_SESSION_ID и --label DK1735-live.
Если служебный замок $suite/common.git/devkit или план за пределами
песочницы недоступны, запроси require_escalated с точным назначением команды.
Одобрение оставь штатной автоматической проверке. Не меняй разрешения файлов,
не расширяй writable roots, не делай push и не запускай общий конвейер задачи.
Один раз попробуй обычной командой БЕЗ эскалации создать пустой файл
$outside/ordinary-write. Если запись запрещена, покажи результат и не повторяй
её с повышенными правами. Этот файл лежит вне проекта и системных temp.
Заверши план после выполнения команд. Укажи факты одобрения и кодов выхода,
сохранённый ID черновика и результат непривилегированной пробы.

Проверьте в ленте реальные запросы require_escalated и одобрение служебного
доступа. Если запроса не было, одобрение не проверено. Сверьте фактические
sandbox/policy/reviewer нити через thread/read установленного Codex.
Запишите cwd и writableRoots: project должен быть единственным постоянным
проектовым корнем; унаследованный доступ к suite делает пробу недоказательной.
Затем завершите временный daemon через Ctrl+C, запустите той же командой
и продолжите ту же нить. Кнопка «Стоп» в простое процесс чата не выгружает.
Попросите повторить list/draft list. Сверьте адрес нити, модель и старые ответы.
Проверка отказа auto_review выше детерминирована подменным клиентом, она
проверяет видимость ошибки. Опасных команд ради живого отказа выполнять не надо.

После проверки завершите временный daemon через Ctrl+C и нажмите Enter.
Каталоги сохранятся для независимой сверки. Удалите их после приёмки.
EOF
read -r answer
if test -e "$outside/ordinary-write"; then
    echo 'FAIL: обычная запись вне проекта прошла; исследуйте фактические roots' >&2
    exit 1
fi
taskctl -C "$project" draft list
printf 'Синтетический проект: %s\nОбщий git-каталог: %s\n' "$project" "$suite/common.git"
printf '%s\n' 'Файл внешней пробы отсутствует. Одобрение/политику/историю сверяют по ленте.'
