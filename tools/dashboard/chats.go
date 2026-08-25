package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Разговор с агентом (POC ветки poc-chat, переделка после отбитой приёмки
// DK-397). Диалог это сессия харнеса, один к одному с транскриптом: список
// диалогов собирается из каталогов транскриптов проекта, а реестр
// ~/.devkit/sessions.log говорит, каких задач сессия касалась, какой моделью
// она поднята и в какой tmux-сессии идёт.
//
// Прежнее устройство моделировало разговор файлами и отложенной доставкой:
// реплика ложилась строкой во вход и ждала хода инструмента. Отсюда четыре
// «чата» на одну цель и реплика, ушедшая чужому окну. Теперь доставка идёт в
// сам процесс: живой сессии реплика подаётся через tmux send-keys, кончившейся
// поднимается продолжение `claude --resume`, а новый диалог это новая
// tmux-сессия с репликой первым аргументом.

// chatModelDefault это модель нового диалога, пока человек не выбрал другую.
const chatModelDefault = "opus"

// chatModel это ступень выбора модели в панели: имя модели, ярус и подписка,
// чьей квотой она платится. Список собирается из раскладки подписок
// (agentctl harness --json), а не пишется в коде: имён поставщиков в дашборде
// нет ни одного, и новая подписка появляется в выборе сама.
type chatModelOpt struct {
	Model   string `json:"model"`
	Tier    string `json:"tier"`
	Harness string `json:"harness"`
	Default bool   `json:"default,omitempty"`
}

// chatModelOpts разворачивает лестницы всех включённых подписок в плоский
// список выбора. Повторы модели в одной подписке отсеиваются: у второй
// подписки верхние ярусы сложены одной моделью, и три одинаковых строки в
// выпадающем списке читались бы как ошибка.
func (s *server) chatModelOpts() []chatModelOpt {
	out := []chatModelOpt{}
	for _, h := range s.harnesses().Harnesses {
		seen := map[string]bool{}
		for _, m := range h.Models {
			if seen[m.Model] {
				continue
			}
			seen[m.Model] = true
			out = append(out, chatModelOpt{Model: m.Model, Tier: m.Tier,
				Harness: h.Name, Default: h.Default && m.Tier == "pro"})
		}
	}
	return out
}

// chatHarnessOf называет подписку, чьей моделью просят поднять разговор: у
// второй подписки клиент поднимается своим каталогом конфигурации, и без этого
// сессия ушла бы на чужую квоту.
func (s *server) chatHarnessOf(model string) *Harness {
	view := s.harnesses()
	for i := range view.Harnesses {
		for _, m := range view.Harnesses[i].Models {
			if m.Model == model {
				return &view.Harnesses[i]
			}
		}
	}
	return nil
}

// Состояния диалога. live это живой процесс в tmux, которым правит дашборд;
// vscode это свежий транскрипт без своей tmux-сессии, то есть окно человека, и
// писать туда с дашборда нечем; dead это кончившийся процесс, его продолжает
// резюм.
const (
	chatLive   = "live"
	chatVscode = "vscode"
	chatDead   = "dead"
)

// chatEntry это строка списка диалогов. Заголовок берётся из первой реплики
// человека, обрезанной, как это делает расширение Claude Code для vscode:
// имени диалог не требует, а первая реплика узнаётся глазом.
type chatEntry struct {
	ID string `json:"id"`
	// Project называет проект разговора в общем списке машины (?all=1): без
	// него строку чужого проекта не подписать и не открыть, адрес ленты и
	// реплики ходит через имя проекта. В проектном списке поле пустое, там
	// хозяин и так известен.
	Project string `json:"project,omitempty"`
	Title   string `json:"title,omitempty"`
	// Mtime это время последней содержательной реплики разговора: по нему
	// список стоит свежими сверху и им же подписана строка. Время правки файла
	// сюда больше не едет вовсе, оно двигалось служебщиной (замечание
	// пользователя про чаты, всплывшие наверх без единой реплики).
	Mtime   string   `json:"mtime,omitempty"`
	Tasks   []string `json:"tasks,omitempty"`
	Model   string   `json:"model,omitempty"`
	Tmux    string   `json:"tmux,omitempty"`
	State   string   `json:"state"`
	Tree    string   `json:"tree,omitempty"`
	Branch  string   `json:"branch,omitempty"`
	Harness string   `json:"harness,omitempty"`
	// Живая сессия из реестра клиента: сокет канала, pid, слово про то, где она
	// идёт, и её состояние (idle значит «ждёт ввода»). Пусто значит, что
	// процесса у диалога нет вовсе.
	Sock  string `json:"sock,omitempty"`
	PID   int    `json:"pid,omitempty"`
	Where string `json:"where,omitempty"`
	Idle  bool   `json:"idle,omitempty"`
	// Summary это заголовок от самого харнеса: он старше и эвристики, и haiku.
	Summary string `json:"-"`
	// First это первая реплика человека, обрезанная для списка. Title поверх
	// неё замещается заголовком (titleFill), а панель, вернувшаяся на адрес
	// new, узнаёт родившийся диалог именно по первой реплике: она уехала
	// клиенту первым аргументом и легла в транскрипт первой (пришивание,
	// chatSewn в app.js).
	First string `json:"first,omitempty"`
	// Live это модель, которой сессия работает на самом деле (из транскрипта).
	// Model рядом это сохранённый выбор дашборда, то есть чем её поднимать в
	// следующий раз. Расходятся они у чужого окна: там модель выбрана в самом
	// клиенте, и выбор дашборда до резюма не действует.
	LiveModel string `json:"liveModel,omitempty"`
	// Own говорит, дашбордова ли это сессия: только у своей смена модели
	// действует сразу следующим подъёмом, чужую до резюма не переубедить.
	Own bool `json:"own,omitempty"`
	// Note подписывает узнавание задачи словами: «задача не с доски проекта»,
	// «свободный чат», «говорит о XR-1». Считалось это и раньше
	// (bindTask), но наружу шло только списком сессий, а панель разговора
	// подписи не показывала вовсе, и разговор о чужой доске выглядел обычным
	// разговором проекта. Bound рядом это разряд привязки: работой задачи
	// считается только boundLead.
	Note  string `json:"note,omitempty"`
	Bound string `json:"bound,omitempty"`
	// Stuck называет клин словами: разговор выглядит живым (сокет отвечает,
	// доставки «успешны»), а хода в нём нет и не будет. Пусто у здорового
	// разговора, и экран тогда рисует его как прежде.
	Stuck string `json:"stuck,omitempty"`
	// Gone называет словами снятый и пересозданный разговор: имя его
	// tmux-сессии реестр отдал другому разговору, то есть работу подняли
	// заново, а этот разговор кончился. Писать в него нечего, и панель обязана
	// сказать это словами: молчание тут неотличимо от доставки, и ровно им
	// кончился живой случай, где реплика уехала посторонней сессии.
	// GoneTo это тот разговор, что занял имя: он и есть понятный выход.
	Gone   string `json:"gone,omitempty"`
	GoneTo string `json:"goneTo,omitempty"`
}

// goneRestartWord это слова снятого разговора. Одни и те же в панели и в
// пузыре недоставленной реплики: человек читает их в обоих местах.
const goneRestartWord = "сессия разговора снята: работу подняли заново, ответить в ней нечем"

// stuckLostTerm это срок молчания транскрипта, после которого клин считается
// клином, а не паузой между ходами: ход агента длится секунды, и две минуты
// тишины при пропавшем терминале не пауза.
const stuckLostTerm = 2 * time.Minute

// stuckLostTermWord это слова клина. Одни и те же на списке чатов и в ответе
// на реплику: человек читает их в плашке, а решает по ним одинаково.
const stuckLostTermWord = "терминал пропал"

// lostTerminal узнаёт клин: процесс клиента жив, а tmux-сессии, в которой он
// был поднят, больше нет. Такой клиент пишет в исчезнувший терминал и стоит
// намертво: реплики уходят в его сокет «успешно» и копятся в замороженной
// очереди, транскрипт молчит, а экран показывает вечное «работает» (инцидент с
// чатом DK-460). Само по себе исчезновение tmux-сессии клином не считается:
// у окна vscode её нет вовсе, а у только что снятой сессии процесс успевает
// умереть сам.
func lostTerminal(pid int, tmux string, alive func(string) bool, mod, now time.Time) bool {
	if pid <= 0 || tmux == "" || alive(tmux) {
		return false
	}
	if !pidAlive(pid) {
		return false
	}
	return now.Sub(mod) > stuckLostTerm
}

// stuckAskWord это слова третьего рода стоящего чата: клиент спросил
// разрешение или ответ в своём терминале и ждёт человека там. Это не клин,
// снимать процесс тут нельзя, человеку нужен attach в tmux-сессию: свежий
// профиль второй подписки встаёт так на первом же запуске (вопрос доверия
// каталогу, живой случай chat-13), и без плашки «чат молчит» неотличим от
// работы.
const stuckAskWord = "ждёт ответа в терминале"

// stuckDeafWord это слова второго рода клина: терминал на месте, приглашение
// рисуется, а событийный цикл мёртв (клиент 69975, чат 29fc49de). Сокет
// такого клиента принимает соединения и байты силами ядра, ввод с клавиатуры
// исчезает без эха, транскрипт стоит, и до зонда доставка выглядела успехом.
const stuckDeafWord = "канал молчит"

// deafTTL держит память зонда: клин не рассасывается сам за секунды, а зонд
// клина стоит целый таймаут, и гонять его на каждую сборку списка нельзя.
const deafTTL = 45 * time.Second

// deafProbeWait это таймаут зонда детектора: живой клиент отвечает за
// миллисекунды (живая проба по всем сессиям машины), полторы секунды тут с
// запасом на занятый цикл.
const deafProbeWait = 1500 * time.Millisecond

type deafEntry struct {
	ok   bool
	born time.Time
}

// peerDeaf отвечает, молчит ли канал живой сессии: транскрипт стоит дольше
// срока клина, а пустой зонд (peerProbe) не дождался закрытия. Свежий
// транскрипт снимает вопрос без зонда: разговор ходит, значит цикл жив.
func (s *server) peerDeaf(sock string, mod time.Time) bool {
	if sock == "" || s.now().Sub(mod) <= stuckLostTerm {
		return false
	}
	now := s.now()
	s.mu.Lock()
	e, hit := s.deaf[sock]
	s.mu.Unlock()
	if hit && now.Sub(e.born) < deafTTL {
		return !e.ok
	}
	ok := s.probe(sock, deafProbeWait) == nil
	s.mu.Lock()
	s.deaf[sock] = deafEntry{ok: ok, born: now}
	s.mu.Unlock()
	return !ok
}

// markDeaf сеет память зонда провалом с ручки отправки: реплика уже упёрлась
// в молчание, и список обязан назвать клин сразу, а не после своего зонда.
func (s *server) markDeaf(sock string) {
	s.mu.Lock()
	s.deaf[sock] = deafEntry{ok: false, born: s.now()}
	s.mu.Unlock()
}

// chatStoreDir это каталог с настройками диалогов: модель живёт файлом при
// диалоге, а не полем реестра, потому что реестр дописывается строками от
// нескольких писателей, и правка одного поля там стоила бы перезаписи журнала.
func chatStoreDir(home string) string {
	return filepath.Join(home, ".devkit", "chats")
}

type chatStore struct {
	Model string `json:"model,omitempty"`
	// Title это заголовок разговора, названный haiku: харнес пишет summary не
	// всякому транскрипту, а первая реплика заголовком не годится. Считается он
	// один раз и живёт тут навсегда.
	Title string `json:"title,omitempty"`
	// Hidden убирает чат из списков насовсем: им помечены пробные чаты, поднятые
	// ради проверки дашборда, у которых метки в промпте не было.
	Hidden bool `json:"hidden,omitempty"`
	// From называет диалог, продолжением которого поднят этот: `claude --resume`
	// заводит новую сессию со своим транскриптом, и без этой ссылки история
	// разговора рвалась бы на две строки списка.
	From string `json:"from,omitempty"`
}

func (s *server) chatStoreRead(key string) chatStore {
	var st chatStore
	data, err := os.ReadFile(filepath.Join(chatStoreDir(s.cfg.Home), key+".json"))
	if err != nil {
		return st
	}
	json.Unmarshal(data, &st)
	return st
}

func (s *server) chatStoreWrite(key string, st chatStore) error {
	dir := chatStoreDir(s.cfg.Home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, key+".json"), data, 0o644)
}

// chatKeyRe сито ключа настроек: ключом бывает ID сессии либо имя tmux-сессии,
// и ни то, ни другое не вправе уводить запись из своего каталога.
var chatKeyRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,120}$`)

// chatModel называет модель диалога. Порядок такой: своя запись при сессии,
// запись при имени tmux-сессии (её кладёт подъём нового диалога, когда ID
// сессии ещё не родился), дальше умолчание.
func (s *server) chatModel(sid, tmux string) string {
	if sid != "" && chatKeyRe.MatchString(sid) {
		if st := s.chatStoreRead(sid); st.Model != "" {
			return st.Model
		}
	}
	if tmux != "" && chatKeyRe.MatchString(tmux) {
		if st := s.chatStoreRead("tmux-" + tmux); st.Model != "" {
			return st.Model
		}
	}
	return chatModelDefault
}

// chatFile это транскрипт вместе с проектом, из чьего обхода он пришёл: общий
// список машины собирает файлы всех проектов разом, и строке надо помнить
// своего, чтобы назвать его на экране и посчитать префикс его доски.
type chatFile struct {
	sessionInfo
	projName string
	projPath string
}

func chatFilesOf(list []sessionInfo, name, path string) []chatFile {
	out := make([]chatFile, 0, len(list))
	for _, f := range list {
		out = append(out, chatFile{sessionInfo: f, projName: name, projPath: path})
	}
	return out
}

// chatEntries собирает список диалогов проекта. Транскрипты идут свежими
// сверху, привязка к задачам приходит из реестра по факту работы плюс хвост
// имени бокового дерева: дерево заводится ровно под одну задачу и врать не
// умеет. Угадывания по первой реплике тут нет вовсе, оно и разводило один
// разговор на четыре карточки.
func (s *server) chatEntries(projPath string, limit int) []chatEntry {
	files := chatFilesOf(sessionFiles(s.transcriptRoots(), projPath), "", projPath)
	return s.chatEntriesFrom(files, limit)
}

// chatAllLimit это потолок общего списка машины: он выше проектного, потому
// что накрывает все доски разом, но остаётся потолком, чтобы обход не
// перечитывал транскрипты машины целиком.
const chatAllLimit = 160

// chatEntriesAll собирает диалоги всех проектов машины в один список:
// переключение на чужой разговор не должно требовать смены проекта доски, а
// принадлежность строки называет поле Project. Шапки читаются только у файлов
// над общим потолком, свежие сверху, и лежат в той же памяти процесса, что и у
// проектного списка.
func (s *server) chatEntriesAll(limit int) []chatEntry {
	roots := s.transcriptRoots()
	projects, _ := s.projects()
	byPath := map[string]chatFile{}
	for _, p := range projects {
		for _, f := range sessionFiles(roots, p.Path) {
			// Каталог бокового дерева задачи попадает и в обход родителя (имя
			// продолжает его дефисом), и в обход собственного проекта, когда
			// дерево стоит рядом с ним. Файл остаётся за тем, чьё имя каталога
			// длиннее: он и есть более точный хозяин.
			if had, ok := byPath[f.path]; ok && len(claudeDirName(had.projPath)) >= len(claudeDirName(p.Path)) {
				continue
			}
			byPath[f.path] = chatFile{sessionInfo: f, projName: p.Name, projPath: p.Path}
		}
	}
	files := make([]chatFile, 0, len(byPath))
	for _, f := range byPath {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].ID < files[j].ID
	})
	return s.chatEntriesFrom(files, limit)
}

// chatEntriesFrom строит строки списка по готовому набору файлов. Общее
// хозяйство захода (реестр записей, свёртка имён tmux, живые сессии, имена
// подписок) считается один раз, сколько бы проектов ни легло в обход, а
// префикс доски берётся по проекту файла и помнится на заход.
func (s *server) chatEntriesFrom(files []chatFile, limit int) []chatEntry {
	recs := sessions.LoadAll(s.cfg.Home)
	// Имя tmux сворачивается по последней записи всего реестра: клиент за
	// диалогами доверия и импортов пересоздаёт сессию, и одно имя носят
	// несколько записей подряд, а поверх этого имя переиспользуется между
	// проектами (живой случай chat-DK-397-2: старая сессия соседнего проекта
	// держала имя живой tmux-сессии и выглядела живым разговором). Имя
	// достаётся сессии, чья запись с ним свежее всех, у остальных снимается.
	tmuxClaim := map[string]string{}
	tmuxWhen := map[string]string{}
	for sid, rs := range recs {
		for _, rec := range rs {
			if rec.Tmux == "" {
				continue
			}
			if w, ok := tmuxWhen[rec.Tmux]; !ok || rec.Time > w {
				tmuxClaim[rec.Tmux], tmuxWhen[rec.Tmux] = sid, rec.Time
			}
		}
	}
	alive := tmuxAliveFn()
	names := harnessRoots(s.harnesses())
	live := s.peers()
	cutoff := s.now().Add(-sessionLiveTTL)
	// Префикс доски нужен ровно затем, чтобы отличить чужую задачу от своей:
	// сессия соседнего проекта попадает в список по общему каталогу
	// транскриптов, и без этой проверки её задача читалась бы как задача этой
	// доски. Тот же разбор ведёт список работ (sessionWorks).
	prefixes := map[string]string{}
	prefixOf := func(projPath string) string {
		if p, ok := prefixes[projPath]; ok {
			return p
		}
		p := ""
		if raw, err := s.projectBoard(projPath); err == nil {
			if b, err := parseBoardView(raw); err == nil {
				p = b.Prefix
			}
		}
		prefixes[projPath] = p
		return p
	}
	out := []chatEntry{}
	for i, f := range files {
		if limit > 0 && i >= limit {
			break
		}
		prefix := prefixOf(f.projPath)
		head := s.sessionHeadCached(f.path, f.stamp)
		// Служебная сессия суммаризации чатом не является: её завёл сам
		// дашборд ради заголовка, и в списке ей делать нечего.
		if titleSession(head.First) || s.chatStoreRead(f.ID).Hidden {
			continue
		}
		last := sessions.Last(recs[f.ID])
		tasks := sessions.Touched(recs[f.ID])
		if id := taskIDInName(f.suffix); id != "" && !hasTask(tasks, id) {
			tasks = append([]string{id}, tasks...)
		}
		task, note, bound := bindTask(s.binds(), f.ID, f.suffix, head)
		if task != "" && prefix != "" && !strings.HasPrefix(task, prefix+"-") {
			note = foreignTaskNote
		} else if bound == boundLead {
			// Работа своей доски подписи не просит: заголовок разговора
			// говорит про неё больше, чем «по дереву задачи».
			note = ""
		}
		e := chatEntry{
			ID: f.ID, Project: f.projName,
			Title: head.First, First: head.First, Summary: head.Summary,
			Mtime: saidAt(head, f.sessionInfo), Tasks: tasks,
			Note: note, Bound: bound,
			LiveModel: modelShort(readSessionModel(f.path)),
			Own:       last.Tmux != "",
			Tmux:      last.Tmux, Tree: f.suffix, Branch: head.Branch,
			Harness: names[f.root],
			Model:   s.chatModel(f.ID, last.Tmux),
		}
		// Устаревшее имя снимается: живую tmux-сессию под этим именем ведёт
		// другая, более свежая запись реестра, и мерить ею живость этого
		// разговора значило бы показывать его живым и ловить его по ?tmux=.
		if e.Tmux != "" && tmuxClaim[e.Tmux] != f.ID {
			// Имя занял другой разговор: это и есть след перезапуска работы.
			// Прежде имя просто снималось молча, и панель показывала кончившийся
			// разговор обычным, а реплика в него уезжала мимо человека.
			e.Gone, e.GoneTo = goneRestartWord, tmuxClaim[e.Tmux]
			e.Tmux = ""
		}
		// Свою tmux-сессию дашборд поднял сам и знает, какой моделью её
		// поднял: транскрипт называет модель только с первого ответа, и сразу
		// после смены он ещё какое-то время говорит прежнее имя. Ровно это и
		// видел человек: перезапустил разговор с opus, а в селекторе остался
		// fable. Чужое окно (vscode) своей записи не заводит, и там модель
		// по-прежнему читается из транскрипта.
		if e.Tmux != "" && chatKeyRe.MatchString(e.Tmux) {
			if st := s.chatStoreRead("tmux-" + e.Tmux); st.Model != "" {
				e.LiveModel = st.Model
			}
		}
		// Мера состояния: реестр живых сессий клиента старше всего остального.
		// Есть запись с живым процессом, значит диалог идёт и ему есть куда
		// писать, чем бы он ни был поднят, окном vscode или tmux-сессией
		// дашборда. Прежняя мера (своё имя tmux плюс свежесть транскрипта)
		// осталась запасной: реестр появился не во всякой версии клиента.
		if p, ok := live[f.ID]; ok {
			// Живой сокет старше следа перезапуска: разговор идёт, что бы ни
			// говорило занятое имя, и объявлять его снятым нельзя.
			e.Gone, e.GoneTo = "", ""
			e.Sock, e.PID, e.Where = p.Sock, p.PID, peerWord(p)
			if p.Tmux != "" && e.Tmux == "" {
				e.Tmux = strings.SplitN(p.Tmux, ":", 2)[0]
			}
		}
		// Клин ищется там же, где меряется состояние: у клина все признаки
		// живого разговора, и без отдельной проверки он им и остаётся. Родов
		// два: пропавший терминал и мёртвый событийный цикл при живом pty,
		// второй ловится зондом канала по стоящему транскрипту.
		if lostTerminal(e.PID, e.Tmux, alive, f.mod, s.now()) {
			e.Stuck = stuckLostTermWord
		} else if e.Sock != "" && s.peerDeaf(e.Sock, f.mod) {
			e.Stuck = stuckDeafWord
		} else if (e.Sock != "" || (e.Tmux != "" && alive(e.Tmux))) && s.chatStuck(f.ID) != "" {
			// Третий род: живой клиент стоит на вопросе в своём терминале
			// (разрешение, доверие каталогу первого запуска). Мера та же, что
			// у ответа на реплику: последняя запись сессии в журнале
			// уведомителя это permission_prompt.
			e.Stuck = stuckAskWord
		}
		// Занятость разговора считается для всех живых, а не только для тех, у
		// кого нашлась запись в реестре клиента. Прежде поле Idle оставалось
		// нулевым (то есть «занят») у разговора, чьей записи в реестре нет
		// вовсе: процесс давно умер, tmux-сессия жива, и список рисовал
		// семичасовой разговор активным (замечание пользователя про сессию,
		// в которой давно никто не писал). Мера тут та же, что у работ:
		// транскрипт старше всего, а слову реестра верят, пока запись свежа.
		e.Idle = !s.sessionBusy(f.path, s.now())
		if p, ok := live[f.ID]; ok && p.Status == "busy" && peerFresh(p, s.now()) {
			e.Idle = false
		}
		switch {
		case e.Sock != "":
			e.State = chatLive
		case e.Tmux != "" && alive(e.Tmux):
			e.State = chatLive
		case e.Tmux != "":
			e.State = chatDead
		case f.mod.After(cutoff):
			e.State = chatVscode
		default:
			e.State = chatDead
		}
		out = append(out, e)
	}
	// Порядок списка это порядок разговора, а не порядок касаний файла: пока
	// сортировки тут не было, список стоял так, как его отдал обход каталога, то
	// есть по времени правки транскриптов (замечание пользователя).
	sortEntries(out)
	return out
}

func hasTask(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// chatListLimit держит число транскриптов, у которых читается шапка на один
// заход списка: заголовок диалога это первая реплика, и её чтение платится
// один раз на транскрипт, дальше шапка лежит в памяти процесса.
const chatListLimit = 80

func (s *server) handleChatList(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "чаты")
	if found == nil {
		return
	}
	// Общий список машины (?all=1): диалоги всех проектов разом, у каждой
	// строки назван её проект. Панель выбирает разговор из него, не меняя
	// проекта доски; проектный список остаётся для точечных вопросов вроде
	// поиска по имени tmux.
	var list []chatEntry
	if r.URL.Query().Get("all") != "" {
		list = s.chatEntriesAll(chatAllLimit)
	} else {
		list = s.chatEntries(found.Path, chatListLimit)
	}
	// Поиск по имени tmux-сессии: им дашборд узнаёт ID сессии, поднятой минуту
	// назад, когда хук старта уже успел записать строку реестра.
	if name := strings.TrimSpace(r.URL.Query().Get("tmux")); name != "" {
		var hit []chatEntry
		for _, e := range list {
			if e.Tmux == name {
				hit = append(hit, e)
			}
		}
		list = hit
	}
	if want := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("task"))); want != "" {
		var hit []chatEntry
		for _, e := range list {
			if hasTask(e.Tasks, want) {
				hit = append(hit, e)
			}
		}
		list = hit
	}
	if list == nil {
		list = []chatEntry{}
	}
	s.titleFill(list)
	resp := map[string]any{"project": found.Name, "chats": list, "models": s.chatModelOpts()}
	if len(list) == 0 {
		resp["note"] = "чатов тут пока нет: заведите новый кнопкой «+»"
	}
	writeJSON(w, http.StatusOK, resp)
}

// chatNewName выбирает имя tmux-сессии диалога: chat-<ID>-<n> у диалога с
// задачей, chat-<n> у диалога без неё. Номер не растёт вечно, снятый диалог
// отдаёт имя следующему.
func chatNewName(id string, alive func(string) bool) string {
	base := "chat"
	if id != "" {
		base = "chat-" + id
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", base, n)
		if !alive(name) {
			return name
		}
	}
}

// chatCmd собирает команду клиента для tmux. Реплика человека едет первым
// аргументом: интерактивный клиент берёт её как первый вопрос и остаётся
// стоять, дальше реплики подаются в тот же процесс через send-keys.
// planRule это правило плана в заказе любой поднятой работы: чата, конвейерной
// сессии задачи и груминга черновика. План ведётся файлом, а не инструментом
// TodoWrite: в обход разрешений (--dangerously-skip-permissions) харнес его не
// выдаёт вовсе, и у сессий дашборда дороги, кроме файла, нет. Чаты дашборда поднимаются
// голым клиентом, без определений исполнителей конвейера, и вести план им
// некому было велеть: кольцо в шапке разговора рисует деления как раз по этому
// плану, а без него оно остаётся ровной дорожкой.
const planRule = "Веди план работ файлом ~/.devkit/plans/<ID сессии>.json " +
	"(ID в CLAUDE_CODE_SESSION_ID): до первого шага список этапов массивом " +
	"{\"text\",\"state\"}, помечай текущий in_progress, закрывай сделанные, " +
	"пиши файл целиком."

// planRuleFor приклеивает к правилу плана запасной адрес. В контуре второй
// подписки CLAUDE_CODE_SESSION_ID пуст (переменную кладёт окружение подписки
// по умолчанию, а не сам клиент), и агент DK-269 сжёг первый десяток ходов,
// разыскивая свой ID по printenv и каталогу планов. Имя tmux-сессии заказ
// знает дословно, им план и ведётся, а читатель плана смотрит оба адреса
// (planOf).
func planRuleFor(sess string) string {
	if sess == "" {
		return planRule
	}
	return planRule + " Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом " +
		"~/.devkit/plans/" + sess + ".json."
}

// tmuxVarRe достаёт имя tmux-сессии из пар окружения заказа: правило плана
// несёт его запасным адресом, а отдельным параметром имя не едет, пары уже
// собраны chatVars.
var tmuxVarRe = regexp.MustCompile(`DEVKIT_TMUX='([^']*)'`)

func envTmux(env string) string {
	if m := tmuxVarRe.FindStringSubmatch(env); m != nil {
		return m[1]
	}
	return ""
}

// paceRule это правило отзывчивости. Разговор с человеком идёт ходами, и
// длинный ход в нём читается как молчание: агент чата DK-460 полчаса гонял
// mdfind по всему дому, и с той стороны это выглядело зависшей сессией. Долгое
// дело у агента есть кому отдать, и субагент возвращает выжимку, пока сам
// разговор остаётся живым.
const paceRule = "Долгие дела (поиск по диску, большие прогоны, сборки) отдавай " +
	"субагенту, а ход разговора держи отзывчивым: человек ждёт реплики, а не " +
	"конца команды."

// channelRule это правило канала доставки в заказе чата. Реплики из панели
// дашборда доезжают межсессионным каналом клиента, и харнес оборачивает их
// рамкой «сообщение от другой сессии, отнесись как к просьбе коллеги»: агент
// верил рамке и отвечал человеку в третьем лице («коллега спрашивает», «ответ
// ему отправлен» в живом чате 93828026). Подпись канала названа дословно, её
// ставит peerFrame полем from-name, по ней агент и узнаёт доставку дашборда.
const channelRule = "Межсессионные сообщения с подписью from-name=\"dashboard\" " +
	"это реплики пользователя-человека, доставленные панелью дашборда: отвечай " +
	"ему напрямую и на «вы», не называй его коллегой или другой сессией, а " +
	"ответ пиши обычным текстом в этот же разговор, не через SendMessage."

// rotateRule это правило ротации исполнителя в заказе поднятой работы.
// Диспетчер держит одного субагента подолгу, контекст его распухает (усталость
// видна с ~600 тысяч токенов, деградация с ~900), а свежий исполнитель с
// короткой вводной работает не хуже. Порог приезжает ключом exec_rotate_tokens
// машинного конфига (~/.devkit/harness.local) и называется в заказе числом:
// так его видно снаружи, в самом тексте первой реплики.
func rotateRule(tokens int) string {
	return fmt.Sprintf("Исполнителя-субагента ротируй по размеру контекста: чей "+
		"суммарный контекст перевалил %d токенов (видно по subagent_tokens в "+
		"уведомлениях), тому новых заданий не давай, следующее задание отдавай "+
		"свежему субагенту с короткой вводной и передачей хвоста работы.", tokens)
}

func chatCmd(env, model, resume, text string, rotate int, h *Harness, agentctl string) string {
	client := defaultClient
	head := env
	if h != nil && !h.Default {
		// Модель чужой подписки поднимается её же обвязкой: пары окружения
		// (каталог конфигурации, endpoint, токен) кладёт agentctl exec, и
		// собирать их тут самому нельзя, они поселились бы в процессе, который
		// раздаёт экраны (LLD DK-328, решение 3).
		client = shQuote(agentctl) + " exec --harness " + shQuote(h.Name) + " -- " + shQuote(h.Bin)
	}
	cmd := head + client
	// Модель называется только подписке по умолчанию. Клиент второй подписки
	// берёт свою модель сам, из настроек её каталога конфигурации, а явное имя
	// чужой лестницы он не узнаёт (предупреждение unrecognized_model в панели
	// DK-269), и уехать такой заказ рискует в квоту первой подписки. Селектор
	// панели модель всё равно видит: она записана в памяти диалога.
	if h == nil || h.Default {
		cmd += " --model " + shQuote(model)
	} else {
		// Режим разрешений чужой подписке называется флагом, а не берётся из
		// её профиля: свежий профиль поднимает клиента в ручном режиме, и чат
		// вставал с вопросом «Do you want to proceed?» на каждом инструменте
		// (живой случай, chat-13 на второй подписке). Чаты подписки по
		// умолчанию идут в авто-режиме, и одинаковость поведения не должна
		// зависеть от содержимого чужого конфига.
		cmd += " --permission-mode auto"
	}
	if resume != "" {
		cmd += " --resume " + shQuote(resume)
	}
	if text != "" {
		// Правило плана цепляется только к заказу подъёма: у резюма текст это
		// реплика человека, и приписывать к ней наше правило значило бы
		// говорить за него. Ротация исполнителя едет тем же вагоном и по той
		// же причине.
		if resume == "" {
			text += " " + planRuleFor(envTmux(env)) + " " + rotateRule(rotate)
		}
		// Отзывчивость же нужна и резюму, и подъёму: молчаливый получасовой
		// прогон случается как раз в длинном разговоре, а он идёт резюмами.
		// Правило канала едет туда же: реплики панели доезжают межсессионным
		// каналом в любую сессию чата, и узнавать в них человека обязан и
		// поднятый, и продолженный разговор.
		text += " " + paceRule + " " + channelRule
		cmd += " " + shQuote(text)
	}
	return cmd
}

// chatVars это пары окружения диалога: задачу и имя tmux-сессии поднятая сессия
// называет о себе в реестре сама, хуком старта.
func chatVars(id, sess string) string {
	env := "DEVKIT_TMUX=" + shQuote(sess) + " "
	if id != "" {
		env = "DEVKIT_TASK=" + shQuote(id) + " " + env
	}
	// Дом ставится явно: tmux-сервер, поднятый самим демоном, наследует его
	// подложный HOME, и клиент в такой сессии не находит ни хуков, ни логина.
	// Уже поднятый сервер держит настоящий дом сам, и лишним это не будет.
	if home := realHome(); home != "" {
		env = "HOME=" + shQuote(home) + " " + env
	}
	// Опрос фокуса в сессии, поднятой дашбордом, не нужен вовсе: он ходит в
	// System Events, а macOS приписывает это дашборду и просит у него
	// разрешение на управление компьютером, заново после каждой пересборки
	// (находка одиннадцатого круга POC). Уведомления от такой сессии идут как
	// при неопределённом фокусе, то есть приходят.
	return noFocusEnv + " " + env
}

// noFocusEnv гасит опрос фокуса в хуке уведомителя. Ставится он всему, что
// поднимает дашборд; интерактивных сессий человека это не касается.
const noFocusEnv = "DEVKIT_NO_FOCUS=1"

// handleChatStart поднимает новый диалог: первая реплика человека и есть его
// начало, заголовок берётся из неё. Задача необязательна: разговор без задачи
// это обычное дело, и заводится он тем же порядком.
func (s *server) handleChatStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "подъём чата")
	if found == nil {
		return
	}
	var body struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	id := strings.ToUpper(strings.TrimSpace(body.ID))
	if id != "" && !taskParamRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на ID задачи", body.ID)})
		return
	}
	text := chatText(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустая реплика чата не поднимает: чат начинается со слов человека"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = chatModelDefault
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Дерево задачи предпочитается корню проекта: разговор про задачу, у которой
	// заведено боковое дерево, идёт там же, где её работа.
	dir := found.Path
	if id != "" {
		if tree := filepath.Join(filepath.Dir(found.Path), filepath.Base(found.Path)+"-"+strings.ToLower(id)); isDir(tree) {
			dir = tree
		}
	}
	sess := chatNewName(id, tmuxAliveFn())
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model}); err != nil {
		s.logf("модель чата %s не записалась: %v", sess, err)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, "", text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("подъём чата в %s не удался: %s", found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.logf("чат поднят в %s (tmux-сессия %s, модель %s, дерево %s)", found.Name, sess, model, dir)
	writeJSON(w, http.StatusOK, map[string]string{
		"tmux": sess, "model": model, "tree": dir,
		"message": fmt.Sprintf("чат поднят в tmux-сессии %s моделью %s: ID сессии встанет в списке первым её ходом", sess, model)})
}

// chatText готовит реплику человека к отправке. Переносы строк тут священны:
// человек пишет списком и абзацами, а прежняя сборка гнала текст через
// strings.Fields и склеивала всё в одну строку, отчего нумерованный список
// приезжал агенту кашей. Схлопывается только лишнее: возврат каретки, пробелы
// по краям строк и хвостовые пустые строки.
// killWedged снимает зависший процесс клиента. Ход ему не прервать: он стоит
// на записи в терминал, которого нет, и ни Escape, ни реплика в сокет до него
// не доходят. Сигнал идёт мягкий, а потом жёсткий: клиент, который ещё способен
// закрыться сам, закроется и запишет транскрипт, а вставший намертво уйдёт по
// KILL. Состояние разговора на диске от этого не теряется: следующая реплика
// поднимет ту же сессию резюмом.
func (s *server) killWedged(w http.ResponseWriter, sid, tmux string, alive func(string) bool) {
	p, ok := s.peers()[sid]
	if !ok || p.PID <= 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
			"процесса чата %s в реестре клиента нет: снимать нечего", sid)})
		return
	}
	if tmux != "" && alive(tmux) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
			"tmux-сессия %s жива: это не клин, ход прерывается обычным стопом", tmux)})
		return
	}
	proc, err := os.FindProcess(p.PID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"процесс %d не нашёлся: %v", p.PID, err)})
		return
	}
	proc.Signal(syscall.SIGTERM)
	time.Sleep(chatKillPause)
	if pidAlive(p.PID) {
		proc.Signal(syscall.SIGKILL)
	}
	s.logf("зависший чат %s снят сигналом (pid %d, терминал %s пропал)", sid, p.PID, tmux)
	writeJSON(w, http.StatusOK, map[string]any{"way": "kill", "pid": p.PID,
		"message": "зависший процесс снят: следующая реплика поднимет разговор резюмом"})
}

// chatKillPause это пауза между мягким сигналом и жёстким: клиенту дают
// закрыться самому, но не дают тянуть ответ ручке.
const chatKillPause = 700 * time.Millisecond

func chatText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n \t")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// chatSendPause это пауза между текстом и переводом строки при подаче реплики
// в живой процесс. Клиент читает ввод построчно и рисует его в поле; Enter,
// пришедший в том же пакете, что и текст, обгоняет отрисовку, и в поле
// остаётся половина реплики.
var chatSendPause = 250 * time.Millisecond

// chatSend подаёт реплику в живой процесс tmux-сессии. Текст идёт литералом
// (-l), иначе tmux разобрал бы слова вроде «Enter» и «C-c» как имена клавиш.
// Многострочная реплика едет в скобках вставки (bracketed paste): без них
// перенос строки внутри текста клиент читает как нажатие Enter и отправляет
// первую строку, а остальные разбирает как отдельные реплики.
func chatSend(name, text string) error {
	body := text
	if strings.Contains(text, "\n") {
		body = "\x1b[200~" + text + "\x1b[201~"
	}
	if _, err := runProc("tmux", "send-keys", "-t", "="+name+":", "-l", body); err != nil {
		return err
	}
	time.Sleep(chatSendPause)
	_, err := runProc("tmux", "send-keys", "-t", "="+name+":", "Enter")
	return err
}

// Прерывание хода: два Escape в TUI клиента снимают текущий ход и оставляют
// сессию жить дальше. Убийство сессии сюда не годится: прерывают ход, а не
// разговор, и следующая реплика должна попасть в ту же сессию с её памятью.
// chatStopPause это пауза между двумя Escape. Один клавиатурный ход клиент
// тратит на своё состояние (снимает подсказку, выходит из режима ввода), и ход
// от него не прерывается: проверено живым прогоном, где после одного Escape
// журнал субагента продолжал расти, а после второго встал.
const chatStopPause = 400 * time.Millisecond

func chatStop(name string) error {
	if _, err := runProc("tmux", "send-keys", "-t", "="+name+":", "Escape"); err != nil {
		return err
	}
	time.Sleep(chatStopPause)
	_, err := runProc("tmux", "send-keys", "-t", "="+name+":", "Escape")
	return err
}

// handleChatStop прерывает идущий ход чата. Прервать можно только то, что
// поднято нашей tmux: у окна vscode и у мёртвой сессии клавиатуры отсюда нет,
// и кнопки стопа у них на экране тоже нет.
func (s *server) handleChatStop(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "стоп чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	// Стоп бывает двух родов, и род называет зовущий: обычный это Escape в
	// живой терминал, а клин снимается сигналом самому процессу. Мёртвому
	// терминалу Escape подать некуда, и разговор в клине оставался бы вечным.
	var body struct {
		Kill bool `json:"kill"`
		// Drop это третий род: снять живую сессию целиком под перезапуск
		// (смена модели вступает только новым подъёмом клиента). Это не клин,
		// у клина свой род, и не Escape: ход тут не прерывают, а сессию
		// заканчивают, чтобы следующая реплика подняла её резюмом.
		Drop bool `json:"drop"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body)
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	last := sessions.Last(sessions.LoadAll(s.cfg.Home)[sid])
	alive := tmuxAliveFn()
	if body.Drop {
		// Сессия, которой уже нет, это не отказ, а сделанное дело: человек жал
		// закрытие, и закрыто оно и есть. Прежде сюда приходил 409 со словами
		// «снимать под перезапуск нечего», экран показывал красную карточку, а
		// строка оставалась стоять, и второе нажатие упиралось в тот же отказ
		// (живой случай пользователя: tmux-сессии на машине давно не было).
		// Разговор, который дашборд не поднимал вовсе, это другой случай: там
		// снимать и правда нечего, и сказать об этом надо.
		if last.Tmux != "" && !alive(last.Tmux) {
			s.logf("сессия чата %s уже закрыта: tmux-сессии %s нет", sid, last.Tmux)
			writeJSON(w, http.StatusOK, map[string]any{"way": "gone", "tmux": last.Tmux,
				"message": "сессия уже закрыта"})
			return
		}
		if last.Tmux == "" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
				"чат %s не в нашей tmux: его окно поднимал не дашборд, снимать отсюда нечего", sid)})
			return
		}
		if _, err := runProc("tmux", "kill-session", "-t", last.Tmux); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
				"tmux-сессия %s не снялась: %s", last.Tmux, procErr(err))})
			return
		}
		s.logf("сессия чата %s снята под перезапуск (tmux-сессия %s)", sid, last.Tmux)
		writeJSON(w, http.StatusOK, map[string]any{"way": "drop", "tmux": last.Tmux,
			"message": "сессия снята: следующая реплика поднимет разговор резюмом"})
		return
	}
	if body.Kill {
		s.killWedged(w, sid, last.Tmux, alive)
		return
	}
	// Прерывать нечего по двум разным причинам, и звучат они по-разному. Наша
	// сессия кончилась, значит и ход в ней кончился: это спокойная новость, а
	// не сбой. Чужое окно дашборд не поднимал, и клавиатуры к нему у него нет.
	if last.Tmux != "" && !alive(last.Tmux) {
		writeJSON(w, http.StatusOK, map[string]any{"way": "gone", "tmux": last.Tmux,
			"message": "ход уже не идёт: сессия закрыта"})
		return
	}
	if last.Tmux == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
			"чат %s не в нашей tmux: его окно поднимал не дашборд, прервать ход отсюда нечем", sid)})
		return
	}
	if err := chatStop(last.Tmux); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"прерывание не подалось в tmux-сессию %s: %s", last.Tmux, procErr(err))})
		return
	}
	s.logf("ход чата %s прерван (tmux-сессия %s)", sid, last.Tmux)
	writeJSON(w, http.StatusOK, map[string]any{"way": "escape", "tmux": last.Tmux,
		"message": "ход прерван: сессия жива и ждёт следующей реплики"})
}

// handleChatSay доставляет реплику диалогу. Правило одно на три состояния:
// живому процессу реплика подаётся прямо в него, кончившемуся поднимается
// продолжение той же сессии, а окно vscode дашборду не принадлежит, и туда
// человек пишет сам.
func (s *server) handleChatSay(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "реплика чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	var body struct {
		Text string `json:"text"`
		// MsgID это ключ записи в очереди исходящих панели: один и тот же у
		// первой отправки и у всех её повторов.
		MsgID string `json:"msg_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	text := chatText(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустая реплика никуда не едет"})
		return
	}
	// Повтор той же записи дальше не едет. Отправитель повторяет её, когда не
	// увидел ответа ручки, а не увидеть его он может и после доставки: связь
	// рвётся на обратном пути, панель считает реплику неушедшей и шлёт снова.
	// Живой случай: одна реплика доехала до сессии пятью копиями подряд, пять
	// отдельных отправок с разницей в минуты, и каждая легла в очередь клиента,
	// потому что общего ключа у повторов не было вовсе.
	claim := body.MsgID
	if claim != "" {
		if prev, taken := s.chatSayStart(sid, claim); !taken {
			s.logf("повтор реплики чата %s отброшен: та же запись отправителя уже %s", sid, prevWord(prev))
			writeJSON(w, http.StatusOK, map[string]any{"way": "dup", "was": prev.way,
				"message": "эта реплика уже " + prevWord(prev) + ", повтор никуда не поехал"})
			return
		}
		// Незакрытая попытка ключ отпускает: отказ доставки это повод повторить,
		// и держать запись занятой после него значило бы хоронить реплику.
		defer s.chatSayRelease(sid, claim)
	}
	info, ok := findSession(s.transcriptRoots(), found.Path, sid)
	if !ok {
		// Транскрипта нет вовсе: разговор заведён, а сессии за ним никогда не
		// было (произвольный чат, у которого подъём не случился). Отказ тут
		// ронял реплику в никуда, и человек писал в пустой чат по разу в день,
		// не получая ответа (жалоба пользователя). Реплика человека и есть
		// начало разговора: она поднимает сессию заказом.
		s.chatRaiseSay(w, found, sid, text, claim)
		return
	}
	recs := sessions.LoadAll(s.cfg.Home)
	last := sessions.Last(recs[sid])
	// Первым делом канал самого клиента: живая сессия принимает реплику прямо в
	// свой сокет и просыпается за секунды, чем бы она ни была поднята. Окно
	// vscode отсюда тоже слышно, и отказывать ему больше не за что.
	if p, ok := s.peers()[sid]; ok {
		err := peerSay(p.Sock, text)
		if err == nil {
			s.chatSayDone(sid, claim, "socket")
			s.saidSay(saidSessionKey(sid), text, "socket")
			s.logf("реплика ушла в сокет чата %s (pid %d, %s)", sid, p.PID, peerWord(p))
			out := map[string]any{"way": "socket", "pid": p.PID, "where": peerWord(p)}
			// Клиент с пропавшим терминалом берёт реплику сокетом так же
			// охотно, как живой, и кладёт её в очередь, которую уже некому
			// разобрать: про клин надо сказать здесь, иначе доставка выглядит
			// удачной (инцидент с чатом DK-460).
			if lostTerminal(p.PID, last.Tmux, tmuxAliveFn(), info.mod, s.now()) {
				out["stuck"] = stuckLostTermWord
				out["wedged"] = true
				s.logf("реплика чата %s легла в очередь клина: %s", sid, stuckLostTermWord)
				writeJSON(w, http.StatusOK, out)
				return
			}
			// Взятая сокетом реплика доставленной ещё не значит: клиент,
			// стоящий на вопросе разрешения, кладёт её в очередь и молчит.
			if why := s.chatStuck(sid); why != "" {
				out["stuck"] = why
				s.logf("реплика чата %s легла в очередь: %s", sid, why)
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		if errors.Is(err, errPeerNoAck) {
			// Байты ушли, подтверждения нет: событийный цикл клиента мёртв при
			// живом pty (клин клиента 69975). Запасные дороги тут не выход:
			// send-keys в замороженный терминал исчезает без эха, а резюм
			// поверх живого процесса завёл бы второго агента. Реплика остаётся
			// недоставленной в исходящем, клин назван словами, а выход из него
			// стоит кнопкой на плашке.
			s.markDeaf(p.Sock)
			s.logf("клиент чата %s не подтвердил доставку (pid %d): %v", sid, p.PID, err)
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": "клиент принял байты, но не подтвердил доставку: событийный цикл стоит, реплика не доставлена",
				"stuck": stuckDeafWord})
			return
		}
		// Сокет есть, а разговора по нему не вышло: сессия могла умереть
		// между чтением реестра и записью. Дальше идут запасные дороги, и
		// причина остаётся в журнале, а не пропадает молча.
		s.logf("сокет чата %s не взял реплику, иду запасной дорогой: %v", sid, err)
	}
	alive := tmuxAliveFn()
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Имя tmux адресом не работает без сверки хозяина: конвейер задачи снимает
	// сессию и поднимает новую тем же именем, реестр при снятии не правится, а
	// свёртка sessions.Last тянет Tmux из прежних записей. Живое имя, которое
	// реестр отдал другому разговору, значит занятое имя, и send-keys по нему
	// уехал бы в чужую сессию (DK-397 POC). Тогда процесса у диалога нет, и
	// дальше идёт обычный резюм той же сессии.
	held := sessions.TmuxOwner(recs, last.Tmux)
	if held != "" && held != sid {
		s.logf("имя tmux %s занято разговором %s, а не %s: реплика идёт резюмом, а не в чужую сессию",
			last.Tmux, held, sid)
	}
	if last.Tmux != "" && alive(last.Tmux) && (held == "" || held == sid) {
		if err := chatSend(last.Tmux, text); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
				"реплика не подалась в tmux-сессию %s: %s", last.Tmux, procErr(err))})
			return
		}
		s.chatSayDone(sid, claim, "send-keys")
		s.saidSay(saidSessionKey(sid), text, "send-keys")
		s.logf("реплика подана в чат %s (tmux-сессия %s)", sid, last.Tmux)
		out := map[string]any{"way": "send-keys", "tmux": last.Tmux,
			"message": "реплика подана прямо в процесс агента: ответ придёт в ленту"}
		if why := s.chatStuck(sid); why != "" {
			out["stuck"] = why
			s.logf("реплика чата %s легла в очередь: %s", sid, why)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// Отказа «разговор идёт в vscode» тут больше нет: канал клиента достаёт
	// любое живое окно, и раз до сюда дошло, живого процесса у диалога нет ни в
	// реестре, ни в tmux. Свежий транскрипт без сокета значит клиента старой
	// версии либо сессию, умершую только что, и обоим годится резюм.
	// Процесса нет: поднимается продолжение той же сессии. История не рвётся,
	// клиент дочитывает её сам по --resume.
	task := ""
	if len(recs[sid]) > 0 {
		if t := sessions.Touched(recs[sid]); len(t) > 0 {
			task = t[0]
		}
	}
	if id := taskIDInName(info.suffix); id != "" {
		task = id
	}
	dir, okTree := sessionTree(found.Path, info.suffix)
	if !okTree {
		dir = found.Path
	}
	model := s.chatModel(sid, last.Tmux)
	// Реплики, которые агент так и не прочитал, едут вводной резюма. Клин берёт
	// их сокетом и складывает в свою очередь, а та умирает вместе с процессом:
	// без этого человек пишет три реплики, снимает клин и получает ответ только
	// на последнюю (инцидент с чатом DK-460).
	text = withLost(s.lostSaid(sid, info.mod), text)
	sess := chatNewName(task, alive)
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, From: sid}); err != nil {
		s.logf("настройки чата %s не записались: %v", sess, err)
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(task, sess), model, sid, text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		msg := fmt.Sprintf("tmux не поднял продолжение чата %s: %s", sid, procErr(err))
		s.logf("%s", msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	// Имя tmux-сессии ложится в реестр на старый ID сразу: хук старта запишет
	// свою строку, когда клиент родится, а до того мера живости диалога уже
	// обязана работать, иначе вторая реплика подряд подняла бы второй резюм.
	sessions.Append(sessions.Path(s.cfg.Home),
		sessions.Line(s.now(), sid, sessions.Bind{Task: task, Source: "заказ",
			Project: found.Name, Tree: dir, Tmux: sess}, "резюм чата"))
	// Резюм увёз реплику вводной: дожимать её после этого нечем, иначе тот же
	// текст приедет и вводной, и повтором.
	s.chatSayDone(sid, claim, "resume")
	s.saidSay(saidSessionKey(sid), text, "resume")
	s.logf("чат %s продолжен резюмом в tmux-сессии %s (модель %s)", sid, sess, model)
	writeJSON(w, http.StatusOK, map[string]any{"way": "resume", "tmux": sess, "model": model,
		"message": fmt.Sprintf(
			"процесса у чата не было: поднят claude --resume в tmux-сессии %s, история продолжена", sess)})
}

// lostSaid собирает реплики, сказанные после того, как транскрипт замолчал:
// всё, что легло в журнал позже последнего хода агента, до него не доехало.
// Мера грубая нарочно: журнал знает время записи, а транскрипт время хода, и
// сойтись точнее им негде. Лишняя реплика во вводной резюма это повтор, а
// потерянная это молчание в ответ на сказанное.
func (s *server) lostSaid(sid string, mod time.Time) []string {
	out := []string{}
	for _, r := range saidLoad(s.cfg.Home, saidSessionKey(sid)) {
		// Пометка ленты это не сказанное человеком: подкладывать её в вводную
		// резюма значило бы говорить за него.
		if r.Role != "user" {
			continue
		}
		at, err := time.Parse(time.RFC3339, r.Time)
		if err != nil || !at.After(mod) {
			continue
		}
		if t := strings.TrimSpace(r.Text); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// withLost клеит недоставленные реплики к новой: агент читает их подряд, как
// прочитал бы в разговоре, и знает, что они пришли раньше.
func withLost(lost []string, text string) string {
	if len(lost) == 0 {
		return text
	}
	head := "Разговор завис, и эти реплики до тебя не дошли: " +
		strings.Join(lost, "\n") + "\n\nПоследняя реплика: "
	return head + text
}

// chatStuck говорит, стоит ли сессия чата на вопросе, который дашборду не
// закрыть: клиент спросил разрешение в своём окне и ждёт человека там. Реплика
// в такую сессию уходит без отказа (сокет её берёт), но ходу не даёт, и в
// ленте она выглядела доставленной. Меру даёт журнал уведомителя: последнее
// событие сессии это запрос разрешения, значит с тех пор ход не кончался.
// Транскрипту тут веры нет, его двигают и сами вставшие в очередь реплики.
func (s *server) chatStuck(sid string) string {
	data, err := os.ReadFile(s.notifyPath())
	if err != nil {
		return ""
	}
	lines := tailLines(data, tailDefault)
	for i := len(lines) - 1; i >= 0; i-- {
		n, ok := parseNotifyLine(lines[i])
		if !ok || len(n.Session) < 8 || !strings.HasPrefix(sid, n.Session) {
			continue
		}
		if n.Reason == "permission_prompt" {
			return "агент ждёт разрешения в своём окне: реплика встала в очередь и хода не даёт"
		}
		return ""
	}
	return ""
}

// handleChatAsk отдаёт вопрос, на котором стоит клиент разговора. Спрашивает
// его панель: пока клиент ждёт ответа, ленты у разговора нет вовсе, и человек
// видел пустоту вместо вопроса, а ответить мог только руками в tmux (замечание
// пользователя: «не хочу каждый раз чинить что-то через тебя»). Снимок панели
// стоит подпроцесса, поэтому ручка своя и зовётся она только там, где ленты
// нет: разбирать панель на каждый список чатов было бы дорого.
func (s *server) handleChatAsk(w http.ResponseWriter, r *http.Request) {
	found, sid, name, ok := s.chatTmuxOf(w, r)
	if !ok {
		return
	}
	ask := tmuxAskOf(name)
	if len(ask.Options) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "tmux": name,
			"note": fmt.Sprintf("клиент %s ни о чём не спрашивает", name)})
		return
	}
	_ = found
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "tmux": name, "ask": ask})
}

// handleChatAskAnswer отвечает клиенту за человека: номер пункта уезжает в его
// панель клавишами. Решение остаётся за человеком, дашборд ничего не
// подтверждает за него и в конфиг подписки не пишет: меняется только место, где
// человек нажимает.
func (s *server) handleChatAskAnswer(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, sid, name, ok := s.chatTmuxOf(w, r)
	if !ok {
		return
	}
	var body struct {
		Option int `json:"option"`
		// Text это свободный ответ: вариант «Type something» открывает у
		// клиента поле, и слова человека едут туда следом за выбором.
		Text string `json:"text"`
		// Step это переход на шаг опроса (счёт с единицы): шаги у клиента табы,
		// и ходить по ним человек вправе свободно, не отвечая на текущий.
		Step int `json:"step"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body)
	}
	ask := tmuxAskOf(name)
	if len(ask.Options) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
			"клиент %s больше ни о чём не спрашивает: отвечать нечего", name)})
		return
	}
	// Переход по табам это не ответ: он ничего не выбирает, а только открывает
	// другой шаг опроса. Ответы при этом копятся у самого клиента.
	if body.Step > 0 {
		if body.Step > len(ask.Steps) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
				"у опроса %d шагов, а выбран %d", len(ask.Steps), body.Step)})
			return
		}
		if err := tmuxStepTo(name, ask, body.Step); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
				"переход по шагам не подался в tmux-сессию %s: %s", name, procErr(err))})
			return
		}
		s.logf("шаг опроса клиента %s в %s: %d (%s)", name, found.Name, body.Step,
			ask.Steps[body.Step-1].Name)
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "tmux": name,
			"step": body.Step, "message": ""})
		return
	}
	if body.Option < 1 || body.Option > len(ask.Options) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
			"у вопроса %d вариантов, а выбран %d", len(ask.Options), body.Option)})
		return
	}
	pick := ask.Options[body.Option-1]
	// Свободный ответ без слов клиенту не нужен: он откроет поле и встанет
	// ждать, а человек будет думать, что ответил.
	if pick.Kind == pickFree && strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
			"вариант «%s» это свободный ответ: без слов отправлять нечего", pick.Text)})
		return
	}
	if pick.Kind != pickFree && strings.TrimSpace(body.Text) != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
			"вариант «%s» слов не ждёт: текст едет только со свободным ответом", pick.Text)})
		return
	}
	if err := tmuxAnswer(name, ask, body.Option, body.Text); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"ответ не подался в tmux-сессию %s: %s", name, procErr(err))})
		return
	}
	said := pick.Text
	if pick.Kind == pickFree {
		said = pick.Text + ": " + truncate(body.Text, 120)
	}
	// Отвеченный последним шаг приводит сводку самого виджета, и второе
	// подтверждение человеку не нужно: последний ответ и есть отправка
	// (замечание пользователя). Проходит её дашборд сам и только когда
	// отвечено всё: со своим предупреждением сводка остаётся на экране, иначе
	// опрос уехал бы неполным.
	if note := s.askPassReview(name); note != "" {
		said += ", " + note
	}
	s.logf("ответ на вопрос клиента %s в %s: пункт %d (%s)", name, found.Name, body.Option, said)
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "tmux": name,
		"option": body.Option, "said": said,
		"message": "ответ отправлен клиенту: " + said})
}

// askPassReview проходит сводку опроса за человека. Пустая строка значит, что
// проходить было нечего: либо сводки нет, либо она предупреждает о
// неотвеченных вопросах, и тогда решать человеку.
func (s *server) askPassReview(name string) string {
	ask := tmuxAskOf(name)
	if ask.Kind != askKindReview || ask.Warn != "" {
		return ""
	}
	at := 0
	for i, opt := range ask.Options {
		if opt.Kind == pickSubmit {
			at = i + 1
			break
		}
	}
	if at == 0 {
		return ""
	}
	if err := tmuxAnswer(name, ask, at, ""); err != nil {
		s.logf("сводка опроса %s не отправилась: %s", name, procErr(err))
		return ""
	}
	s.logf("сводка опроса %s отправлена без второго подтверждения", name)
	return "ответы отправлены"
}

// chatTmuxOf находит tmux-сессию разговора: имя лежит записью реестра, и без
// него ни спросить клиента, ни ответить ему нечем.
func (s *server) chatTmuxOf(w http.ResponseWriter, r *http.Request) (*Project, string, string, bool) {
	found := s.findProject(w, r, "вопрос клиента")
	if found == nil {
		return nil, "", "", false
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return nil, "", "", false
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return nil, "", "", false
	}
	name := sessions.Last(sessions.LoadAll(s.cfg.Home)[sid]).Tmux
	if name == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
			"разговор %s не живёт в нашей tmux: спрашивать его клиента неоткуда", sid)})
		return nil, "", "", false
	}
	return found, sid, name, true
}

// chatRaiseSay поднимает новую сессию репликой человека: разговор в списке
// есть, а сессии за ним нет ни живой, ни мёртвой. Такой чат заводит кнопка «+»
// без задачи, и до этой ветки реплика в нём ложилась в журнал отправленного
// без адресата: агент не поднимался, ответа не приходило, и молчание было
// неотличимо от работы. Дерево тут корень проекта, задача пустая, правило
// плана приезжает заказом, как у любого подъёма.
func (s *server) chatRaiseSay(w http.ResponseWriter, found *Project, sid, text, claim string) {
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	recs := sessions.LoadAll(s.cfg.Home)
	task := ""
	if t := sessions.Touched(recs[sid]); len(t) > 0 {
		task = t[0]
	}
	model := s.chatModel(sid, "")
	sess := chatNewName(task, tmuxAliveFn())
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, From: sid}); err != nil {
		s.logf("настройки чата %s не записались: %v", sess, err)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		chatCmd(chatVars(task, sess), model, "", text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		msg := fmt.Sprintf("tmux не поднял сессию чата %s: %s", sid, procErr(err))
		s.logf("%s", msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	s.chatSayDone(sid, claim, "start")
	s.saidSay(saidSessionKey(sid), text, "start")
	s.logf("чат %s без сессии поднят репликой человека (tmux-сессия %s, модель %s)", sid, sess, model)
	resp := map[string]any{"way": "start", "tmux": sess, "model": model,
		"message": fmt.Sprintf(
			"сессии у чата не было: реплика поднята заказом новой сессии в tmux %s", sess)}
	// Доверие каталогу человек подтверждает клиенту сам, и до тех пор клиент
	// стоит на вопросе, не делая ни хода. Сказать об этом надо сразу, а не
	// оставлять человека перед пустой лентой на минуту: вопрос придёт в панель
	// кнопками, и об этом тут же и написано.
	if note := s.trustNote(s.chatHarnessOf(model), found.Path); note != "" {
		resp["trust"] = note
	}
	writeJSON(w, http.StatusOK, resp)
}

// trustNote отвечает словами, если клиент подписки ещё не знает этого каталога.
// Доверие лежит в конфиге профиля (`~/.claude.json` его дома, поле записи
// проекта), и читается оно тем же разбором, каким его читает выбор каталога для
// снимка квоты. Пусто значит «клиент каталогу доверяет»: молчание тут и
// означает, что подъём пройдёт без вопросов. Ничего в конфиг дашборд при этом
// не пишет: решение остаётся человеку, меняется только место, где он его
// принимает (решение пользователя).
func (s *server) trustNote(h *Harness, dir string) string {
	home := s.cfg.Home
	if h != nil && h.Home != "" {
		home = h.Home
	}
	if quotaTrust(home)[dir] {
		return ""
	}
	who := "клиент"
	if h != nil && h.Name != "" {
		who = "клиент подписки " + h.Name
	}
	return fmt.Sprintf("%s этому каталогу ещё не доверяет (%s): сессия встанет на вопросе о доверии, "+
		"ответить можно прямо тут кнопками, в терминал идти не надо", who, dir)
}

// handleChatModel меняет модель диалога. Смена действует на следующий подъём
// или резюм: у идущего процесса модель уже выбрана его запуском, и подменить
// её со стороны нечем.
func (s *server) handleChatModel(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "модель чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"model\": \"sonnet\"}"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустая модель ничего не значит"})
		return
	}
	was := s.chatModel(sid, "")
	st := s.chatStoreRead(sid)
	st.Model = model
	if err := s.chatStoreWrite(sid, st); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("модель не записалась: %v", err)})
		return
	}
	// След смены в самой ленте: без него человек, вернувшийся к разговору,
	// читает ответы двух разных моделей подряд и не видит, где кончилась одна
	// и началась другая. Разделитель живёт журналом разговора, а не памятью
	// панели: он обязан пережить и перерисовку, и перезагрузку страницы.
	if was != "" && was != model {
		s.saidMark(saidSessionKey(sid), fmt.Sprintf("модель изменена: %s -> %s", was, model))
	}
	s.logf("модель чата %s в %s теперь %s", sid, found.Name, model)
	writeJSON(w, http.StatusOK, map[string]string{"session": sid, "model": model,
		"message": fmt.Sprintf("модель чата теперь %s: она возьмётся на следующем подъёме или резюме сессии", model)})
}

// saidAt это время разговора для списка: метка последней содержательной реплики,
// а когда её не нашлось (в прочитанном хвосте одни ходы инструментов либо
// транскрипт пуст), остаётся время правки файла. Пустая строка в списке была бы
// хуже неточной: разговор без времени уезжает в самый низ и пропадает из виду.
func saidAt(head sessionHead, f sessionInfo) string {
	if head.Said == "" {
		return f.Mtime
	}
	// Метка транскрипта приходит с долями секунды, а время правки файла без
	// них, и список сравнивает их строками: в таком виде «...:55.166Z»
	// оказывалось раньше «...:55Z» той же секунды. Формат тут сводится к
	// одному, секундному UTC, каким его пишет и сам список файлов.
	at, err := time.Parse(time.RFC3339, head.Said)
	if err != nil {
		return f.Mtime
	}
	return at.UTC().Format(time.RFC3339)
}

// sortEntries держит список свежими сверху и при равном времени по ID: порядок
// обязан быть устойчивым, иначе выпадающий список прыгает под пальцем.
func sortEntries(list []chatEntry) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Mtime != list[j].Mtime {
			return list[i].Mtime > list[j].Mtime
		}
		return list[i].ID < list[j].ID
	})
}

// Продолжение работы задачи (замечание 9 второго круга POC). Кнопка
// «Продолжить» на экране задачи поднимала нового агента конвейером, и прежний
// разговор с его контекстом оставался в стороне. Теперь она продолжает
// последнюю сессию задачи: живой её будит канал, кончившейся поднимает резюм, и
// только там, где разговора нет вовсе, заводится новый.

// taskChat находит свежий диалог задачи. Свежесть тут по времени транскрипта:
// список уже отсортирован, и первый совпавший и есть последний разговор.
func (s *server) taskChat(projPath, id string) (chatEntry, bool) {
	for _, e := range s.chatEntries(projPath, chatListLimit) {
		if hasTask(e.Tasks, id) {
			return e, true
		}
	}
	return chatEntry{}, false
}

// continuePrompt это заказ продолжения. Он разговорный: сессия уже знает
// задачу, и пересказывать ей постановку незачем. Правила едут те же, что у
// подъёма, включая ротацию исполнителя: длинный разговор диспетчера идёт
// как раз резюмами, и порог ему нужен не меньше, чем новой сессии.
func continuePrompt(id string, rotate int, sess string) string {
	return "Продолжай работу по " + id + " с того места, где остановился. " +
		planRuleFor(sess) + " " + rotateRule(rotate) + " " + paceRule + " " + channelRule
}

// goalContinuePrompt это вводная продолжения цели: правило про живую сессию у
// цели другое (долгий цикл не подгоняют репликой), а правила заказа те же.
func goalContinuePrompt(id string, rotate int, sess string) string {
	return "Продолжай цель " + id + ". " + planRuleFor(sess) + " " +
		rotateRule(rotate) + " " + paceRule + " " + channelRule
}

func (s *server) handleTaskContinue(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, row, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	// Цель продолжается так же, как задача, только правило про живую сессию у
	// неё другое: диспетчерская сессия цели это долгий цикл, и подгонять его
	// репликой незачем, он и так идёт (замечание про пункт 10 для целей).
	goal := isGoalTitle(row.Title)
	// Заказ собирается по месту: запасной адрес правила плана несёт имя
	// tmux-сессии, а оно в каждой ветке своё (живой чат против резюма).
	prompt := func(sess string) string {
		if goal {
			return goalContinuePrompt(id, s.rotateTokens(), sess)
		}
		return continuePrompt(id, s.rotateTokens(), sess)
	}
	e, has := s.taskChat(found.Path, id)
	if !has {
		// Чата нет ни одного: поднимается новый, с той же репликой. Раньше тут
		// экран откатывался на подъём конвейера, и у цели это был не тот
		// механизм вовсе.
		s.startFresh(w, found, id, prompt(""))
		return
	}
	if e.Sock != "" {
		if goal {
			s.logf("цель %s уже идёт в живом чате %s (pid %d)", id, e.ID, e.PID)
			writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "live",
				"session": e.ID, "where": e.Where,
				"message": "цель " + id + " уже идёт: будить её нечем и незачем"})
			return
		}
		err := peerSay(e.Sock, prompt(e.Tmux))
		if err == nil {
			s.logf("работа %s продолжена в живом чате %s (pid %d)", id, e.ID, e.PID)
			writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "socket",
				"session": e.ID, "where": e.Where})
			return
		}
		if errors.Is(err, errPeerNoAck) {
			// Молчащий канал это клин, а не повод поднимать резюм поверх
			// живого процесса: вторым агентом клин не лечится, лечится он
			// кнопкой на плашке чата.
			s.markDeaf(e.Sock)
			s.logf("клиент чата %s не подтвердил доставку, продолжение %s не поехало: %v", e.ID, id, err)
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": "чат " + e.ID[:8] + " в клине: клиент не подтверждает доставку, снимите его с плашки в панели разговора",
				"stuck": stuckDeafWord})
			return
		}
	}
	sid := e.ID
	info, okS := findSession(s.transcriptRoots(), found.Path, sid)
	if !okS {
		s.startFresh(w, found, id, prompt(""))
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	dir, okTree := sessionTree(found.Path, info.suffix)
	if !okTree {
		dir = found.Path
	}
	model := s.chatModel(sid, e.Tmux)
	sess := chatNewName(id, tmuxAliveFn())
	s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, From: sid})
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, sid, prompt(sess), s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("tmux не поднял продолжение работы %s: %s", id, procErr(err))})
		return
	}
	sessions.Append(sessions.Path(s.cfg.Home),
		sessions.Line(s.now(), sid, sessions.Bind{Task: id, Source: "заказ",
			Project: found.Name, Tree: dir, Tmux: sess}, "продолжение работы"))
	s.logf("работа %s продолжена резюмом чата %s в tmux-сессии %s", id, sid, sess)
	writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "resume",
		"session": sid, "tmux": sess})
}

// Живая работа агента (замечание третьего круга POC). После отправки реплики в
// ленте была тишина до готового ответа: агент думает и зовёт инструменты
// минутами, а панель показывала пустоту, неотличимую от непрошедшей отправки.
// Реестр клиента держит состояние сессии полем status (busy против idle) и
// обновляет его на каждой смене, отсюда индикатор и берёт правду.

func (s *server) handleChatStatus(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "состояние чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	p, ok := s.peers()[sid]
	if !ok {
		// Процесса нет вовсе: работать некому, и это не ошибка, а ответ.
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "live": false, "busy": false})
		return
	}
	// Пустой status это клиент, который его не пишет: занятость тогда неизвестна,
	// и врать про неё нечем. Индикатор в таком случае живёт лентой, а не опросом.
	busy := false
	if info, ok := findSession(s.transcriptRoots(), found.Path, sid); ok {
		busy = s.sessionBusy(info.path, s.now())
	}
	if p.Status == "busy" {
		busy = true
	}
	said := p.Status
	if said == "" {
		said = "по транскрипту"
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "live": true,
		"busy": busy, "status": said, "where": peerWord(p)})
}

// Заголовок диалога (замечание 4 четвёртого круга POC). Первая реплика целиком
// заголовком не годится: «Привет. Ответь одной строкой: как называется этот
// проект? Ничего не делай, только скажи.» растягивалось на весь экран. Порядок
// такой, от дешёвого к дорогому: запись summary самого харнеса (её пишет
// Claude Code и ею же подписывает разговоры список `claude --resume`),
// сохранённый ранее заголовок из ~/.devkit/chats/<sid>.json, дальше эвристика
// первого предложения. Haiku зовётся фоном и только там, где эвристика
// работает плохо, а результат оседает в том же файле навсегда.

// titleWords это потолок заголовка словами: пять-семь слов читаются глазом
// целиком, длиннее уже не заголовок, а сама реплика.
const titleWords = 7

// titleTrim режет реплику до заголовка эвристикой: первое предложение без
// вежливых зачинов и без вопросительного хвоста. Ошибиться тут дёшево, а
// стоит она нисколько.
// Снимается только вежливый зачин: «ответь», «скажи» и родня несут сам заказ,
// и без них заголовок теряет смысл.
var titleDropRe = regexp.MustCompile(`^(?i)(привет|здравствуй\S*|слушай|окей|ок)[,!.\s]+`)

func titleTrim(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	// Первая строка: многострочная реплика это заказ, и заголовок ему даёт
	// первая строка, а не вся простыня.
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	for {
		cut := titleDropRe.ReplaceAllString(t, "")
		if cut == t {
			break
		}
		t = strings.TrimSpace(cut)
	}
	// Первое предложение: дальше идут уточнения вроде «ничего не делай».
	if i := strings.IndexAny(t, ".?!"); i > 0 {
		t = strings.TrimSpace(t[:i])
	}
	words := strings.Fields(t)
	if len(words) > titleWords {
		words = words[:titleWords]
		return strings.Join(words, " ") + "..."
	}
	return strings.Join(words, " ")
}

// titleMark это метка служебного вызова в самом начале промпта. По ней
// транскрипт суммаризации узнаётся в списках и выбрасывается из них: клиент
// пишет журнал всякому вызову, в том числе одноразовому, и без метки эти
// сессии всплывали чатами наравне с разговорами человека (баг девятого круга
// POC). Метка стоит первой строкой, потому что список читает только начало
// первой реплики.
const titleMark = "[devkit-title]"

// titleLegacy это начало промпта, каким он был до метки: уже написанные
// транскрипты узнаются по нему, и старый мусор уходит с экранов сам, без
// удаления файлов.
const titleLegacy = "Назови диалог заголовком"

// probeMark помечает пробный чат, поднятый ради проверки самого дашборда.
// Такие чаты не разговор человека, и в списках им делать нечего ровно по той же
// причине, по которой там нет сессий суммаризации (замечание 20).
const probeMark = "[devkit-probe]"

// probeLegacy это пробы, поднятые до правила про метку: они лежат в дереве
// devkit безметочными и всплывали в списке разговорами вроде «если в твоём
// списке инструментов есть TodoWrite, ответь ровно...». Список короткий и
// закрытый: он про уже написанные транскрипты, а новые пробы зовутся с меткой.
var probeLegacy = []string{
	"если в твоём списке инструментов есть todowrite",
	"если у тебя есть инструмент todowrite",
	"ответь одним словом: ок",
	"запусти в bash команду: sleep 300",
}

// taskChats отвечает, у каких задач на этой машине есть исполнительские сессии,
// живые или кончившиеся. По нему строка In progress и решает, наша это работа
// или её взяли в другом месте. Исполнительской считается сессия, поднятая
// кнопкой запуска или продолжения, конвейером и сессия в дереве задачи.
// Разговорные чаты строку не присваивают: груминг, привязка рукой и разговор о
// задаче это чтение и обсуждение, а не работа над ней, и запускать по ним
// нечего (замечание пользователя про DK-460).
func (s *server) taskChats(projPath string) map[string]string {
	out := map[string]string{}
	binds := s.binds()
	view := s.harnesses()
	for _, f := range sessionFiles(s.transcriptRoots(), projPath) {
		head := s.sessionHeadCached(f.path, f.stamp)
		// Груминг черновика и служебная сессия заголовка приезжают тем же
		// заказом дашборда, и по полям реестра от запуска задачи они
		// неотличимы: разводит их первая реплика.
		if strings.HasPrefix(head.First, groomOrderPrefix) || titleSession(head.First) {
			continue
		}
		task, note, bound := bindTask(binds, f.ID, f.suffix, head)
		if task == "" || bound != boundLead || note == handNote {
			continue
		}
		// Разговорный чат задачу не ведёт: подписка Check берётся у той сессии,
		// на которой работу начинали, а строка от чата своей не становится.
		if !leadsTask(binds[f.ID].Tmux, f.suffix, note) {
			continue
		}
		// Подписка задачи это подписка её исполнительской сессии: та, на которой
		// работу начали, ею же её и закрывают. Корень транскрипта называет её
		// сам, отдельной записи для этого не заводится.
		if out[task] == "" {
			out[task] = harnessOfRoot(view, s.cfg.Home, f.root)
		}
	}
	return out
}

// titleSession узнаёт служебную сессию по первой реплике.
func titleSession(first string) bool {
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, titleMark) || strings.HasPrefix(first, titleLegacy) ||
		strings.HasPrefix(first, probeMark) {
		return true
	}
	low := strings.ToLower(first)
	for _, mark := range probeLegacy {
		if strings.HasPrefix(low, mark) {
			return true
		}
	}
	return false
}

// titleDir это рабочая директория служебного вызова: каталог вне всех проектов,
// чтобы транскрипт лёг в свой угол и не попал ни в один список. Не создался,
// значит вызов пойдёт из директории процесса, и его подберёт фильтр по метке.
func (s *server) titleDir() string {
	dir := filepath.Join(s.cfg.Home, ".devkit", "titles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// titleAsk просит haiku назвать чат. Модель тут самая дешёвая нарочно:
// заголовок это украшение списка, и платить за него ярусом выше некому.
func (s *server) titleAsk(text string) string {
	if m := clientMissing(defaultClient); m != "" {
		return ""
	}
	prompt := titleMark + " Назови диалог заголовком в 5-7 слов по первой реплике человека. " +
		"Ответь только заголовком, без кавычек и пояснений. Реплика: " + truncate(text, 600)
	// Вызов служебный: заголовок это украшение списка, а не работа человека.
	// Хуки devkit на нём молчат по метке окружения, а транскрипт уезжает в свой
	// каталог вне проектов, чтобы не всплыть чатом (баг девятого круга POC).
	out, err := runProcQuiet(s.titleDir(), true, defaultClient, "-p", "--model", "haiku", prompt)
	if err != nil {
		return ""
	}
	said := strings.TrimSpace(string(out))
	if titleJunk(said) {
		return ""
	}
	return titleTrim(said)
}

// titleJunk узнаёт служебный ответ вместо заголовка. Клиент отвечает своим
// текстом и на отказ хука, и на несостоявшийся логин, а заголовок из такого
// ответа оседал в кеше навсегда и вставал в шапку чата («UserPromptSubmit
// operation blocked by hook»). Признаки грубые нарочно: заголовок это пять-семь
// слов одной строкой, и всё, что на него не похоже, лучше выбросить, оставшись
// с эвристикой.
func titleJunk(said string) bool {
	if said == "" {
		return true
	}
	if strings.Contains(said, "\n") {
		return true
	}
	low := strings.ToLower(said)
	for _, mark := range []string{
		"blocked by hook", "operation blocked", "not logged in", "please run /login",
		"userpromptsubmit", "pretooluse", "posttooluse", "invalid api key",
		"execution error", "traceback", "no such file",
	} {
		if strings.Contains(low, mark) {
			return true
		}
	}
	// Заголовок длиннее двух строк текста это уже не заголовок, а рассказ.
	return len([]rune(said)) > 200
}

// titleJobs держит счёт идущих суммаризаций: заголовок нужен списку, а не
// человеку прямо сейчас, и очередь на восемьдесят транскриптов сожгла бы
// квоту на украшение.
var titleJobs = make(chan struct{}, 1)

// titleAskLimit это сколько заголовков заказывается за один заход списка.
// Список открывают часто, и за несколько заходов свежие разговоры обрастают
// заголовками сами, без единого ожидания на экране.
const titleAskLimit = 2

// titleFor это одна лестница заголовка разговора на всех потребителей: список
// диалогов, раздел «Агенты», всякий следующий. Порядок от дешёвого к дорогому:
// summary самого харнеса, сохранённый заголовок, эвристика первого предложения
// на месте. Haiku зовётся фоном и правит эвристику к следующему заходу; ask
// говорит, можно ли его заказывать, потому что счёт заказов держит вызывающий.
// Второй такой лестницы заводить нельзя: разойдясь, они дали бы одному
// разговору два разных имени на соседних экранах (замечание 1 восьмого круга).
// Второй ответ говорит, ушёл ли заказ haiku: счёт заказов держит вызывающий, а
// знает про заказ только эта лестница.
func (s *server) titleFor(sid, summary, first string, ask bool) (string, bool) {
	if summary != "" {
		return summary, false
	}
	if sid != "" && chatKeyRe.MatchString(sid) {
		if st := s.chatStoreRead(sid); st.Title != "" {
			return st.Title, false
		}
	}
	said := titleTrim(first)
	if ask && first != "" && sid != "" && chatKeyRe.MatchString(sid) {
		s.titleOrder(sid, first)
		return said, true
	}
	return said, false
}

// titleOrder заказывает заголовок фоном. Заказ идёт по одному на машину:
// параллельные вызовы клиента стоят дороже, чем ожидание заголовка до
// следующего открытия экрана.
func (s *server) titleOrder(sid, text string) {
	go func() {
		select {
		case titleJobs <- struct{}{}:
		default:
			return
		}
		defer func() { <-titleJobs }()
		said := s.titleAsk(text)
		if said == "" {
			return
		}
		cur := s.chatStoreRead(sid)
		cur.Title = said
		s.chatStoreWrite(sid, cur)
		s.logf("заголовок чата %s назван haiku: %s", sid, said)
	}()
}

// titleFill дописывает заголовки списку диалогов той же лестницей. Счёт заказов
// держится тут: список приходит на восемьдесят транскриптов, и заказывать
// заголовок каждому значило бы сжечь квоту на украшение.
func (s *server) titleFill(list []chatEntry) {
	asked := 0
	for i := range list {
		e := &list[i]
		said, ordered := s.titleFor(e.ID, e.Summary, e.Title, asked < titleAskLimit)
		e.Title = said
		if ordered {
			asked++
		}
	}
}

// Вставка картинки в чат (замечание 4 двенадцатого круга POC). Бинарной
// передачи через сокет тут не заводится нарочно: канал живых сессий носит
// текст, а картинку агент читает сам, своим Read. Дашборд кладёт файл в свой
// каталог и дописывает к реплике ссылку на путь, то есть делает ровно то же,
// что человек, перетащивший файл в окно клиента.

// shotDir это каталог вложений чата: свой на сессию, чтобы файлы не смешивались
// и чтобы чат можно было вычистить целиком.
func (s *server) shotDir(sid string) string {
	return filepath.Join(s.cfg.Home, ".devkit", "uploads", sid)
}

// shotLimit держит вложение в берегах: скриншот экрана это единицы мегабайт, а
// всё, что больше, в реплику по ошибке.
const shotLimit = 12 << 20

var shotKind = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

func (s *server) handleChatShot(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "вложение чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Data string `json:"data"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, shotLimit+(shotLimit/3))).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON {\"kind\": \"image/png\", \"data\": \"<base64>\"}"})
		return
	}
	ext, ok := shotKind[strings.TrimSpace(body.Kind)]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("вид %q не картинка: беру png, jpeg, gif, webp", body.Kind)})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "данные не разобрались как base64"})
		return
	}
	if len(raw) == 0 || len(raw) > shotLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("картинка пустая или длиннее предела %d МБ", shotLimit>>20)})
		return
	}
	dir := s.shotDir(sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("каталог вложений не создался: %v", err)})
		return
	}
	name := fmt.Sprintf("%s%s", s.now().Format("20060102T150405"), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("вложение не записалось: %v", err)})
		return
	}
	s.logf("вложение чата %s легло в %s (%d КБ)", sid, path, len(raw)/1024)
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "name": name, "bytes": len(raw)})
}

// handleChatShotGet отдаёт вложение чата картинкой: лента показывает миниатюру,
// а браузеру файл с диска иначе не достать. Путь проверяется по каталогу
// вложений, чтобы ручка не превратилась в чтение произвольного файла.
func (s *server) handleChatShotGet(w http.ResponseWriter, r *http.Request) {
	if s.findProject(w, r, "картинка чата") == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "битый id чата"})
		return
	}
	name := filepath.Base(r.URL.Query().Get("name"))
	if name == "." || name == string(filepath.Separator) || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "битое имя вложения"})
		return
	}
	path := filepath.Join(s.shotDir(sid), name)
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вложения нет"})
		return
	}
	http.ServeFile(w, r, path)
}

// startFresh поднимает новый чат работы и отвечает тем же телом, что и
// продолжение: экрану всё равно, продолжили ему сессию или завели первую, ему
// нужен адрес, куда идти смотреть.
func (s *server) startFresh(w http.ResponseWriter, found *Project, id, text string) {
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	dir := found.Path
	if tree := filepath.Join(filepath.Dir(found.Path), filepath.Base(found.Path)+"-"+strings.ToLower(id)); isDir(tree) {
		dir = tree
	}
	model := chatModelDefault
	sess := chatNewName(id, tmuxAliveFn())
	s.chatStoreWrite("tmux-"+sess, chatStore{Model: model})
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, "", text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("tmux не поднял новый чат %s: %s", id, procErr(err))})
		return
	}
	s.logf("работа %s поднята новым чатом в tmux-сессии %s", id, sess)
	writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "fresh", "tmux": sess,
		"message": "чата не было: поднят новый в tmux-сессии " + sess})
}

// Привязка по факту работы. Сессия, у которой задачи в реестре нет, всё равно
// бывает исполнительской: человек открыл окно, сказал «test» и работает по
// строке, а привязке взяться неоткуда. Прежде такая строка объявлялась взятой
// на другой машине, и это было прямое враньё: отсутствие привязанной сессии не
// доказывает чужую машину (жалоба пользователя на DK-481).
//
// Порог держится строгим нарочно: ложная привязка хуже отсутствия, она отдаёт
// строку не тому. Считается только явная работа по задаче, а не случайное
// упоминание ID в разговоре.
const (
	// Сколько реплик хвоста смотрим: работа по задаче видна последними ходами,
	// а глубже начинается прошлое сессии.
	tailWorkReplies = 60
	// Сколько раз ID должен прозвучать в репликах, чтобы считаться работой.
	// Один раз это вопрос «а что там с DK-481», два и больше это работа.
	tailWorkSaid = 2
)

// gitSubjectRe ловит subject коммита в команде: ID в нём это работа по задаче,
// а не разговор о ней.
var gitSubjectRe = regexp.MustCompile(`(?s)git\s+commit[^\n]*?-m\s*['"]([^'"]*)['"]`)

// worksTask отвечает, работает ли сессия по этой задаче, судя по хвосту её
// транскрипта. Доводов два: ID в subject коммита (сильный, хватает одного) и ID
// в репликах разговора (слабый, нужен не один раз).
func worksTask(list []reply, id string) bool {
	said := 0
	for _, r := range list {
		text := r.Text
		if r.Args != nil && r.Args["command"] != "" {
			text += "\n" + r.Args["command"]
		}
		if r.Role == "tool" {
			// Ход инструмента: интересует только коммит, остальные команды
			// упоминают ID мимоходом (открыть файл задачи, показать строку).
			for _, m := range gitSubjectRe.FindAllStringSubmatch(text, -1) {
				if strings.Contains(m[1], id) {
					return true
				}
			}
			continue
		}
		if r.Role != "user" && r.Role != "assistant" {
			continue
		}
		if strings.Contains(text, id) {
			said++
			if said >= tailWorkSaid {
				return true
			}
		}
	}
	return false
}

// taskWorkers ищет исполнителей среди сессий без привязки: живых, этого
// проекта, у которых своей задачи нет вовсе. Спрашивается только про
// названные задачи (те, которых иначе объявят чужими), и только по хвосту
// транскрипта: полный скан тут не нужен.
func (s *server) taskWorkers(projPath string, want []string) map[string]string {
	out := map[string]string{}
	if len(want) == 0 {
		return out
	}
	binds := s.binds()
	live := s.peers()
	for _, f := range sessionFiles(s.transcriptRoots(), projPath) {
		if _, alive := live[f.ID]; !alive {
			continue
		}
		if task, _, bound := bindTask(binds, f.ID, f.suffix, s.sessionHeadCached(f.path, f.stamp)); task != "" && bound == boundLead {
			continue
		}
		list := tailReplies(f.path, tailWorkReplies)
		for _, id := range want {
			if out[id] != "" {
				continue
			}
			if worksTask(list, id) {
				out[id] = f.ID
				s.logf("задача %s: исполнителем считается сессия %s, привязки у неё нет, "+
					"а хвост транскрипта работает этим ID", id, f.ID)
			}
		}
	}
	return out
}
