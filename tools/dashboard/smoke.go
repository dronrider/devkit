package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

// Сквозной прогон агентской части DoD цели DK-112: dashboard smoke поднимает
// сервер на синтетическом окружении и проходит по API ту самую цепочку, что
// названа целью, - доска со статусами и работами, запуск работы, сообщение
// цели и его подхват витком, стоп и уведомление о стопе в живой ленте, - а
// пройдя, печатает «dashboard smoke: ok».
//
// Живого ничего не задевается: свой дом во временной директории, свой корень
// с синтетическим проектом, подпроцессы (taskctl, tmux, claude, оболочка
// цикла) изображают фикстуры того же прогона, а бэкенд уведомлений подменён
// пустышкой, так что баннера на машине не появляется. Настоящий тут
// hooks/notify.py: формат его журнала это контракт ленты, и держать его
// проверенным сквозным прогоном дешевле, чем ловить расхождение глазами.
//
// Гоняется руками (dashboard smoke) и тестом (smoke_test.go): сторож остаётся
// сторожем только пока его зовут.

const smokeOK = "dashboard smoke: ok"

// smokeToken это секрет входа синтетического стенда: сервер поднимается со
// своим конфигом, и живой ~/.devkit/dashboard.local в прогоне не участвует.
const smokeToken = "smoke-token"

// smokeGoal и smokeTask это строки синтетической доски: цель поднимается
// оболочкой цикла, задача остаётся соседкой по доске, чтобы список секций был
// не из одной строки.
const (
	smokeGoal = "XR-100"
	smokeTask = "XR-002"
	// smokeAccepted это принятая человеком задача в Check: на ней проверяется
	// закрытие мимо витка. Вид приёмки она носит суффиксом заголовка, а до
	// дашборда он доезжает полем accept ответа taskctl.
	smokeAccepted = "XR-003"
	// smokeDraft это запись накопителя: на ней проверяется удаление черновика с
	// экрана, до которого раньше приходилось идти в терминал.
	smokeDraft = "XR-009"
)

// Подписки стенда. Имена выдуманные, живых тут нет ни одного: в коде дашборда
// имён харнесов не стоит вовсе (рубеж TestQuotaNoHarnessNamesInCode), а прогон
// проверяет дорогу, а не конкретную подписку. Клиентов два, по одному на
// подписку, и оба лежат фикстурами в PATH стенда: выбор доехал до команды
// тогда, когда сессию поднял клиент выбранной подписки, а не соседней.
const (
	smokeHarnessOne = "первая-подписка"
	smokeHarnessTwo = "вторая-подписка"
	smokeClientOne  = "клиент-один"
	smokeClientTwo  = "клиент-два"
)

// smokeBoardJSON изображает ответ taskctl list --json: доска с целью в работе,
// принятой задачей в Check и соседкой в Backlog. Закрытие уносит строку Check с
// доски, и таким прогон видит её после нажатия.
const smokeBoardJSON = smokeBoardHead +
	`{"key":"check","title":"Check","rows":[{"id":"XR-003","title":"Принятая глазами","accept":"user","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"S","link":"-"}]},` +
	smokeBoardTail

const smokeBoardClosedJSON = smokeBoardHead + `{"key":"check","title":"Check","rows":[]},` + smokeBoardTail

const smokeBoardHead = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[{"id":"XR-100","title":"Цель: пробный цикл smoke","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"[tasks/XR-100.md](tasks/XR-100.md)"}]},`

const smokeBoardTail = `{"key":"backlog","title":"Backlog","rows":[{"id":"XR-002","title":"Соседка по доске","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"S","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[]}]}`

const smokeBoardDoc = `# Синтетическая доска smoke (префикс XR)

Доска стенда: строки прогон берёт фикстурой taskctl, а этот файл держит
признак проекта, по которому дашборд ищет доски в корнях конфига. Строка
принятой задачи стоит и тут: закрытие правит доску файлом, и по нему сервер
узнаёт, что помнить прежний ответ утилиты больше нельзя.

## In progress

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-003 | Принятая глазами [приёмка: user] | task | P2 | 31 (25+3+1+0+2) | S | - |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-002 | Соседка по доске | task | P2 | 30 (25+2+1+0+2) | S | - |

## Blocked
`

const smokeGoalDoc = `# XR-100: Цель: пробный цикл smoke

## Чего хотим

Синтетическая цель прогона smoke: на ней проверяется переписка через файл
цели и подхват сообщения витком.

## Задачи цели

Нарезка прогона smoke, две строки.

- XR-002 (task, S, R=30). Соседка по доске, стоит в Backlog.
- XR-001 (task, S, R=20). Закрытая до прогона, лежит в архиве.

## Журнал

- 2026-01-01, цель заведена прогоном smoke; continue
`

// smokeArchiveDoc держит закрытую задачу состава: строка закрытой задачи
// уезжает с доски, и без архива состав цели показывал бы её потерянной.
const smokeArchiveDoc = `# Синтетический архив smoke

| ID | Задача | Тип | P | Закрыто | Ссылка |
|--------|--------|-----|---|---------|--------|
| XR-001 | Закрытая до прогона | task | P2 | 2026-01-01 | - |
`

// smokeDraftDoc это запись накопителя из одного слова: ровно такой мусор с
// доски и снимают, а до дашборда снять его можно было только терминалом.
const smokeDraftDoc = `show

записан 2026-01-03
`

// smokeStep это шаг цепочки DoD: имя для вывода и сама проверка, которая
// возвращает строку с тем, что увидела.
type smokeStep struct {
	name string
	run  func() (string, error)
}

type smoke struct {
	dir, home, root, proj, bin string
	url                        string
	client                     *http.Client
	restore                    []func()
	items                      chan Notification
	notes                      chan string
	feedErr                    chan error
}

func (s *smoke) close() {
	for i := len(s.restore) - 1; i >= 0; i-- {
		s.restore[i]()
	}
}

func smokeWrite(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), mode)
}

// setEnv ставит переменные окружения на время прогона и отдаёт откат: смок
// гоняется и в процессе теста, и оставлять за собой чужой HOME ему нельзя.
// Пустое значение значит снять переменную.
func setEnv(kv map[string]string) func() {
	old := map[string]*string{}
	for k, v := range kv {
		if prev, ok := os.LookupEnv(k); ok {
			p := prev
			old[k] = &p
		} else {
			old[k] = nil
		}
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	return func() {
		for k, v := range old {
			if v == nil {
				os.Unsetenv(k)
				continue
			}
			os.Setenv(k, *v)
		}
	}
}

// devkitCheckout ищет чекаут devkit: настоящий hooks/notify.py прогону нужен,
// потому что формат его журнала и есть контракт ленты. Порядок тот же, что у
// taskctl: DEVKIT_HOME, дерево рядом с рабочей директорией (так смок находит
// себя, когда его зовёт go test из tools/dashboard) и ~/projects/devkit.
func devkitCheckout() string {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, v)
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "..", ".."))
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "projects", "devkit"))
	}
	for _, c := range cands {
		if isFile(filepath.Join(c, "hooks", "notify.py")) {
			return c
		}
	}
	return ""
}

// smokeFixtures кладёт исполняемые фикстуры чужих программ. tmux помнит свои
// сессии файлом: без памяти запуск и стоп не связались бы, а работа не
// появилась бы в списке живых.
func (s *smoke) fixtures() error {
	sessions := s.tmuxListFile()
	if err := smokeWrite(s.boardFile(), smokeBoardJSON+"\n", 0o644); err != nil {
		return err
	}
	if err := smokeWrite(s.boardFile()+".closed", smokeBoardClosedJSON+"\n", 0o644); err != nil {
		return err
	}
	// taskctl отвечает доской из файла, а не одной зашитой строкой: close
	// обязан её править, иначе закрытая задача осталась бы на экране. Каждый
	// вызов ложится в журнал, по нему прогон и сверяет, какой командой дашборд
	// закрыл строку.
	taskctl := fmt.Sprintf(`board=%s
doc=%s
arch=%s
drafts=%s
printf '%%s\n' "$*" >> %s
case "$1 $2" in
"draft list")
  printf '{"drafts":['
  first=1
  for f in "$drafts"/*.md; do
    [ -e "$f" ] || continue
    id=$(basename "$f" .md)
    [ $first = 1 ] || printf ','
    first=0
    printf '{"id":"%%s","title":"%%s","age_words":"сегодня"}' "$id" "$(head -1 "$f")"
  done
  printf ']}\n'
  exit 0
  ;;
"draft drop")
  rm -f "$drafts/$3.md"
  printf '%%s удалён как протухший: %%s\n' "$3" "$5"
  exit 0
  ;;
esac
case "$1" in
set)
  # Правка ранга: сумма считается из разбивки, новая строка ложится и в ответ
  # утилиты, и в файл доски. Файл тут не для красоты, по его отпечатку сервер
  # узнаёт, что помнить прежний ответ больше нельзя.
  id=$2
  rank=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--rank" ] && rank="$a"
    prev="$a"
  done
  if [ -z "$rank" ]; then
    printf 'нечего менять, жду --rank\n' >&2
    exit 1
  fi
  sum=$(printf '%%s' "$rank" | awk -F+ '{s=0; for (i=1;i<=NF;i++) s+=$i; print s}')
  parts=$(printf '%%s' "$rank" | tr '+' ',')
  sed "s/\(\"id\":\"$id\"[^}]*\)\"r\":[0-9]*,\"r_parts\":\[[0-9,]*\]/\1\"r\":$sum,\"r_parts\":[$parts]/" "$board" > "$board.new"
  mv "$board.new" "$board"
  sed "/^| $id /s/| [0-9]* ([0-9+]*) |/| $sum ($rank) |/" "$doc" > "$doc.new"
  mv "$doc.new" "$doc"
  printf '%%s: R -> %%s\n' "$id" "$sum"
  ;;
close)
  cp "$board.closed" "$board"
  grep -v -F "$2" "$doc" > "$doc.new"
  mv "$doc.new" "$doc"
  printf '| %%s | Принятая глазами [приёмка: user] | task | P2 | 2026-01-02 | - |\n' "$2" >> "$arch"
  printf '%%s закрыта 2026-01-02, строка в архиве\n' "$2"
  ;;
*)
  cat "$board"
  ;;
esac
`, shQuote(s.boardFile()), shQuote(s.boardDoc()), shQuote(s.archiveDoc()),
		shQuote(s.draftsDir()), shQuote(s.callsFile()))
	for name, body := range map[string]string{
		"taskctl": taskctl,
		"tmux": fmt.Sprintf(`list=%s
runs=%s
case "$1" in
ls)
  [ -s "$list" ] || exit 1
  cat "$list"
  ;;
new-session)
  prev=""
  cmd=""
  for a in "$@"; do
    [ "$prev" = "-s" ] && printf '%%s\n' "$a" >> "$list"
    prev="$a"
    cmd="$a"
  done
  printf 'сессия: %%s\n' "$cmd" >> "$runs"
  # Команда сессии не только записывается, но и исполняется: «имя доехало до
  # команды» иначе проверялось бы по строке запроса, а не по тому, кого эта
  # строка подняла.
  sh -c "$cmd" >> "$runs" 2>&1 || true
  ;;
capture-pane)
  printf 'Invalid API key. Please run /login\n'
  ;;
kill-session)
  name=${3#=}
  grep -v -x "$name" "$list" > "$list.new"
  mv "$list.new" "$list"
  ;;
esac
exit 0
`, shQuote(sessions), shQuote(s.runsFile())),
		// claude только ищется в PATH до подъёма сессии задачи, звать его
		// прогону незачем.
		"claude": "exit 0\n",
		// agentctl: раскладку подписок печатает машинным видом, а exec ставит имя
		// выбранной подписки и поднимает переданную команду. Обе половины тут те
		// же, что у живой утилиты, и обе нужны прогону: по первой собирается
		// список на экране, по второй имя доезжает до клиента.
		"agentctl": fmt.Sprintf(`runs=%s
case "$1 $2" in
"harness --json")
  cat <<'JSON'
%s
JSON
  ;;
"exec --harness")
  name=$3
  shift 4
  printf 'exec: подписка %%s, команда %%s\n' "$name" "$*" >> "$runs"
  DEVKIT_HARNESS="$name" "$@"
  ;;
esac
exit 0
`, shQuote(s.runsFile()), smokeHarnessJSON(s.harnessHome())),
		// Клиенты подписок: каждый пишет, кем его подняли и с каким заказом.
		// Разными их держит прогон нарочно, иначе выбор подписки был бы неотличим
		// от её отсутствия.
		smokeClientOne: smokeClientBody(s.runsFile(), smokeClientOne, "", ""),
		smokeClientTwo: smokeClientBody(s.runsFile(), smokeClientTwo, s.harnessJournal(), s.bindsFile()),
		// Бэкенд уведомлений: баннер на машине прогона не появляется, а
		// строка в журнале уведомителя пишется как при живой отправке.
		"notify-fake": "exit 0\n",
	} {
		if err := smokeWrite(filepath.Join(s.bin, name), "#!/bin/sh\n"+body, 0o755); err != nil {
			return err
		}
	}
	// Оболочка цикла: подъём заводит tmux-сессию и журнал цели, ключ --say
	// дописывает строку про стоп туда же, где виден ход.
	goalRun := fmt.Sprintf(`#!/usr/bin/env python3
import os
import sys
import time

args = sys.argv[1:]
gid = args[0]
root = args[args.index("-C") + 1] if "-C" in args else "."
log = os.path.join(root, ".devkit", "goal-%%s.log" %% gid)
os.makedirs(os.path.dirname(log), exist_ok=True)
stamp = time.strftime("%%Y-%%m-%%dT%%H:%%M:%%S")
if "--say" in args:
    line = args[args.index("--say") + 1]
    with open(log, "a", encoding="utf-8") as f:
        f.write("%%s %%s\n" %% (stamp, line))
    print(line)
else:
    with open(%s, "a", encoding="utf-8") as f:
        f.write("goal-%%s\n" %% gid)
    with open(log, "a", encoding="utf-8") as f:
        f.write("%%s цикл цели %%s начат прогоном smoke\n" %% (stamp, gid))
    print("цикл цели %%s поднят в tmux-сессии goal-%%s" %% (gid, gid))
`, pyQuote(sessions))
	if err := smokeWrite(filepath.Join(s.root, "devkit", "kit", "skills", "goal-loop", "goal-run.py"),
		goalRun, 0o755); err != nil {
		return err
	}
	// Оболочка конвейера: на стенде она поднимает голову один раз и заказом
	// первого прохода. Проходы, доска и журнал у настоящей оболочки проверяются
	// своей самопроверкой, а тут смотрят, что подписка и заказ доезжают до
	// клиента через неё.
	taskRun := `#!/usr/bin/env python3
import os
import subprocess
import sys

args = sys.argv[1:]
tail = args[args.index("--") + 1:] if "--" in args else []
order = args[args.index("--order") + 1] if "--order" in args else ""
sys.exit(subprocess.run(tail + ["-p", order]).returncode)
`
	return smokeWrite(filepath.Join(s.root, "devkit", "kit", "skills", "board-task", "task-run.py"),
		taskRun, 0o755)
}

// smokeHarnessJSON это машинный вид agentctl harness --json: две включённые
// подписки со своими клиентами, первая по умолчанию. У второй есть своё
// хозяйство: журналы разговоров она пишет туда, и без этого поля стенд не
// увидел бы headless-сессию собственного запуска (DK-362).
func smokeHarnessJSON(home string) string {
	return fmt.Sprintf(`{
  "default": %q,
  "source": "машинный слой стенда",
  "harnesses": [
    {"name": %q, "enabled": true, "default": true, "bin": %q},
    {"name": %q, "enabled": true, "default": false, "bin": %q, "home": %q, "env": ["CONFIG_DIR"]}
  ]
}`, smokeHarnessOne, smokeHarnessOne, smokeClientOne, smokeHarnessTwo, smokeClientTwo, home)
}

// smokeHeadlessID это имя транскрипта, который пишет клиент второй подписки:
// живой клиент назвал бы файл своим session_id, а стенду хватает постоянного
// имени, по нему шаг и ходит на экран агента.
const smokeHeadlessID = "smoke-headless-1"

// harnessHome это каталог хозяйства второй подписки стенда, harnessJournal это
// каталог транскриптов проекта под ним: раскладку имён считает та же функция,
// что и сервер, иначе стенд проверял бы свою выдумку.
func (s *smoke) harnessHome() string { return filepath.Join(s.dir, "harness-home") }
func (s *smoke) harnessJournal() string {
	return filepath.Join(s.harnessHome(), "projects", claudeDirName(s.proj))
}

// bindsFile это реестр чатов стенда: тот же путь от дома, по которому его
// читает сервер.
func (s *smoke) bindsFile() string { return sessions.Path(s.home) }

// smokeClientBody это тело фикстуры клиента: имя подписки он берёт из
// окружения, как настоящий клиент берёт оттуда свой каталог конфигурации.
// Названный каталог журнала клиент заполняет транскриптом разговора, как это
// делает живой клиент в headless-запуске: заказ первой репликой человека,
// дальше ответ. По нему дашборд и узнаёт, чем занята поднятая работа.
// Строку реестра фикстура кладёт сама, за SessionStart-хук devkit
// (hooks/session-task.py): на живой машине задачу сессии называет он, из
// DEVKIT_TASK, а угадывание по первой реплике снято, и без этой строки
// поднятый разговор задачи не знает вовсе.
func smokeClientBody(runs, name, journal, binds string) string {
	// Заказ это последний аргумент команды, а не позиционный: между именем
	// клиента и -p стоят флаги запуска (--permission-mode auto у второй
	// подписки), и взятый по номеру аргумент ловил бы флаг вместо заказа.
	body := fmt.Sprintf("for a; do said=\"$a\"; done\n"+
		"printf '%s: подписка %%s, заказ %%s\\n' \"$DEVKIT_HARNESS\" \"$said\" >> %s\n",
		name, shQuote(runs))
	if journal == "" {
		return body + "exit 0\n"
	}
	return body + fmt.Sprintf(`dir=%s
mkdir -p "$dir"
# Кавычки заказа экранируются: в заказе стоит правило плана с полями text и
# state в кавычках, и сырой подстановкой оно ломало бы JSON транскрипта.
said=$(printf '%%s' "$said" | sed 's/"/\\"/g')
{
  printf '{"type":"user","message":{"role":"user","content":"%%s"},"timestamp":"2026-08-10T10:00:01.000Z","promptSource":"sdk","gitBranch":"main"}\n' "$said"
  printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Взял в работу."}]},"timestamp":"2026-08-10T10:00:02.000Z"}\n'
} > "$dir/%s.jsonl"
if [ -n "$DEVKIT_TASK" ]; then
  binds=%s
  mkdir -p "$(dirname "$binds")"
  printf '%%s сессия %%s задача %%s проект demo дерево - транскрипт %%s источник заказ повод startup tmux %%s\n' \
    "$(date +%%Y-%%m-%%dT%%H:%%M:%%S)" %s "$DEVKIT_TASK" "$dir/%s.jsonl" "${DEVKIT_TMUX:--}" >> "$binds"
fi
exit 0
`, shQuote(journal), smokeHeadlessID, shQuote(binds), shQuote(smokeHeadlessID), smokeHeadlessID)
}

// Файлы стенда, которые правит фикстура taskctl: доска машинным видом, файл
// доски проекта, архив и журнал вызовов утилиты.
func (s *smoke) boardFile() string { return filepath.Join(s.dir, "board.json") }

// runsFile это журнал поднятых сессий: строка команды, строка agentctl exec и
// строка клиента, который в итоге поднялся.
func (s *smoke) runsFile() string   { return filepath.Join(s.dir, "runs.log") }
func (s *smoke) callsFile() string  { return filepath.Join(s.dir, "taskctl.calls") }
func (s *smoke) boardDoc() string   { return filepath.Join(s.proj, "docs", "TASKS.md") }
func (s *smoke) archiveDoc() string { return filepath.Join(s.proj, "docs", "TASKS-archive.md") }
func (s *smoke) draftsDir() string  { return filepath.Join(s.proj, "docs", "tasks", "drafts") }
func (s *smoke) draftFile() string  { return filepath.Join(s.draftsDir(), smokeDraft+".md") }

// tmuxListFile это список живых сессий фикстуры tmux: подъём дописывает туда
// имя, kill-session убирает, а шаг смерти подъёма снимает имя сам, изображая
// клиента, который вышел, не назвавшись в реестре.
func (s *smoke) tmuxListFile() string { return filepath.Join(s.dir, "tmux.sessions") }

// pyQuote квотит строку для python-фикстуры.
func pyQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func newSmoke(dir string) (*smoke, error) {
	s := &smoke{
		dir:  dir,
		home: filepath.Join(dir, "home"),
		bin:  filepath.Join(dir, "bin"),
	}
	s.root = filepath.Join(s.home, "projects")
	s.proj = filepath.Join(s.root, "demo")
	if err := smokeWrite(filepath.Join(s.proj, "docs", "TASKS.md"), smokeBoardDoc, 0o644); err != nil {
		return nil, err
	}
	if err := smokeWrite(filepath.Join(s.proj, "docs", "tasks", smokeGoal+".md"), smokeGoalDoc, 0o644); err != nil {
		return nil, err
	}
	if err := smokeWrite(filepath.Join(s.proj, "docs", "TASKS-archive.md"), smokeArchiveDoc, 0o644); err != nil {
		return nil, err
	}
	if err := smokeWrite(s.draftFile(), smokeDraftDoc, 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.bin, 0o755); err != nil {
		return nil, err
	}
	if err := s.fixtures(); err != nil {
		return nil, err
	}
	// Уведомитель настоящий: он лежит в чекауте devkit, а сервер ищет его в
	// корнях конфига, поэтому чекаут смотрит в синтетический корень своей
	// частью hooks.
	checkout := devkitCheckout()
	if checkout == "" {
		return nil, fmt.Errorf("чекаут devkit не нашёлся: прогон гоняет настоящий hooks/notify.py, " +
			"указать чекаут: DEVKIT_HOME=<путь>")
	}
	if err := os.Symlink(filepath.Join(checkout, "hooks"), filepath.Join(s.root, "devkit", "hooks")); err != nil {
		return nil, err
	}
	s.restore = append(s.restore, setEnv(map[string]string{
		"HOME": s.home,
		"PATH": s.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		// Своё дерево devkit у прогона синтетическое: оболочка цикла лежит
		// фикстурой в его корне, и переменная разработчика тут увела бы сервер
		// к настоящей оболочке машины. Чекаут для уведомителя смок нашёл
		// раньше, отдельным вопросом.
		"DEVKIT_HOME": "",
		// Уведомитель обязан дойти до журнала: выключатель и опрос фокуса
		// сняты, а баннер уходит в пустышку.
		"DEVKIT_NOTIFY_OFF":     "",
		"DEVKIT_NOTIFY_BACKEND": "notify-fake",
		"DEVKIT_NOTIFY_FOCUS":   "off",
	}))
	// Живые утилиты машины не зовутся: taskctl сервер ищет сначала рядом с
	// собственным бинарём, и рядом на время прогона лежит фикстура.
	oldExe := exeDir
	exeDir = func() string { return s.bin }
	s.restore = append(s.restore, func() { exeDir = oldExe })
	// Штатный каталог кита у прогона тоже свой: путь поднятой сессии дашборд
	// собирает с ним первым (sessionPath, DK-549), и настоящий ~/go/bin поднял
	// бы живого клиента вместо фикстуры.
	oldKit := kitDir
	kitDir = func() string { return s.bin }
	s.restore = append(s.restore, func() { kitDir = oldKit })

	static, err := fs.Sub(embedded, "static")
	if err != nil {
		return nil, err
	}
	cfg := &Config{Home: s.home, Roots: []string{s.root}, Token: smokeToken}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := httpServer(newServer(cfg, static, nil).handler())
	go srv.Serve(ln)
	s.restore = append(s.restore, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	s.url = "http://" + ln.Addr().String()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	s.client = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	return s, nil
}

// call ходит в API и разбирает ответ в v; не тот код ответа это ошибка шага со
// словами сервера, а не молчаливое несовпадение.
func (s *smoke) call(method, path, body string, want int, v any) error {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+path, rdr)
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: код %d, ждал %d (%s)", method, path, resp.StatusCode, want, strings.TrimSpace(string(data)))
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s %s: ответ не разобрался: %v (%s)", method, path, err, strings.TrimSpace(string(data)))
	}
	return nil
}

func (s *smoke) login() error {
	return s.call("POST", "/api/login", fmt.Sprintf(`{"token": %q}`, smokeToken), http.StatusOK, nil)
}

type smokeBoard struct {
	Board struct {
		Prefix   string `json:"prefix"`
		Sections []struct {
			Key  string `json:"key"`
			Rows []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				R     int    `json:"r"`
				// Run это признак идущей работы в самой строке: по нему строка
				// рисует «Стоп» и отличает оборванную работу от очереди.
				Run string `json:"run"`
				// Stage и StageSince это вид деятельности и начало этапа: их
				// кладёт в запись конвейер, а ручка доски отдаёт строкой (DK-338).
				Stage      string `json:"stage"`
				StageSince int64  `json:"stage_since"`
				// Waiting это состояние «ждёт человека» (DK-433): признак
				// ожидания, парковка вопросом либо повод из журнала.
				Waiting *Waiting `json:"waiting"`
			} `json:"rows"`
		} `json:"sections"`
	} `json:"board"`
	Works []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Via  string `json:"via"`
	} `json:"works"`
	Errors []string `json:"errors"`
}

func (s *smoke) board() (smokeBoard, error) {
	var v smokeBoard
	err := s.call("GET", "/api/projects/demo/board", "", http.StatusOK, &v)
	return v, err
}

// stepBoard: доска со статусами и текущими работами, первый пункт DoD.
func (s *smoke) stepBoard() (string, error) {
	var list struct {
		Projects []struct {
			Name     string         `json:"name"`
			Sections map[string]int `json:"sections"`
			Error    string         `json:"error"`
		} `json:"projects"`
	}
	if err := s.call("GET", "/api/projects", "", http.StatusOK, &list); err != nil {
		return "", err
	}
	if len(list.Projects) != 1 || list.Projects[0].Name != "demo" {
		return "", fmt.Errorf("в корне конфига ждал один проект demo, пришло %d", len(list.Projects))
	}
	if e := list.Projects[0].Error; e != "" {
		return "", fmt.Errorf("доска demo не прочиталась: %s", e)
	}
	v, err := s.board()
	if err != nil {
		return "", err
	}
	if v.Board.Prefix != "XR" {
		return "", fmt.Errorf("префикс доски %q, ждал XR", v.Board.Prefix)
	}
	rows := map[string]string{}
	for _, sec := range v.Board.Sections {
		for _, row := range sec.Rows {
			rows[row.ID] = sec.Key
		}
	}
	if rows[smokeGoal] != "in-progress" || rows[smokeTask] != "backlog" {
		return "", fmt.Errorf("строки доски не по секциям: %v", rows)
	}
	if len(v.Works) != 0 {
		return "", fmt.Errorf("до запуска работ быть не должно, пришло %d", len(v.Works))
	}
	// Признак идущей работы приезжает в самой строке: до запуска сессий у цели
	// на этой машине нет ни одной, и признака у строки нет тоже. Прежде тут
	// стояло слово other, и экран объявлял такую строку взятой в другом месте;
	// снято оно вместе с припиской «исполнителя не видно» (rowRun в tasks.go).
	if run := boardRun(v, smokeGoal); run != "" {
		return "", fmt.Errorf("признак работы строки %s %q, до запуска ждал пустой", smokeGoal, run)
	}
	if run := boardRun(v, smokeTask); run != "" {
		return "", fmt.Errorf("строка %s в Backlog помечена работой %q", smokeTask, run)
	}
	return fmt.Sprintf("секции %v, строк %d, работ нет, признака работы на строках нет",
		list.Projects[0].Sections, len(rows)), nil
}

// boardStage достаёт вид деятельности и начало этапа из строки доски.
func boardStage(v smokeBoard, id string) (string, int64) {
	for _, sec := range v.Board.Sections {
		for _, row := range sec.Rows {
			if row.ID == id {
				return row.Stage, row.StageSince
			}
		}
	}
	return "", 0
}

// stepStage: вид деятельности едет полем строки доски. Запись этапа кладёт
// конвейер, а не дашборд, поэтому шаг пишет её тем же вызовом, каким её пишет
// agentctl pick --record, и смотрит, что ручка доски отдала вид и время начала
// рядом с готовым признаком Run. Тут же проверяется оборванный этап: за строкой
// в Backlog живой сессии нет, и запись за неё выдавать работу не должна.
func (s *smoke) stepStage() (string, error) {
	since := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	if err := stage.Open(s.home, s.proj, smokeGoal, stage.Dev, "субагент opus/high по вердикту pick", since); err != nil {
		return "", err
	}
	if err := stage.Open(s.home, s.proj, smokeTask, stage.Review, "субагент sonnet/high по вердикту pick", since); err != nil {
		return "", err
	}
	v, err := s.board()
	if err != nil {
		return "", err
	}
	kind, at := boardStage(v, smokeGoal)
	if kind != stage.Dev {
		return "", fmt.Errorf("вид деятельности строки %s %q, ждал %q", smokeGoal, kind, stage.Dev)
	}
	if at != since.Unix() {
		return "", fmt.Errorf("начало этапа строки %s %d, ждал %d", smokeGoal, at, since.Unix())
	}
	if kind, _ := boardStage(v, smokeTask); kind != "" {
		return "", fmt.Errorf("оборванный этап строки %s выдан за работу словом %q", smokeTask, kind)
	}
	// Ожидание снаружи живой сессии не требует по смыслу, и та же строка обязана
	// его показать.
	if err := stage.Open(s.home, s.proj, smokeTask, stage.Outside, "проверка после выката", since); err != nil {
		return "", err
	}
	v, err = s.board()
	if err != nil {
		return "", err
	}
	if kind, _ := boardStage(v, smokeTask); kind != stage.Outside {
		return "", fmt.Errorf("ожидание снаружи строки %s пришло как %q", smokeTask, kind)
	}
	return fmt.Sprintf("строка %s несёт «%s» с %s, оборванный этап %s не выдан за работу",
		smokeGoal, stage.Dev, since.Format("15:04"), smokeTask), nil
}

// boardRun достаёт признак идущей работы из строки доски.
func boardRun(v smokeBoard, id string) string {
	for _, sec := range v.Board.Sections {
		for _, row := range sec.Rows {
			if row.ID == id {
				return row.Run
			}
		}
	}
	return ""
}

// stepStart: запуск работы, второй пункт DoD.
// stepSearch: поиск задач, десятый сценарий цели DK-327. Проверяется тут стык
// трёх источников сразу: живая доска приходит из кэша сервера, архив
// разбирается файлом, а текст ищется обходом docs/tasks. Каждый по
// отдельности проверен тестом, а вот сойтись в одной выдаче они могут только
// на живом проекте с файлами на диске.
func (s *smoke) stepSearch() (string, error) {
	ask := func(q string) (searchResp, error) {
		var v searchResp
		err := s.call("GET", "/api/projects/demo/search?q="+url.QueryEscape(q), "", http.StatusOK, &v)
		return v, err
	}
	rows := func(v searchResp, key string) []searchRow {
		for _, g := range v.Groups {
			if g.Key == key {
				return g.Rows
			}
		}
		return nil
	}
	live, err := ask("соседка")
	if err != nil {
		return "", err
	}
	board := rows(live, "board")
	if len(board) != 1 || board[0].ID != smokeTask || board[0].Sect != "backlog" {
		return "", fmt.Errorf("поиск по живой доске: %+v, ждал %s из Backlog", board, smokeTask)
	}
	closed, err := ask("закрытая")
	if err != nil {
		return "", err
	}
	arch := rows(closed, "archive")
	if len(arch) != 1 || arch[0].ID != "XR-001" || arch[0].Closed != "2026-01-01" {
		return "", fmt.Errorf("поиск по архиву: %+v, ждал XR-001 с датой закрытия", arch)
	}
	if arch[0].R != 0 || arch[0].Cost != "" {
		return "", fmt.Errorf("архивная строка выдумала ранг или цену: %+v", arch[0])
	}
	text, err := ask("переписка через файл")
	if err != nil {
		return "", err
	}
	hits := rows(text, "text")
	if len(hits) != 1 || hits[0].ID != smokeGoal || !strings.Contains(hits[0].Quote, "переписка через файл") {
		return "", fmt.Errorf("поиск по тексту задач: %+v, ждал цитату из файла %s", hits, smokeGoal)
	}
	empty, err := ask("тарабарщина")
	if err != nil {
		return "", err
	}
	if empty.Note == "" {
		return "", fmt.Errorf("пустая выдача не сказала, где искали: %+v", empty)
	}
	return fmt.Sprintf("доска: %s, архив: %s от %s, текст: %s строка %d; пустая выдача подписана",
		board[0].ID, arch[0].ID, arch[0].Closed, hits[0].File, hits[0].Line), nil
}

func (s *smoke) stepStart() (string, error) {
	var v struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
		Message string `json:"message"`
	}
	if err := s.call("POST", "/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, smokeGoal),
		http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.Kind != "goal" || v.Session != "goal-"+smokeGoal {
		return "", fmt.Errorf("запуск поднял не цель: kind=%q session=%q", v.Kind, v.Session)
	}
	return v.Message, nil
}

// stepWorks: поднятая работа видна доской как живая, и остановить её можно
// только зная это. Знает про неё и сама строка: признак в её данных это то, по
// чему список задач рисует пометку и «Стоп» (DK-317).
func (s *smoke) stepWorks() (string, error) {
	v, err := s.board()
	if err != nil {
		return "", err
	}
	if run := boardRun(v, smokeGoal); run != "tmux" {
		return "", fmt.Errorf("признак работы строки %s %q, после запуска ждал tmux: "+
			"строка списка снова не знает про идущую работу", smokeGoal, run)
	}
	for _, w := range v.Works {
		if w.ID == smokeGoal && w.Kind == "goal" && w.Via == "tmux" {
			return fmt.Sprintf("работа %s ведётся tmux-сессией goal-%s, строка помечена признаком tmux", w.ID, w.ID), nil
		}
	}
	return "", fmt.Errorf("поднятой работы %s в списке живых нет: %+v", smokeGoal, v.Works)
}

const smokeMessage = "прогон smoke: проверь ленту уведомлений"

// stepMessage: сообщение человека уходит в состояние цели, третий пункт DoD.
func (s *smoke) stepMessage() (string, error) {
	var v struct {
		Line    string `json:"line"`
		Message string `json:"message"`
		Note    string `json:"note"`
	}
	if err := s.call("POST", "/api/projects/demo/goals/"+smokeGoal+"/message",
		fmt.Sprintf(`{"text": %q}`, smokeMessage), http.StatusOK, &v); err != nil {
		return "", err
	}
	var pending struct {
		Pending []string `json:"pending"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/message", "",
		http.StatusOK, &pending); err != nil {
		return "", err
	}
	if len(pending.Pending) != 1 || !strings.Contains(pending.Pending[0], smokeMessage) {
		return "", fmt.Errorf("сообщение не легло в «Входящие»: %v", pending.Pending)
	}
	// Виток читает файл цели, а не ответ API: сообщение обязано лежать там,
	// откуда он его берёт первым шагом.
	doc, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	if lines := inboxLines(string(doc)); len(lines) != 1 {
		return "", fmt.Errorf("во «Входящих» файла цели %d строк, ждал одну", len(lines))
	}
	// Повтор той же реплики: второе нажатие «Отправить» по неотвечающей связи
	// не должно оборачиваться вторым сообщением витку (DK-281).
	if err := s.call("POST", "/api/projects/demo/goals/"+smokeGoal+"/message",
		fmt.Sprintf(`{"text": %q}`, smokeMessage), http.StatusOK, &v); err != nil {
		return "", err
	}
	again, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	if lines := inboxLines(string(again)); len(lines) != 1 {
		return "", fmt.Errorf("повтор сообщения завёл вторую строку: во «Входящих» %d", len(lines))
	}
	note := "коммит прошёл"
	if v.Note != "" {
		// Синтетический проект не репозиторий, и провал коммита тут штатен:
		// важно, что сервер называет его словами, а не глотает.
		note = "правка на месте, git назван словами"
	}
	return fmt.Sprintf("строка «%s» ждёт витка, %s", v.Line, note), nil
}

// stepTaskMessage: ответ задаче ложится безадресной строкой во вход задачи
// основного чекаута (LLD DK-430, решение 2). Шаг идёт до файла, а не до ответа
// API: читают этот вход подхват и сторожок, и важно, что строка лежит там, где
// они её ищут, и без адресата, иначе сторожок не счёл бы её ответом задаче.
func (s *smoke) stepTaskMessage() (string, error) {
	const said = "прогон smoke: отвечаю задаче с карточки"
	var v struct {
		Chat    string `json:"chat"`
		Line    string `json:"line"`
		Tree    string `json:"tree"`
		Message string `json:"message"`
	}
	if err := s.call("POST", "/api/projects/demo/tasks/"+smokeTask+"/message",
		fmt.Sprintf(`{"text": %q}`, said), http.StatusOK, &v); err != nil {
		return "", err
	}
	src := filepath.Join(s.proj, ".devkit", "chat", "task-"+smokeTask+".in")
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("вход задачи не записался: %v", err)
	}
	if !strings.Contains(string(data), said) {
		return "", fmt.Errorf("реплики нет во входе %s:\n%s", src, data)
	}
	if strings.Contains(string(data), ", сессии ") {
		return "", fmt.Errorf("реплика задаче ушла с адресатом, и ответом задаче сторожок её не сочтёт:\n%s", data)
	}
	// Повтор с устаревшего экрана второй строки не заводит: сторожок разбудил
	// бы строку дважды.
	if err := s.call("POST", "/api/projects/demo/tasks/"+smokeTask+"/message",
		fmt.Sprintf(`{"text": %q}`, said), http.StatusOK, &v); err != nil {
		return "", err
	}
	again, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if n := strings.Count(string(again), said); n != 1 {
		return "", fmt.Errorf("повтор завёл вторую строку: во входе %d реплик", n)
	}
	return fmt.Sprintf("строка «%s» лежит в чате %s чекаута без адресата", v.Line, v.Chat), nil
}

// boardWaiting достаёт состояние ожидания строки из ответа доски.
func boardWaiting(v smokeBoard, id string) *Waiting {
	for _, sec := range v.Board.Sections {
		for _, row := range sec.Rows {
			if row.ID == id {
				return row.Waiting
			}
		}
	}
	return nil
}

// stepWaiting: задача, вставшая на вопросе, видна прямо со строки доски. Шаг
// кладёт признак ожидания тем же internal/chat, каким его пишет taskctl ask, и
// смотрит, что строка принесла состояние словом, вопрос, срок и подпись
// источника. Снятый признак ожидание убирает: молчание тут значит «никто
// никого не ждёт», и отличить его от вечно горящего чипа надо прогоном, а не
// глазами.
func (s *smoke) stepWaiting() (string, error) {
	until := time.Now().Add(6 * time.Minute).Truncate(time.Second)
	const asked = "прогон smoke: чинить копией или общим модулем?"
	ask := chat.Ask{Until: until, Session: "smoke-headless-1", Task: smokeTask,
		Questions: []chat.Question{{Text: asked}}}
	if err := chat.WriteAsk(s.proj, chat.TaskName(smokeTask), ask); err != nil {
		return "", err
	}
	v, err := s.board()
	if err != nil {
		return "", err
	}
	w := boardWaiting(v, smokeTask)
	if w == nil {
		return "", fmt.Errorf("строка %s не назвалась ждущей, хотя признак ожидания лежит во входе задачи", smokeTask)
	}
	if w.Source != waitAsk || w.Note != waitAskNote {
		return "", fmt.Errorf("источник ожидания %q с подписью %q, ждал %q", w.Source, w.Note, waitAsk)
	}
	if w.Until != until.Unix() {
		return "", fmt.Errorf("срок ожидания %d, ждал %d: обратный отсчёт чипа считается по нему", w.Until, until.Unix())
	}
	if len(w.Questions) != 1 || w.Questions[0] != asked {
		return "", fmt.Errorf("вопрос до строки доски не доехал: %q", w.Questions)
	}
	if err := chat.DropAsk(s.proj, chat.TaskName(smokeTask)); err != nil {
		return "", err
	}
	if v, err = s.board(); err != nil {
		return "", err
	}
	if left := boardWaiting(v, smokeTask); left != nil {
		return "", fmt.Errorf("снятый признак оставил ожидание на строке: %+v", left)
	}
	return fmt.Sprintf("строка %s несёт «%s» до %s, источник: %s; снятый признак ожидание убрал",
		smokeTask, w.State, until.Format("15:04:05"), w.Note), nil
}

// stepJournal: журнал цели читается из раздела «Журнал» её файла. Шаг идёт до
// запуска работы: пока цель не гонялась оболочкой, файла .devkit/goal-XR-100.log
// у неё нет, и строки обязаны прийти из файла цели, найденного по живой ссылке
// строки доски (она стоит markdown-разметкой относительно docs/, как её пишет
// taskctl). Шаг ловит ровно ту поломку, которую нашла живая проверка DK-255:
// разметка, принятая за путь, оставляла экран агента без журнала.
func (s *smoke) stepJournal() (string, error) {
	var v struct {
		Exists bool     `json:"exists"`
		Source string   `json:"source"`
		Sign   string   `json:"source_note"`
		Note   string   `json:"note"`
		Lines  []string `json:"lines"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/log", "", http.StatusOK, &v); err != nil {
		return "", err
	}
	if !v.Exists || v.Source != "goal-file" {
		return "", fmt.Errorf("журнал цели не прочитан из её файла: source %q, %s", v.Source, v.Note)
	}
	if !strings.Contains(v.Sign, "docs/tasks/"+smokeGoal+".md") {
		return "", fmt.Errorf("источник журнала подписан %q", v.Sign)
	}
	last := ""
	if len(v.Lines) > 0 {
		last = v.Lines[len(v.Lines)-1]
	}
	if !strings.Contains(last, "цель заведена прогоном smoke") {
		return "", fmt.Errorf("записи витка в журнале нет: %v", v.Lines)
	}
	return fmt.Sprintf("%d строк, источник назван: %s", len(v.Lines), v.Sign), nil
}

// stepComposition: состав цели сабтасками. Проверяется весь путь до экрана:
// нарезка читается из раздела «Задачи цели» файла цели, живая задача берёт
// статус со строки доски, а закрытая из архива, куда её строка уехала.
func (s *smoke) stepComposition() (string, error) {
	var v struct {
		File  string     `json:"file"`
		Note  string     `json:"note"`
		Tasks []goalTask `json:"tasks"`
		Count goalCounts `json:"counts"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/tasks", "", http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.Note != "" || len(v.Tasks) != 2 {
		return "", fmt.Errorf("состав цели не прочитан: %s (%d задач)", v.Note, len(v.Tasks))
	}
	live, closed := v.Tasks[0], v.Tasks[1]
	if live.ID != smokeTask || live.Sect != "backlog" || live.Title == "" {
		return "", fmt.Errorf("живая задача состава пришла без строки доски: %+v", live)
	}
	if !closed.Done || closed.Closed == "" {
		return "", fmt.Errorf("закрытая задача состава пришла без архива: %+v", closed)
	}
	if v.Count.Closed != 1 || v.Count.Ahead != 1 {
		return "", fmt.Errorf("счётчики состава не сошлись: %+v", v.Count)
	}
	return fmt.Sprintf("%d задачи из %s: %s %s, %s закрыта %s",
		v.Count.Total, v.File, live.ID, live.Section, closed.ID, closed.Closed), nil
}

func (s *smoke) goalPath() string {
	return filepath.Join(s.proj, "docs", "tasks", smokeGoal+".md")
}

// stepDelivered: доставка реплики идущему витку. Живого подхвата в прогоне
// нет, его изображает та же отметка в .devkit/goal-<ID>.mail, какую на живой
// машине кладёт hooks/chat-in.py перед доставкой. Проверяется этим стык дашборда
// с подхватом: отметка лежит там, откуда сервер её читает, и лежащая строка
// перестаёт числиться ждущей витка.
func (s *smoke) stepDelivered() (string, error) {
	doc, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	lines := inboxLines(string(doc))
	if len(lines) != 1 {
		return "", fmt.Errorf("доставлять нечего: во «Входящих» %d строк", len(lines))
	}
	session := "8f2a1c30-1111-2222-3333-444455556666"
	mark := time.Now().Format(mailStamp) + " " + session + "\n" + lines[0] + "\n"
	if err := smokeWrite(mailPath(s.proj, smokeGoal), mark, 0o644); err != nil {
		return "", err
	}
	var v struct {
		Pending   []string   `json:"pending"`
		Delivered []mailMark `json:"delivered"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/message", "",
		http.StatusOK, &v); err != nil {
		return "", err
	}
	if len(v.Delivered) != 1 || v.Delivered[0].Line != lines[0] {
		return "", fmt.Errorf("отметка доставки не доехала до ручки чата: %+v", v.Delivered)
	}
	if v.Delivered[0].Session != session || v.Delivered[0].At == "" {
		return "", fmt.Errorf("доставка приехала без сессии или без времени: %+v", v.Delivered[0])
	}
	if len(v.Pending) != 1 {
		return "", fmt.Errorf("доставленная строка ушла из «Входящих»: %v", v.Pending)
	}
	return "строка доставлена витку " + session[:8] + ", во «Входящих» она по-прежнему лежит", nil
}

// stepTurn: подхват сообщения витком, четвёртый пункт DoD. Живого витка тут
// нет, его изображает та же правка файла цели, которую делает шаг 5 скилла
// goal-loop: строка уходит из «Входящих», а в «Журнал» встаёт запись про
// подхват. Проверяется этим ровно стык дашборда с витком: сообщение лежит
// там, откуда виток его читает, и убранное перестаёт числиться ждущим.
func (s *smoke) stepTurn() (string, error) {
	doc, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	taken := inboxLines(string(doc))
	if len(taken) != 1 {
		return "", fmt.Errorf("витку нечего подхватывать: %v", taken)
	}
	out := []string{}
	for _, ln := range strings.Split(string(doc), "\n") {
		if strings.TrimSpace(ln) == "- "+taken[0] {
			continue
		}
		out = append(out, ln)
	}
	turn := fmt.Sprintf("- %s, виток прогона smoke подхватил сообщение с дашборда; continue",
		time.Now().Format("2006-01-02"))
	doc2 := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n" + turn + "\n"
	if err := os.WriteFile(s.goalPath(), []byte(doc2), 0o644); err != nil {
		return "", err
	}
	var pending struct {
		Pending   []string   `json:"pending"`
		Delivered []mailMark `json:"delivered"`
		Note      string     `json:"note"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/message", "",
		http.StatusOK, &pending); err != nil {
		return "", err
	}
	if len(pending.Pending) != 0 {
		return "", fmt.Errorf("подхваченное сообщение всё ещё ждёт витка: %v", pending.Pending)
	}
	if pending.Note == "" {
		return "", fmt.Errorf("опустевшие «Входящие» остались без слов: пустота неотличима от поломки")
	}
	// Отметка доставки шага 8 осталась на месте, а строки под ней уже нет:
	// подхваченное витком не должно возвращаться доставленным.
	if len(pending.Delivered) != 0 {
		return "", fmt.Errorf("отставшая отметка выдала подхваченную строку за доставленную: %+v", pending.Delivered)
	}
	return "сообщение подхвачено записью витка, дашборд говорит: " + pending.Note, nil
}

// openFeed открывает живую ленту и разбирает её поток в фоне: события и
// пустоты складываются по своим каналам, чтобы шаг стопа ждал именно
// уведомления, а не любой строки.
func (s *smoke) openFeed() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.restore = append(s.restore, cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", s.url+"/api/notifications?stream=1&kind=stop", nil)
	if err != nil {
		return err
	}
	// Своего срока у потока нет: он живёт до конца прогона.
	client := &http.Client{Jar: s.client.Jar}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		resp.Body.Close()
		return fmt.Errorf("лента ответила не потоком, а %q", ct)
	}
	s.items = make(chan Notification, 32)
	s.notes = make(chan string, 8)
	s.feedErr = make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		r := bufio.NewReader(resp.Body)
		event, data := "", ""
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				s.feedErr <- err
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				if event == "note" {
					s.notes <- data
				} else {
					var n Notification
					if json.Unmarshal([]byte(data), &n) == nil {
						s.items <- n
					}
				}
				event, data = "", ""
			}
		}
	}()
	return nil
}

// stepFeedOpen: лента открыта потоком до стопа, иначе «без перезагрузки
// страницы» проверить нечем.
func (s *smoke) stepFeedOpen() (string, error) {
	if err := s.openFeed(); err != nil {
		return "", err
	}
	select {
	case note := <-s.notes:
		return "поток открыт, пустота названа словами: " + note, nil
	case n := <-s.items:
		return "поток открыт, в хвосте уже лежит " + n.Title, nil
	case err := <-s.feedErr:
		return "", fmt.Errorf("поток оборвался: %v", err)
	case <-time.After(10 * time.Second):
		return "", fmt.Errorf("лента молчит: за 10с не пришло ни события, ни слов про пустоту")
	}
}

// stepStop: стоп работы, пятый пункт DoD. Стоп идёт по тому же API, что и
// кнопкой с телефона.
func (s *smoke) stepStop() (string, error) {
	var v struct {
		State   string `json:"state"`
		Message string `json:"message"`
		Note    string `json:"note"`
	}
	if err := s.call("DELETE", "/api/projects/demo/runs/"+smokeGoal, "", http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.State != "стоп" {
		return "", fmt.Errorf("стоп ответил состоянием %q", v.State)
	}
	if v.Note != "" {
		return "", fmt.Errorf("стоп прошёл с припиской: %s", v.Note)
	}
	log, err := os.ReadFile(filepath.Join(s.proj, ".devkit", "goal-"+smokeGoal+".log"))
	if err != nil {
		return "", fmt.Errorf("журнал цикла не прочитался: %v", err)
	}
	if !strings.Contains(string(log), "стоп из дашборда") {
		return "", fmt.Errorf("строки про стоп нет в журнале цикла: %s", log)
	}
	board, err := s.board()
	if err != nil {
		return "", err
	}
	if len(board.Works) != 0 {
		return "", fmt.Errorf("после стопа работа всё ещё живая: %+v", board.Works)
	}
	// После стопа живой работы нет, транскриптов у цели на этой машине не
	// заводилось вовсе (фикстура tmux их не пишет), и признак со строки уходит
	// целиком: запустить её снова можно той же кнопкой, какой запускают
	// нетронутую.
	if run := boardRun(board, smokeGoal); run != "" {
		return "", fmt.Errorf("признак работы строки %s после стопа %q, ждал пустой", smokeGoal, run)
	}
	return "сессия снята, строка про стоп в журнале цикла, живых работ нет, признак со строки ушёл", nil
}

// stepFeedStop: уведомление о стопе доезжает в открытую ленту, последний
// пункт DoD.
func (s *smoke) stepFeedStop() (string, error) {
	deadline := time.After(30 * time.Second)
	for {
		select {
		case n := <-s.items:
			if n.Kind != "stop" || n.ID != smokeGoal {
				continue
			}
			// Задача и проект приехали полями события, а не разбором текста
			// баннера (DK-323): проекта в тексте нет вовсе, и пустое поле тут
			// значит, что поля до ленты не доехали.
			if n.Project != "demo" {
				return "", fmt.Errorf("событие пришло без проекта в поле: %+v", n)
			}
			delivered := "баннер ушёл бэкендом " + n.Backend
			if !n.Sent {
				// Прогон живёт в песочнице, и уведомитель туда баннера не
				// шлёт: событие в ленте от этого не пропадает, а причина
				// стоит рядом с ним.
				delivered = "баннера не было: " + n.Result
			}
			return fmt.Sprintf("«%s» (задача %s проекта %s, повод %s, %s)",
				n.Title, n.ID, n.Project, n.Reason, delivered), nil
		case err := <-s.feedErr:
			return "", fmt.Errorf("поток оборвался до уведомления: %v", err)
		case <-deadline:
			return "", fmt.Errorf("уведомления о стопе %s в ленте не дождались за 30с", smokeGoal)
		}
	}
}

const smokeIdleMessage = "прогон smoke: пишу уже остановленному циклу"

// stepIdleMessage: сообщение при стоящем цикле не притворяется доставленным.
// Шаг идёт после стопа, и в этом вся его суть: работы нет, поднимать
// «Входящие» некому, и ручка обязана сказать это словами, а не положить строку
// молча, как она делала до DK-319.
func (s *smoke) stepIdleMessage() (string, error) {
	var v struct {
		Line    string `json:"line"`
		Message string `json:"message"`
		Idle    bool   `json:"idle"`
	}
	if err := s.call("POST", "/api/projects/demo/goals/"+smokeGoal+"/message",
		fmt.Sprintf(`{"text": %q}`, smokeIdleMessage), http.StatusOK, &v); err != nil {
		return "", err
	}
	if !v.Idle || !strings.Contains(v.Message, "не идёт") {
		return "", fmt.Errorf("ответ при стоящем цикле не назвал его стоящим: idle=%v, «%s»", v.Idle, v.Message)
	}
	// Строка при этом ложится во «Входящие»: отказ от записи был бы потерей
	// сообщения, а поднятый виток прочитает её первым шагом.
	doc, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	for _, ln := range inboxLines(string(doc)) {
		if strings.Contains(ln, smokeIdleMessage) {
			return v.Message, nil
		}
	}
	return "", fmt.Errorf("строки сообщения нет во «Входящих» файла цели: %v", inboxLines(string(doc)))
}

// stepCloseAccepted: закрытие принятой задачи идёт без витка. Нажатие
// «Закрыть» у строки с пользовательской приёмкой это тот же POST /runs, что и у
// любой работы, но сессии агента за ним нет: человек уже принял задачу глазами,
// и сервер зовёт taskctl close сам. Шаг сверяет вызов по журналу фикстуры и по
// строке синтетической доски, а вторым нажатием ещё и ответ устаревшему экрану:
// закрытая задача обязана называть себя закрытой, а не пропавшей с доски.
func (s *smoke) stepCloseAccepted() (string, error) {
	var v struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
		Message string `json:"message"`
		Note    string `json:"note"`
	}
	if err := s.call("POST", "/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, smokeAccepted),
		http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.Kind != "close" || v.Session != "" {
		return "", fmt.Errorf("нажатие подняло работу вместо закрытия: kind=%q session=%q", v.Kind, v.Session)
	}
	calls, err := os.ReadFile(s.callsFile())
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(calls), "close "+smokeAccepted) {
		return "", fmt.Errorf("taskctl close не позван, вызовы утилиты: %s", strings.TrimSpace(string(calls)))
	}
	sessions, _ := os.ReadFile(filepath.Join(s.dir, "tmux.sessions"))
	if strings.Contains(string(sessions), "task-"+smokeAccepted) {
		return "", fmt.Errorf("закрытие подняло tmux-сессию task-%s: виток тут лишний", smokeAccepted)
	}
	doc, err := os.ReadFile(s.boardDoc())
	if err != nil {
		return "", err
	}
	if strings.Contains(string(doc), smokeAccepted) {
		return "", fmt.Errorf("строка %s осталась на синтетической доске:\n%s", smokeAccepted, doc)
	}
	board, err := s.board()
	if err != nil {
		return "", err
	}
	for _, sec := range board.Board.Sections {
		for _, row := range sec.Rows {
			if row.ID == smokeAccepted {
				return "", fmt.Errorf("закрытая задача всё ещё на доске, секция %s", sec.Key)
			}
		}
	}
	// Второе нажатие с устаревшего экрана: строки нет, а причина этому не
	// «нет строки», а закрытие, которое уже прошло (DK-289).
	var refusal struct {
		Error string `json:"error"`
	}
	if err := s.call("POST", "/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, smokeAccepted),
		http.StatusNotFound, &refusal); err != nil {
		return "", err
	}
	if !strings.Contains(refusal.Error, "уже закрыта") {
		return "", fmt.Errorf("устаревшему экрану отказали выдуманной причиной: %s", refusal.Error)
	}
	git := "коммит доски прошёл"
	if v.Note != "" {
		// Синтетический проект не репозиторий: важно, что провал коммита назван
		// словами, а не проглочен.
		git = "закрытие на месте, git назван словами"
	}
	return fmt.Sprintf("%s, сессии не поднималось, %s; второе нажатие: %s", v.Message, git, refusal.Error), nil
}

// stepHarnessRun: выбор подписки доезжает до запускаемой команды (DK-326).
// Шаг идёт до самого клиента, а не до строки запроса: список приезжает ручкой
// /api/harnesses, запрос называет вторую подписку, а поднимается в итоге её
// клиент с её именем в окружении. Проверять по ответу сервера тут было бы
// нечестно, потому что сломаться может ровно то, что между: команда сессии,
// обёртка agentctl exec и имя, которое до клиента не доехало.
func (s *smoke) stepHarnessRun() (string, error) {
	var list HarnessView
	if err := s.call("GET", "/api/harnesses", "", http.StatusOK, &list); err != nil {
		return "", err
	}
	if len(list.Harnesses) != 2 {
		return "", fmt.Errorf("подписок в ответе %d, ждал две: %+v", len(list.Harnesses), list)
	}
	if list.Harnesses[0].Name != smokeHarnessOne || !list.Harnesses[0].Default {
		return "", fmt.Errorf("подписка по умолчанию не названа: %+v", list.Harnesses[0])
	}
	if list.Harnesses[1].Name != smokeHarnessTwo || list.Harnesses[1].Bin != smokeClientTwo {
		return "", fmt.Errorf("вторая подписка пришла без своего клиента: %+v", list.Harnesses[1])
	}
	// Имя, которого на машине нет, отбивается словами и до всякой сессии:
	// устаревший экран не должен молча уезжать на подписку по умолчанию.
	var refusal struct {
		Error string `json:"error"`
	}
	if err := s.call("POST", "/api/projects/demo/runs",
		fmt.Sprintf(`{"id": %q, "harness": "подписка-которой-нет"}`, smokeTask),
		http.StatusBadRequest, &refusal); err != nil {
		return "", err
	}
	if !strings.Contains(refusal.Error, "на машине нет") {
		return "", fmt.Errorf("незнакомая подписка отбита выдуманной причиной: %s", refusal.Error)
	}
	var v struct {
		Session string `json:"session"`
		Harness string `json:"harness"`
		Message string `json:"message"`
	}
	if err := s.call("POST", "/api/projects/demo/runs",
		fmt.Sprintf(`{"id": %q, "harness": %q}`, smokeTask, smokeHarnessTwo), http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.Harness != smokeHarnessTwo || !strings.Contains(v.Message, smokeHarnessTwo) {
		return "", fmt.Errorf("ответ запуска не называет подписку: %+v", v)
	}
	runs, err := os.ReadFile(s.runsFile())
	if err != nil {
		return "", fmt.Errorf("журнала поднятых сессий нет: %v", err)
	}
	text := string(runs)
	if !strings.Contains(text, "agentctl' exec --harness") {
		return "", fmt.Errorf("команда сессии не завёрнута в agentctl exec:\n%s", text)
	}
	want := fmt.Sprintf("%s: подписка %s, заказ Выполни %s", smokeClientTwo, smokeHarnessTwo, smokeTask)
	if !strings.Contains(text, want) {
		return "", fmt.Errorf("до клиента подписка не доехала, ждал %q:\n%s", want, text)
	}
	if strings.Contains(text, smokeClientOne+":") {
		return "", fmt.Errorf("сессию поднял клиент чужой подписки:\n%s", text)
	}
	// Поднятая работа обязана быть видна: клиент второй подписки пишет журнал в
	// своё хозяйство, и пока список сессий ходил в один ~/.claude, разговор
	// headless-запуска с доски не показывался нигде (DK-362).
	var sess struct {
		Sessions []sessionInfo `json:"sessions"`
		Note     string        `json:"note"`
	}
	if err := s.call("GET", "/api/projects/demo/sessions?task="+smokeTask, "", http.StatusOK, &sess); err != nil {
		return "", err
	}
	if len(sess.Sessions) != 1 || sess.Sessions[0].ID != smokeHeadlessID {
		return "", fmt.Errorf("чата headless-запуска нет в списке сессий задачи %s: %+v, приписка: %s",
			smokeTask, sess.Sessions, sess.Note)
	}
	var talk struct {
		Items []reply `json:"items"`
	}
	if err := s.call("GET", "/api/projects/demo/sessions/"+smokeHeadlessID, "", http.StatusOK, &talk); err != nil {
		return "", err
	}
	if len(talk.Items) == 0 || !strings.Contains(talk.Items[0].Text, smokeTask) {
		return "", fmt.Errorf("экран агента открылся без заказа работы: %+v", talk.Items)
	}
	return fmt.Sprintf("%s, сессию поднял %s с именем подписки в окружении, чат виден в списке задачи (%s); "+
		"незнакомая подписка отбита: %s",
		v.Message, smokeClientTwo, sess.Sessions[0].ID, refusal.Error), nil
}

// stepStopHook: замечание стоп-хука (DK-693) харнес кладёт в транскрипт
// репликой роли user с префиксом и пометкой isMeta, а лента обязана отдать её
// служебкой с подписью «стоп-хук», а не безымянной серой строкой. Запись
// пишется того же вида, что пишет харнес: двоеточие, перевод строки, много
// строк текста, без них шаг проверял бы выдуманную форму. Шаг идёт после
// запуска работы: транскрипт headless-сессии к этому времени уже написан
// клиентом, и реплика дописывается в него хвостом, как дописал бы её
// настоящий стоп-хук.
func (s *smoke) stepStopHook() (string, error) {
	const said = "Остановись и прогони тесты"
	line := fmt.Sprintf(`{"type":"user","isMeta":true,"message":{"role":"user","content":%q},"timestamp":"2026-08-10T10:00:03.000Z"}`,
		stopHookPrefix+"\n"+said)
	path := filepath.Join(s.harnessJournal(), smokeHeadlessID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("транскрипта headless-сессии нет: %v", err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	var talk struct {
		Items []reply `json:"items"`
	}
	if err := s.call("GET", "/api/projects/demo/sessions/"+smokeHeadlessID, "", http.StatusOK, &talk); err != nil {
		return "", err
	}
	for _, it := range talk.Items {
		if it.Note == stopHookWord {
			if it.Role != roleNote || it.Text != said {
				return "", fmt.Errorf("стоп-хук разобрался, но показан не тем блоком: %+v", it)
			}
			return "реплика с префиксом «" + stopHookPrefix +
				"» стоит в ленте служебкой с подписью «" + stopHookWord + "»", nil
		}
	}
	return "", fmt.Errorf("записи стоп-хука в ленте нет: %+v", talk.Items)
}

// stepSystemNote: автоматическое уведомление о фоновой работе (чат DK-656)
// харнес кладёт репликой роли user с английской преамбулой и тегом, а лента
// обязана отдать его одним блоком «Фоновый агент», без безымянной строки с
// дисклеймером. Запись пишется того же вида, что пишет харнес: маркер,
// преамбула, пустая строка, тег со сводкой и статусом.
func (s *smoke) stepSystemNote() (string, error) {
	const sum = "Background command \"go test\" was stopped"
	note := sysNoteMark + "\n" +
		"This is an automated background-task event, NOT a message from the user.\n" +
		"Do NOT interpret this as user acknowledgement, confirmation, or response to any pending question.\n" +
		"\n" +
		"<task-notification>\n<task-id>smoke1</task-id>\n<status>killed</status>\n" +
		"<summary>" + sum + "</summary>\n</task-notification>"
	line := fmt.Sprintf(`{"type":"user","isMeta":true,"message":{"role":"user","content":%q},"timestamp":"2026-08-10T10:00:04.000Z"}`, note)
	path := filepath.Join(s.harnessJournal(), smokeHeadlessID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("транскрипта headless-сессии нет: %v", err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	var talk struct {
		Items []reply `json:"items"`
	}
	if err := s.call("GET", "/api/projects/demo/sessions/"+smokeHeadlessID, "", http.StatusOK, &talk); err != nil {
		return "", err
	}
	for _, it := range talk.Items {
		if it.Role == roleNote && it.Note == "" {
			return "", fmt.Errorf("дисклеймер уведомления остался безымянной строкой: %+v", it)
		}
	}
	for _, it := range talk.Items {
		if it.Note == "Фоновый агент: killed" {
			if it.Role != roleNote || it.Mark != "agent" || it.Text != sum {
				return "", fmt.Errorf("уведомление разобралось, но показано не тем блоком: %+v", it)
			}
			return "уведомление с преамбулой стоит в ленте одним блоком «Фоновый агент: killed»", nil
		}
	}
	return "", fmt.Errorf("блока «Фоновый агент: killed» в ленте нет: %+v", talk.Items)
}

// stepChatStart: новый чат по задаче поднимается поверх живого конвейера той же
// задачи и получает своё имя chat-<ID>-<n> (DK-436). Шаг идёт после запуска
// задачи нарочно: tmux-сессия task-<ID> к этому времени жива, и по ответу
// видно, что отказ «работа уже идёт» разговор не задевает, а второй конвейер
// им по-прежнему отбивается. Занятость задачи от чата не меняется: строка
// доски остаётся с прежним признаком работы.
// Молчаливая смерть подъёма (DK-728). Сессия создаётся и тут же уходит: клиент
// вышел, не назвавшись в реестре, как выходит клиент без входа или с
// кончившейся квотой. Ожидание панели идёт поиском разговора по имени
// tmux-сессии, и тем же ответом обязан приехать исход подъёма со словами и
// хвостом терминала, иначе панель обещает разговор, которого не будет.
func (s *smoke) stepRaiseDeath() (string, error) {
	var born struct {
		Way  string `json:"way"`
		Tmux string `json:"tmux"`
	}
	ask := `{"text": "прогон smoke: подъём, который умрёт"}`
	if err := s.call("POST", "/api/projects/demo/chats", ask, http.StatusOK, &born); err != nil {
		return "", err
	}
	if born.Way != "new" || born.Tmux == "" {
		return "", fmt.Errorf("подъём не назвал ни дороги, ни сессии: %+v", born)
	}
	look := "/api/projects/demo/chats?tmux=" + born.Tmux
	var alive struct {
		Dead struct {
			Why string `json:"why"`
		} `json:"dead"`
	}
	if err := s.call("GET", look, "", http.StatusOK, &alive); err != nil {
		return "", err
	}
	if alive.Dead.Why != "" {
		return "", fmt.Errorf("живую сессию объявили мёртвой: %s", alive.Dead.Why)
	}
	// Клиент вышел: имя сессии уходит из списка живых, как ушло бы у tmux.
	if err := s.tmuxDrop(born.Tmux); err != nil {
		return "", err
	}
	var dead struct {
		Dead struct {
			Why  string `json:"why"`
			Tail string `json:"tail"`
		} `json:"dead"`
	}
	if err := s.call("GET", look, "", http.StatusOK, &dead); err != nil {
		return "", err
	}
	if dead.Dead.Why == "" {
		return "", fmt.Errorf("смерть сессии %s не названа: панель ждёт разговор, которого не будет", born.Tmux)
	}
	if !strings.Contains(dead.Dead.Tail, "Invalid API key") {
		return "", fmt.Errorf("в исходе нет хвоста терминала, причину брать негде: %q", dead.Dead.Tail)
	}
	return dead.Dead.Why, nil
}

// tmuxDrop убирает имя из списка живых сессий фикстуры: так выглядит клиент,
// вышедший сам, без kill-session от дашборда.
func (s *smoke) tmuxDrop(name string) error {
	data, err := os.ReadFile(s.tmuxListFile())
	if err != nil {
		return fmt.Errorf("список сессий фикстуры не прочитался: %v", err)
	}
	var keep []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != name && strings.TrimSpace(line) != "" {
			keep = append(keep, line)
		}
	}
	text := ""
	if len(keep) > 0 {
		text = strings.Join(keep, "\n") + "\n"
	}
	return os.WriteFile(s.tmuxListFile(), []byte(text), 0o644)
}

func (s *smoke) stepChatStart() (string, error) {
	var first, second struct {
		Tmux    string `json:"tmux"`
		Message string `json:"message"`
	}
	// Реплика человека и есть начало чата: пустой заказ ручка отбивает, потому
	// что заголовок разговора берётся из первых слов (POC ветки poc-chat).
	ask := fmt.Sprintf(`{"id": %q, "text": "прогон smoke: разговор о задаче"}`, smokeTask)
	if err := s.call("POST", "/api/projects/demo/chats", ask, http.StatusOK, &first); err != nil {
		return "", err
	}
	if first.Tmux != "chat-"+smokeTask+"-1" {
		return "", fmt.Errorf("первый чат задачи поднят сессией %q, ждал chat-%s-1", first.Tmux, smokeTask)
	}
	if err := s.call("POST", "/api/projects/demo/chats", ask, http.StatusOK, &second); err != nil {
		return "", err
	}
	if second.Tmux != "chat-"+smokeTask+"-2" {
		return "", fmt.Errorf("второй чат задачи поднят сессией %q, ждал chat-%s-2: "+
			"чаты отбиваются, как конвейеры", second.Tmux, smokeTask)
	}
	// Пустая реплика чата не поднимает, и отказ на неё виден словами: заведись
	// разговор без слов человека, в списке он остался бы безымянным.
	var empty struct {
		Error string `json:"error"`
	}
	if err := s.call("POST", "/api/projects/demo/chats", fmt.Sprintf(`{"id": %q}`, smokeTask),
		http.StatusBadRequest, &empty); err != nil {
		return "", err
	}
	if !strings.Contains(empty.Error, "чат начинается со слов человека") {
		return "", fmt.Errorf("пустая реплика отбита выдуманной причиной: %s", empty.Error)
	}
	runs, err := os.ReadFile(s.runsFile())
	if err != nil {
		return "", fmt.Errorf("журнала поднятых сессий нет: %v", err)
	}
	want := "DEVKIT_TMUX='" + second.Tmux + "'"
	if !strings.Contains(string(runs), want) {
		return "", fmt.Errorf("чат не назвал себя реестру, ждал %q:\n%s", want, runs)
	}
	// Конвейер поверх живого конвейера по-прежнему отбивается: снимали тут не
	// его, и потеря этого отказа стоила бы двух исполнителей в одном дереве.
	var refusal struct {
		Error string `json:"error"`
	}
	if err := s.call("POST", "/api/projects/demo/runs", ask, http.StatusConflict, &refusal); err != nil {
		return "", err
	}
	v, err := s.board()
	if err != nil {
		return "", err
	}
	// Работа у задачи по-прежнему одна, конвейерная: два живых разговора о ней
	// в занятость не считаются, потому что чат это не конвейер.
	talks := 0
	for _, w := range v.Works {
		if w.ID == smokeTask {
			talks++
			if w.Via != "tmux" || w.Kind != "task" {
				return "", fmt.Errorf("работа задачи %s подменена чатом: %+v", smokeTask, w)
			}
		}
	}
	if talks != 1 {
		return "", fmt.Errorf("живых работ у задачи %s стало %d: чаты сосчитаны занятостью", smokeTask, talks)
	}
	return fmt.Sprintf("%s; второй чат встал рядом (%s), а второй конвейер отбит: %s",
		first.Message, second.Tmux, refusal.Error), nil
}

// smokeHeldChat это кончившийся разговор, чьё имя окна увёл печатный подъём,
// smokeHeldAlien живой сосед, который в этом окне идёт на самом деле, а
// smokeHeldName само имя. Номер имени взят с запасом: резюм берёт первый
// свободный, и занятая девятка ему не мешает.
const (
	smokeHeldChat  = "smoke-held-1"
	smokeHeldAlien = "smoke-alien-1"
	smokeHeldName  = "chat-" + smokeTask + "-9"
)

// stepHeldWindow: разговор с чужим окном (DK-673). Реестр чатов отдаёт имя
// окна кончившемуся разговору, а идёт в этом окне живой сосед. Так выглядит
// машина после печатного подъёма чужой сессии из окна панели. Прогон
// спрашивает три вещи. Реплика уезжает резюмом в своё окно, а не клавишами в
// чужое. Уборка в архив чужую сессию не снимает и называет, чей это разговор.
// Список чатов показывает разговор снятым и называет занявшего имя.
func (s *smoke) stepHeldWindow() (string, error) {
	said := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"разбор кончился"},`+
		`"timestamp":%q,"gitBranch":"main"}`+"\n",
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	tr := filepath.Join(s.harnessJournal(), smokeHeldChat+".jsonl")
	if err := smokeWrite(tr, said, 0o644); err != nil {
		return "", err
	}
	line := sessions.Line(time.Now(), smokeHeldChat, sessions.Bind{
		Task: smokeTask, Project: "demo", Tree: s.proj, Transcript: tr,
		Source: "заказ", Tmux: smokeHeldName}, "startup")
	if err := sessions.Append(s.bindsFile(), line); err != nil {
		return "", err
	}
	// Живой клиент называет своё окно сам, и запись эта живёт, пока жив
	// процесс. Pid тут свой, прогонный: чужой живости взять негде.
	peer := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"сосед","kind":"interactive",`+
		`"entrypoint":"cli","tmux":"%s:@9.%%9"}`, os.Getpid(), smokeHeldAlien, smokeHeldName)
	if err := smokeWrite(filepath.Join(s.home, ".claude", "sessions",
		fmt.Sprintf("%d.json", os.Getpid())), peer, 0o644); err != nil {
		return "", err
	}
	list := filepath.Join(s.dir, "tmux.sessions")
	live, err := os.ReadFile(list)
	if err != nil {
		return "", err
	}
	if err := smokeWrite(list, string(live)+smokeHeldName+"\n", 0o644); err != nil {
		return "", err
	}

	var seen struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := s.call("GET", "/api/projects/demo/chats?all=1", "", http.StatusOK, &seen); err != nil {
		return "", err
	}
	var held *chatEntry
	for i := range seen.Chats {
		if seen.Chats[i].ID == smokeHeldChat {
			held = &seen.Chats[i]
		}
	}
	if held == nil {
		return "", fmt.Errorf("разговор %s пропал из списка чатов", smokeHeldChat)
	}
	if held.GoneTo != smokeHeldAlien {
		return "", fmt.Errorf("список не назвал занявшего имя: gone %q, goneTo %q, tmux %q",
			held.Gone, held.GoneTo, held.Tmux)
	}

	var arch struct {
		Dropped bool   `json:"dropped"`
		Message string `json:"message"`
	}
	if err := s.call("POST", "/api/projects/demo/chats/"+smokeHeldChat+"/archive",
		`{"archived": true}`, http.StatusOK, &arch); err != nil {
		return "", err
	}
	if arch.Dropped {
		return "", fmt.Errorf("уборка сняла чужую живую сессию: %s", arch.Message)
	}
	after, err := os.ReadFile(list)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(after), smokeHeldName) {
		return "", fmt.Errorf("окно %s снято уборкой чужого разговора:\n%s", smokeHeldName, after)
	}
	var say struct {
		Way     string `json:"way"`
		Tmux    string `json:"tmux"`
		Message string `json:"message"`
	}
	if err := s.call("POST", "/api/projects/demo/chats/"+smokeHeldChat+"/say",
		`{"text": "Довёл?"}`, http.StatusOK, &say); err != nil {
		return "", err
	}
	if say.Way != "resume" {
		return "", fmt.Errorf("реплика поехала дорогой %q, а окно %s занято чужим разговором",
			say.Way, smokeHeldName)
	}
	if say.Tmux == smokeHeldName {
		return "", fmt.Errorf("резюм поднят в чужом окне %s", say.Tmux)
	}

	return fmt.Sprintf("реплика уехала резюмом в %s, список назвал занявшего имя (%s), уборка сказала: %s",
		say.Tmux, held.GoneTo, arch.Message), nil
}

// stepDropDraft: черновик снимается с экрана, а не из терминала. Причина
// обязательна и здесь, и у утилиты: файла после команды нет, и живёт причина
// сообщением коммита доски, поэтому шаг сначала жмёт удаление без причины и
// ждёт отказа, а потом сверяет вызов утилиты по журналу фикстуры и пропажу
// файла синтетического накопителя.
func (s *smoke) stepDropDraft() (string, error) {
	path := "/api/projects/demo/drafts/" + smokeDraft
	var refusal struct {
		Error string `json:"error"`
	}
	if err := s.call("DELETE", path, `{"reason": ""}`, http.StatusBadRequest, &refusal); err != nil {
		return "", err
	}
	if !strings.Contains(refusal.Error, "причину") {
		return "", fmt.Errorf("удаление без причины отбито выдуманной причиной: %s", refusal.Error)
	}
	if _, err := os.Stat(s.draftFile()); err != nil {
		return "", fmt.Errorf("отбитый запрос всё равно тронул файл черновика: %v", err)
	}
	reason := "след промаха мимо подкоманды, разбирать нечего"
	var v struct {
		Message string `json:"message"`
		Note    string `json:"note"`
	}
	if err := s.call("DELETE", path, fmt.Sprintf(`{"reason": %q}`, reason), http.StatusOK, &v); err != nil {
		return "", err
	}
	if _, err := os.Stat(s.draftFile()); !os.IsNotExist(err) {
		return "", fmt.Errorf("файл черновика остался на месте: %v", err)
	}
	calls, err := os.ReadFile(s.callsFile())
	if err != nil {
		return "", err
	}
	if want := "draft drop " + smokeDraft + " --reason " + reason; !strings.Contains(string(calls), want) {
		return "", fmt.Errorf("taskctl draft drop позван не так, вызовы утилиты: %s", strings.TrimSpace(string(calls)))
	}
	// Исход разбора дашборд больше не пересказывает своей ручкой: разговор с
	// агентом идёт в чате, а на доске исход виден по факту (решение
	// пользователя). Удаление тут и проверяется по факту: файла записи нет, а
	// сама запись ушла из накопителя.
	var left struct {
		Drafts []struct {
			ID string `json:"id"`
		} `json:"drafts"`
	}
	if err := s.call("GET", "/api/projects/demo/drafts", "", http.StatusOK, &left); err != nil {
		return "", err
	}
	for _, d := range left.Drafts {
		if d.ID == smokeDraft {
			return "", fmt.Errorf("удалённая запись осталась в накопителе: %s", d.ID)
		}
	}
	git := "коммит доски прошёл"
	if v.Note != "" {
		// Синтетический проект не репозиторий: важно, что провал коммита назван
		// словами, а не проглочен.
		git = "удаление на месте, git назван словами"
	}
	return fmt.Sprintf("%s; отказ без причины: %s; %s; записи в накопителе больше нет",
		v.Message, refusal.Error, git), nil
}

// stepDragRank: перетаскивание строки очереди, девятый сценарий цели DK-327.
// Пальца у прогона нет, и проверяет он не жест, а его исход: пересчитанная
// ценность уезжает той же ручкой, что и форма задачи, новый ранг доезжает до
// синтетической доски и читается с неё обратно, а ответ называет фактическое
// место строки. Тут же откат: с разошедшейся ожидаемой разбивкой ручка отвечает
// словами и доску не трогает.
func (s *smoke) stepDragRank() (string, error) {
	path := "/api/projects/demo/tasks/" + smokeTask
	var v struct {
		Message string `json:"message"`
		Place   struct {
			Sect   string `json:"sect"`
			R      int    `json:"r"`
			P      string `json:"p"`
			RParts []int  `json:"r_parts"`
		} `json:"place"`
	}
	if err := s.call("PATCH", path, `{"r_parts": [null, 6, null, null, null]}`, http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.Place.Sect != "backlog" || v.Place.R != 34 || v.Place.P != "P2" {
		return "", fmt.Errorf("ответ на правку ценности назвал место %+v, ждал ранг 34 в backlog", v.Place)
	}
	calls, err := os.ReadFile(s.callsFile())
	if err != nil {
		return "", err
	}
	if want := "set " + smokeTask + " --rank 25+6+1+0+2"; !strings.Contains(string(calls), want) {
		return "", fmt.Errorf("taskctl set позван не так, вызовы утилиты: %s", strings.TrimSpace(string(calls)))
	}
	board, err := s.board()
	if err != nil {
		return "", err
	}
	got := 0
	for _, sec := range board.Board.Sections {
		for _, row := range sec.Rows {
			if row.ID == smokeTask {
				got = row.R
			}
		}
	}
	if got != 34 {
		return "", fmt.Errorf("на доске у %s ранг %d, ждал доехавшие 34", smokeTask, got)
	}
	var refusal struct {
		Error string `json:"error"`
	}
	if err := s.call("PATCH", path,
		`{"r_parts": [null, 2, null, null, null], "expect_r_parts": [25, 2, 1, 0, 2]}`,
		http.StatusConflict, &refusal); err != nil {
		return "", err
	}
	if !strings.Contains(refusal.Error, "откат не применён") {
		return "", fmt.Errorf("откат поверх чужой правки отбит не теми словами: %s", refusal.Error)
	}
	return fmt.Sprintf("%s; на доске ранг %d, место в %s; откат поверх чужой правки: %s",
		v.Message, got, v.Place.Sect, refusal.Error), nil
}

// realHomeDir называет дом машины мимо переменной окружения HOME:
// os.UserCacheDir читает её из процесса, а прогон тестов слияния подставляет
// вместо неё временный каталог (DK-641/643), сам лежащий под TMPDIR. user.Current
// идёт в системную базу учётных записей и называет настоящий дом, даже когда
// HOME процесса подменена (DK-677).
func realHomeDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

// smokeBase это корень рабочих каталогов прогона: кеш пользователя, а не
// системный temp. hooks/notify.py метит корень под /tmp, /var/folders и
// TMPDIR песочницей и гасит баннер ещё до выбора бэкенда (sandbox_reason,
// DK-196), а прогон нарочно подставляет DEVKIT_NOTIFY_BACKEND=notify-fake,
// чтобы шаг «уведомление о стопе в ленте» проверял настоящую доставку через
// бэкенд, а не пропуск по песочнице: под системным temp сам пропуск гасил бы
// строку в ленте и там, где лента её как раз обязана показать (DK-283).
//
// Кеш ищется от настоящего дома (realHomeDir), не от HOME процесса: в
// прогоне тестов слияния HOME подменена временным каталогом под TMPDIR, и
// os.UserCacheDir от неё вернул бы кеш там же, под той же песочницей, которую
// этот корень и должен обходить (DK-677). HOME процесса подменяется только на
// время самого вызова и сразу восстанавливается.
func smokeBase() (string, error) {
	home, err := realHomeDir()
	if err != nil {
		return "", err
	}
	old, had := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		return "", err
	}
	cache, cacheErr := os.UserCacheDir()
	if had {
		os.Setenv("HOME", old)
	} else {
		os.Unsetenv("HOME")
	}
	if cacheErr != nil {
		return "", cacheErr
	}
	dir := filepath.Join(cache, "devkit-dashboard-smoke")
	return dir, os.MkdirAll(dir, 0o755)
}

// cmdSmoke проходит цепочку DoD и печатает ход по шагам: провалившийся шаг
// называет себя и причину, а не оставляет один ненулевой код.
func cmdSmoke(out io.Writer, keep bool) error {
	base, err := smokeBase()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(base, "run-")
	if err != nil {
		return err
	}
	if keep {
		fmt.Fprintf(out, "окружение прогона остаётся в %s\n", dir)
	} else {
		defer os.RemoveAll(dir)
	}
	s, err := newSmoke(dir)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.login(); err != nil {
		return fmt.Errorf("вход: %v", err)
	}
	steps := []smokeStep{
		{"доска со статусами и работами", s.stepBoard},
		{"поиск по доске, архиву и тексту задач", s.stepSearch},
		{"журнал цели из её файла", s.stepJournal},
		{"состав цели сабтасками", s.stepComposition},
		{"запуск работы", s.stepStart},
		{"работа видна живой", s.stepWorks},
		{"вид деятельности в строке доски", s.stepStage},
		{"сообщение цели", s.stepMessage},
		{"ответ задаче безадресной строкой", s.stepTaskMessage},
		{"ожидание человека видно строкой доски", s.stepWaiting},
		{"доставка реплики витку", s.stepDelivered},
		{"подхват сообщения витком", s.stepTurn},
		{"живая лента открыта", s.stepFeedOpen},
		{"стоп работы", s.stepStop},
		{"уведомление о стопе в ленте", s.stepFeedStop},
		{"сообщение при стоящем цикле", s.stepIdleMessage},
		{"закрытие принятой задачи без витка", s.stepCloseAccepted},
		{"выбор подписки доезжает до команды", s.stepHarnessRun},
		{"реплика стоп-хука стоит служебкой с подписью", s.stepStopHook},
		{"уведомление о фоновой работе без портянки дисклеймера", s.stepSystemNote},
		{"новый чат по задаче поверх живого конвейера", s.stepChatStart},
		{"реплика и уборка при занятом окне", s.stepHeldWindow},
		{"удаление черновика с причиной", s.stepDropDraft},
		{"пересчёт ранга перетаскиванием доехал до доски", s.stepDragRank},
		{"смерть поднятой сессии названа исходом", s.stepRaiseDeath},
	}
	for i, st := range steps {
		note, err := st.run()
		if err != nil {
			return fmt.Errorf("шаг %d (%s): %v", i+1, st.name, err)
		}
		fmt.Fprintf(out, "шаг %d, %s: %s\n", i+1, st.name, note)
	}
	fmt.Fprintln(out, smokeOK)
	return nil
}
