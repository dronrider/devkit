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
	"strconv"
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

// chatModelNote объясняет пустой выбор моделей. Случая два, и чинятся они
// по-разному: agentctl не позвался вовсе либо позвался и отдал подписки без
// маппинга ярусов. Первый случай уже назван плашкой подписок, и слова берутся
// оттуда же, чтобы экран не говорил о нём двумя голосами.
func (s *server) chatModelNote() string {
	if note := s.harnesses().Note; note != "" {
		return note
	}
	return "лестница ярусов пуста: agentctl harness --json не назвал ни одной модели, " +
		"выбирать не из чего. Ярусы прописываются в машинном слое харнесов."
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
	// chatNotStarted это разговор, заведённый кнопкой «+», в котором ещё не
	// сказано ни слова: сессии за ним нет, и поднимет её первая реплика.
	chatNotStarted = "blank"
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
	// Archived это убранный в архив разговор: список его прячет, пока человек
	// не попросит показать архивные, а сам признак живёт памятью диалога.
	Archived bool `json:"archived,omitempty"`
	// Parent называет разговор, раздавший эту работу: реестр машины пишет его
	// подпроцессу делегирования (DK-581). Непустое поле значит, что строка это
	// не разговор человека, а чужая работа, и списку она не нужна: ходы её
	// видны в ленте родителя. Строку при этом собирают как всякую другую, тем
	// же перечнем меряют живую работу задачи и считают агентов кольцом.
	Parent string `json:"parent,omitempty"`
	// Blank это строка незачатого разговора: человек завёл его кнопкой «+», а
	// сессии за ним ещё нет. Транскрипта у такой строки нет, и приходит она не
	// из обхода каталогов, а из памяти диалогов.
	Blank bool `json:"blank,omitempty"`
	// Grown называет сессию, выросшую из незачатой записи: панель, стоящая на
	// её адресе, переезжает по нему на живой разговор. Строкой списка такая
	// запись больше не показывается, разговор в нём уже есть своей строкой.
	Grown string `json:"grown,omitempty"`
	// Draft это набранная в незачатом разговоре реплика: держать её негде,
	// кроме памяти диалога, и панель забирает её отсюда.
	Draft string `json:"draft,omitempty"`
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
	// Login называет словами разлогиненный разговор: клиент жив, отвечает и
	// выглядит рабочим, а на деле просит /login и не делает ни хода. От клина
	// это отличается тем, что перезапуск сам по себе тут не лечит: сперва
	// человеку надо войти на машине, и потому состояние идёт своим полем, а не
	// четвёртым родом Stuck. Пусто у разговора, чей последний ответ настоящий.
	Login string `json:"login,omitempty"`
	// Gone называет словами снятый и пересозданный разговор: имя его
	// tmux-сессии реестр отдал другому разговору, то есть работу подняли
	// заново, а этот разговор кончился. Писать в него нечего, и панель обязана
	// сказать это словами: молчание тут неотличимо от доставки, и ровно им
	// кончился живой случай, где реплика уехала посторонней сессии.
	// GoneTo это тот разговор, что занял имя: он и есть понятный выход.
	Gone   string `json:"gone,omitempty"`
	GoneTo string `json:"goneTo,omitempty"`
	// Heal говорит, что признак клина твёрдый и разговор лечится сам. Твёрдых
	// родов два, пропавший терминал и молчащий канал, и оба меряются стоящим
	// транскриptom. Третий род это вопрос клиента в терминале, там человек
	// нужен живьём, и трогать процесс нельзя. Пусто у всего, в чём есть
	// сомнение: снятие процесса необратимо, и по догадке его не делают.
	Heal bool `json:"heal,omitempty"`
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

// loginGoneWord это слова состояния «нужен вход». Клиент с истёкшим
// OAuth-токеном не работает вовсе: на любую реплику он отвечает служебной
// строкой «Login expired. Please run /login» и ждёт входа, которого из
// дашборда не сделать. Прежде про такой ответ знал один titleJunk, и то лишь
// затем, чтобы не пустить его в заголовок: на экране разговор выглядел живым,
// а отказ стоял обычным пузырём в ленте (живой случай chat-DK-397-1 и
// chat-DK-397-2, два дня подряд).
// Слова короткие нарочно: место в строке списка узкое, а подробности человек
// читает в самой ленте. «Сессия разлогинена» тут говорило про наше устройство,
// и его же приходилось объяснять целым абзацем на плашке.
const loginGoneWord = "нужен вход"

// loginGoneFix говорит, что делать. Порядок тут обязателен, и обратный не
// работает: свежий токен из связки ключей достаётся новому процессу, а живой
// клиент держит свой в памяти и после чужого входа сам его не перечитывает.
// Значит сперва вход на машине, потом перезапуск.
const loginGoneFix = "Зайдите на машину, выполните /login в терминале, " +
	"а потом перезапустите разговор кнопкой: живой процесс держит старый токен " +
	"в памяти и после входа сам его не перечитывает."

// loginGone узнаёт служебный ответ клиента про истёкший логин. Мера тут грубая
// нарочно, как у titleJunk: своих кодов отказа клиент наружу не отдаёт, и
// единственный след истёкшего входа это его же строка в транскрипте. Длина
// держит границу с обычным ответом: служебная строка коротка, а рассказ агента
// про чужой разлогин это рассказ, а не отказ.
func loginGone(text string) bool {
	said := strings.TrimSpace(text)
	if said == "" || len([]rune(said)) > loginGoneLimit {
		return false
	}
	low := strings.ToLower(said)
	for _, mark := range []string{
		"login expired", "please run /login", "not logged in",
		"oauth token expired", "oauth token has expired", "session expired",
	} {
		if strings.Contains(low, mark) {
			return true
		}
	}
	return false
}

// loginGoneLimit это потолок длины служебной строки. Настоящий отказ клиента
// это одно предложение, а двести знаков тут с запасом на обёртку харнеса.
const loginGoneLimit = 200

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

// askSeenTTL держит память снимка виджета. Снимок панели стоит подпроцесса, а
// спрашивает его теперь не только открытая панель, но и список чатов со
// строкой доски: без памяти каждая сборка списка гоняла бы capture-pane по
// всем живым сессиям машины. Вопрос не рассасывается сам за секунды, и десяти
// секунд тут с запасом.
const askSeenTTL = 10 * time.Second

// askEntry это разобранный виджет сессии и момент, когда его сняли.
type askEntry struct {
	ask  tmuxAsk
	born time.Time
}

// tmuxAskOfFn это шов для тестов: боевой сервер снимает настоящую панель,
// тест подставляет свой снимок и tmux машины не трогает.
var tmuxAskOfFn = tmuxAskOf

// tmuxAsking отвечает, на каком вопросе стоит клиент сессии. Ответ помнится
// askSeenTTL: спрашивают его и список чатов, и строка доски, а стоит он
// подпроцесса на каждую живую сессию.
func (s *server) tmuxAsking(name string) tmuxAsk {
	if name == "" || tmuxMissingCheck() != "" {
		return tmuxAsk{}
	}
	now := s.now()
	s.mu.Lock()
	e, hit := s.askSeen[name]
	s.mu.Unlock()
	if hit && now.Sub(e.born) < askSeenTTL {
		return e.ask
	}
	ask := tmuxAskOfFn(name)
	s.mu.Lock()
	if s.askSeen == nil {
		s.askSeen = map[string]askEntry{}
	}
	s.askSeen[name] = askEntry{ask: ask, born: now}
	s.mu.Unlock()
	return ask
}

// healWindow это окно памяти о самолечении. Клин, случившийся позже него, это
// новая беда, и лечится она заново. Порог клина тут один на всё
// (stuckLostTerm): раньше него твёрдого признака не бывает вовсе, потому что
// свежий транскрипт снимает вопрос без зонда.
const healWindow = 10 * stuckLostTerm

// Слова, которыми лечение оседает в ленте разговора. Записью-пометкой, как
// разделитель смены модели: это не тревога, а строка о том, что случилось.
const (
	healSaidWord  = "разговор перезапущен, продолжаю"
	healFailWord  = "разговор перезапустить не вышло, продолжения не будет"
	healAgainWord = "разговор завис снова сразу после перезапуска: больше не перезапускаю, посмотрите терминал"
)

// healEntry это память об одном вылеченном разговоре: когда лечили и сказали ли
// уже про повтор.
type healEntry struct {
	at    time.Time
	again bool
}

// healClaim решает, лечить ли клин этого разговора. Первый раз лечим, второй
// подряд нет: снятие процесса необратимо, и перезапуск по кругу хуже самого
// клина. Второй ответ это причина отказа, пустая у согласия. Третий говорит,
// что про повтор надо сказать в ленте, и говорится это один раз.
func (s *server) healClaim(sid string) (bool, string, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	e, hit := s.heal[sid]
	if !hit || now.Sub(e.at) > healWindow {
		s.heal[sid] = healEntry{at: now}
		return true, "", false
	}
	// Соседняя вкладка заявила лечение секунду назад: это то же самое лечение,
	// а не повторный клин. Молчим и не трогаем ничего.
	if now.Sub(e.at) < stuckLostTerm {
		return false, "лечение этого разговора уже идёт", false
	}
	say := !e.again
	e.again = true
	s.heal[sid] = e
	return false, healAgainWord, say
}

// handleChatHeal это заявка на самолечение клина и отчёт о нём. Пустое тело
// значит заявку: сервер сверяет твёрдый признак клина, помнит перезапуск и
// отвечает согласием либо причиной отказа. Тело с полем done значит отчёт:
// лечение кончилось, и в ленту ложится строка о том, чем именно.
func (s *server) handleChatHeal(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "чат")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	var body struct {
		Done *bool `json:"done"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body)
	if body.Done != nil {
		word := healSaidWord
		if !*body.Done {
			word = healFailWord
		}
		s.saidMark(saidSessionKey(sid), word)
		s.logf("самолечение чата %s кончилось: %s", sid, word)
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "message": word})
		return
	}
	// Признак клина спрашивается заново, а не берётся со слов клиента: снятие
	// процесса необратимо, и заявка обязана опираться на то, что сервер видит
	// сам.
	if !s.chatHealable(found.Path, sid) {
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "claim": false,
			"why": "твёрдого признака клина у разговора нет: не трогаю"})
		return
	}
	ok, why, say := s.healClaim(sid)
	if say {
		s.saidMark(saidSessionKey(sid), healAgainWord)
	}
	if !ok {
		s.logf("самолечение чата %s отклонено: %s", sid, why)
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "claim": false, "why": why})
		return
	}
	s.logf("самолечение чата %s разрешено: клин твёрдый, перезапускаю", sid)
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "claim": true})
}

// chatHealable отвечает, стоит ли этот разговор в твёрдом клине прямо сейчас.
func (s *server) chatHealable(projPath, sid string) bool {
	for _, e := range s.chatEntries(projPath, chatListLimit) {
		if e.ID == sid {
			return e.Heal
		}
	}
	return false
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
	// Seen это момент, когда человек последний раз смотрел разговор, в
	// unix-секундах. Ставит её чтение ленты панелью: лента читается ровно
	// тогда, когда разговор открыт у человека на экране. По ней автоматика и
	// отличает непрочитанный ответ от прочитанного, своей отметки прочтения у
	// нас нет.
	Seen int64 `json:"seen,omitempty"`
	// Archived это уборка разговора рукой: отработавший чат мозолит глаза в
	// списке, а окно по свежести его не прячет, он свежий (замечание
	// пользователя про десяток чатов после разбора накопителя). Признак лежит
	// на сервере, а не во вкладке: он переживает перезапуск дашборда и виден с
	// любого экрана. Дорога назад та же: снятый признак возвращает строку.
	Archived bool `json:"archived,omitempty"`
	// From называет диалог, продолжением которого поднят этот: `claude --resume`
	// заводит новую сессию со своим транскриптом, и без этой ссылки история
	// разговора рвалась бы на две строки списка.
	From string `json:"from,omitempty"`
	// Blank это разговор, заведённый кнопкой «+» и ещё не начатый. Сессии за
	// ним нет вовсе: поднимать клиента впустую дорого, и поднимет её первая
	// реплика человека. До правки такого разговора не существовало нигде,
	// кроме адресной строки вкладки, и человек не мог ни увидеть его в списке,
	// ни завести рядом второй (жалоба пользователя).
	Blank bool `json:"blank,omitempty"`
	// Born это момент заведения записи в unix-секундах. По нему запись стоит в
	// списке (реплик у неё нет, и мерить её нечем) и по нему же её метёт
	// уборка брошенных.
	Born int64 `json:"born,omitempty"`
	// Task называет задачу незачатого разговора: реплика поднимет сессию в её
	// дереве, а список покажет строку под фильтром задачи. У начатого разговора
	// привязку говорит реестр сессий, и это поле ему не нужно.
	Task string `json:"task,omitempty"`
	// Project это проект, в котором запись завели: список общий по машине, и
	// без имени проекта строку не открыть.
	Project string `json:"project,omitempty"`
	// Tmux это имя tmux-сессии, поднятой первой репликой. Ставит его подъём, а
	// живёт оно до тех пор, пока сессия не назовётся в реестре: панель по нему
	// узнаёт свой разговор и после перезагрузки вкладки.
	Tmux string `json:"tmux,omitempty"`
	// Grown называет сессию, выросшую из записи. С этого момента разговор
	// живёт своим транскриптом, а запись остаётся дорожным знаком для панели,
	// стоящей на старом адресе, и метётся уборкой.
	Grown string `json:"grown,omitempty"`
	// Draft это набранная, но не отправленная реплика. У начатого разговора
	// черновик держит вкладка, а у незачатого держать его негде: транскрипта
	// нет, и с чужого экрана такой разговор выглядел бы пустым. Он же говорит
	// уборке, что запись человеку нужна.
	Draft string `json:"draft,omitempty"`
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

// chatSeenStep это шаг отметки показа: панель перечитывает ленту каждые
// несколько секунд, и писать файл на каждый заход незачем.
const chatSeenStep = 30 * time.Second

// chatSeenMark отмечает, что разговор сейчас смотрят. Зовёт её чтение ленты, и
// частые заходы отсекаются шагом: точность тут нужна до минут, а не до секунд.
func (s *server) chatSeenMark(sid string) {
	if sid == "" || !chatKeyRe.MatchString(sid) {
		return
	}
	now := s.now()
	st := s.chatStoreRead(sid)
	if st.Seen > 0 && now.Sub(time.Unix(st.Seen, 0)) < chatSeenStep {
		return
	}
	st.Seen = now.Unix()
	s.chatStoreWrite(sid, st)
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
	list, _ := s.chatEntriesFrom(files, limit, chatWindow{})
	return list
}

// chatAllLimit это потолок общего списка машины: он выше проектного, потому
// что накрывает все доски разом, но остаётся потолком, чтобы обход не
// перечитывал транскрипты машины целиком.
const chatAllLimit = 160

// chatWindowDays это окно списка по умолчанию: разговоры последних трёх суток
// плюс живые любого возраста. Транскрипты с машины не исчезают, и общий список
// копится сам собой (на живой машине сто сорок пять строк при сорока одной
// живой), а человек ходит в него за сегодняшним разговором. Остальное
// достаётся кнопкой «показать раньше» и поиском, который окна не знает вовсе.
// Порог назван тут одним местом: и ручка, и её отказ считают от него.
const chatWindowDays = 3

// chatBlank отвечает, что показывать в разговоре нечего: ни одной
// содержательной реплики, ни заголовка от харнеса. Так выглядит сессия,
// которая поднялась и умерла, служебный подъём клиента и всякий транскрипт,
// где до слов дело не дошло. Разбор тут общий с лентой и своего чтения не
// заводит: Said это метка последней реплики, которую лента рисует пузырём
// (saidReply поверх parseReplies, где служебные вставки уже вырезаны
// splitService), а First это первая реплика человека без служебных обёрток.
// Шапка на эту строку уже прочитана, и отсев не стоит ни одного лишнего
// открытия файла.
func chatBlank(head sessionHead) bool {
	return head.Said == "" && strings.TrimSpace(head.First) == "" &&
		strings.TrimSpace(head.Summary) == ""
}

// chatFresh отвечает, попадает ли разговор в окно списка. Нулевой рубеж это
// «окна нет»: так список отдаётся поиску и кнопке «показать раньше».
// Неразобранная метка оставляет строку на месте: спрятать разговор из-за
// формата времени хуже, чем показать лишнюю строку.
func chatFresh(mtime string, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	at, err := time.Parse(time.RFC3339, mtime)
	if err != nil {
		return true
	}
	return !at.Before(since)
}

// chatEntriesAll собирает диалоги всех проектов машины в один список:
// переключение на чужой разговор не должно требовать смены проекта доски, а
// принадлежность строки называет поле Project. Шапки читаются только у файлов
// над общим потолком, свежие сверху, и лежат в той же памяти процесса, что и у
// проектного списка.
func (s *server) chatEntriesAll(limit int, win chatWindow) ([]chatEntry, bool) {
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
	return s.chatEntriesFrom(files, limit, win)
}

// chatWindow это окно общего списка: рубеж свежести и разговоры, которые
// остаются в списке любого возраста, потому что панель стоит на них прямо
// сейчас. Нулевой рубеж значит «весь список»: так за ним ходят поиск и кнопка
// «показать раньше».
type chatWindow struct {
	since time.Time
	keep  map[string]bool
}

// chatEntriesFrom строит строки списка по готовому набору файлов. Общее
// хозяйство захода (реестр записей, свёртка имён tmux, живые сессии, имена
// подписок) считается один раз, сколько бы проектов ни легло в обход, а
// префикс доски берётся по проекту файла и помнится на заход.
func (s *server) chatEntriesFrom(files []chatFile, limit int, win chatWindow) ([]chatEntry, bool) {
	recs := s.bindsAll()
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
	// Живость файла до чтения шапки: окно пропускает живой разговор любого
	// возраста, и решать это надо там, где транскрипт ещё не открывался. Мера
	// та же, что у состояния строки ниже: запись в реестре клиента либо своё
	// имя tmux, которого не занял разговор свежее.
	liveFile := func(f chatFile) bool {
		if _, ok := live[f.ID]; ok {
			return true
		}
		name := sessions.Last(recs[f.ID]).Tmux
		return name != "" && tmuxClaim[name] == f.ID && alive(name)
	}
	out := []chatEntry{}
	older := false
	read := 0
	for _, f := range files {
		if limit > 0 && read >= limit {
			older = true
			break
		}
		// Окно списка стоит перед чтением шапки, и в этом вся его цена: старый
		// разговор не стоит ни одного открытия транскрипта. Время правки файла
		// тут только сито, оно не раньше времени последней реплики, а точный
		// отбор идёт ниже, по времени самого разговора.
		if !win.since.IsZero() && !win.keep[f.ID] && f.mod.Before(win.since) && !liveFile(f) {
			older = true
			continue
		}
		read++
		prefix := prefixOf(f.projPath)
		head := s.sessionHeadCached(f.path, f.stamp)
		// Служебная сессия суммаризации чатом не является: её завёл сам
		// дашборд ради заголовка, и в списке ей делать нечего.
		store := s.chatStoreRead(f.ID)
		if titleSession(head.First) || store.Hidden {
			continue
		}
		// Разговор, в котором никто ничего не сказал, в списке не строка, а
		// помеха: он поднялся и умер, и открывать в нём нечего.
		if chatBlank(head) {
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
			Harness:  names[f.root],
			Model:    s.chatModel(f.ID, last.Tmux),
			Archived: store.Archived,
			Parent:   last.Parent,
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
		// Разлогин виден по последнему ответу агента в транскрипте: клиент с
		// истёкшим токеном отвечает служебной строкой на любую реплику, и
		// никакого другого следа наружу не отдаёт. Признак не зависит от
		// живости: разговор бывает разлогинен и с живым процессом, и без него,
		// а лечится он одинаково, входом на машине и перезапуском.
		if head.Bye {
			e.Login = loginGoneWord
		}
		// Клин ищется там же, где меряется состояние: у клина все признаки
		// живого разговора, и без отдельной проверки он им и остаётся. Родов
		// два: пропавший терминал и мёртвый событийный цикл при живом pty,
		// второй ловится зондом канала по стоящему транскрипту.
		if lostTerminal(e.PID, e.Tmux, alive, f.mod, s.now()) {
			e.Stuck = stuckLostTermWord
			e.Heal = true
		} else if e.Sock != "" && s.peerDeaf(e.Sock, f.mod) {
			e.Stuck = stuckDeafWord
			e.Heal = true
		} else if (e.Sock != "" || (e.Tmux != "" && alive(e.Tmux))) && s.chatStuck(f.ID) != "" {
			// Третий род: живой клиент стоит на вопросе в своём терминале
			// (разрешение, доверие каталогу первого запуска). Мера та же, что
			// у ответа на реплику: последняя запись сессии в журнале
			// уведомителя это permission_prompt.
			e.Stuck = stuckAskWord
		} else if e.Tmux != "" && alive(e.Tmux) && len(s.tmuxAsking(e.Tmux).Options) > 0 {
			// Тот же третий род, но по самой панели, а не по журналу
			// уведомителя. Журнал тут молчит вовсе там, где больнее всего: на
			// вопросе доверия каталогу сессия ещё не родилась, своего ID у неё
			// нет, и хук уведомителя не сработал ни разу. Ровно так встали два
			// чата xr-proxy, и в списке они выглядели живыми (замечание
			// пользователя 2026-08-28).
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
		// Точный рубеж окна считается по времени разговора: файл трогает
		// служебщина, и по времени правки в сегодняшний список пролезала бы
		// беседа месячной давности. Живой разговор остаётся любого возраста,
		// к нему идут отвечать.
		if e.State != chatLive && !win.keep[f.ID] && !chatFresh(e.Mtime, win.since) {
			older = true
			continue
		}
		out = append(out, e)
	}
	// Порядок списка это порядок разговора, а не порядок касаний файла: пока
	// сортировки тут не было, список стоял так, как его отдал обход каталога, то
	// есть по времени правки транскриптов (замечание пользователя).
	sortEntries(out)
	return out, older
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

// withoutHandedOut убирает строки розданной работы: у сессии, поднятой из
// другого разговора, реестр называет родителя. Работа, поднятая сама по себе
// (руками, скриптом, мимо agentctl run), родителя не имеет и остаётся в
// списке: пропавший разговор человек ищет руками и злится, а лишняя строка
// стоит дешевле.
func withoutHandedOut(list []chatEntry) []chatEntry {
	out := make([]chatEntry, 0, len(list))
	for _, e := range list {
		if e.Parent != "" {
			continue
		}
		out = append(out, e)
	}
	return out
}

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
	older, days := false, 0
	if r.URL.Query().Get("all") != "" {
		// Окно списка: по умолчанию трое суток плюс живые, ключ days его
		// двигает, а days=0 снимает вовсе. Нулём за списком ходит поиск: он
		// общий по всей машине, и окна не знает ни на шаг.
		days = chatWindowDays
		if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				days = n
			}
		}
		win := chatWindow{keep: chatKeepSet(r.URL.Query().Get("keep"))}
		if days > 0 {
			win.since = s.now().AddDate(0, 0, -days)
		}
		list, older = s.chatEntriesAll(chatAllLimit, win)
	} else {
		list = s.chatEntries(found.Path, chatListLimit)
	}
	// Розданная работа списку не строка. Подпроцесс делегирования пишет свой
	// транскрипт и всплывал бы в списке наравне с разговорами человека, причём
	// первым: пишет он непрерывно и потому свежее всех. Видно его в ленте того
	// разговора, который работу раздал (DK-581). Отсев стоит тут, а не в
	// сборке строк: тем же перечнем меряют живую работу задачи (taskLead),
	// считают агентов кольцом и ищут разговор для реплики, и спрятанная там
	// строка ослепила бы их разом.
	list = withoutHandedOut(list)
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
	} else {
		// Незачатые разговоры едут в список отдельным набором: транскрипта у
		// них нет, и обход каталогов их не находит. Окно свежести им не указ,
		// их всего горстка, а брошенные стираются уборкой. В выдачу поиска по
		// имени tmux они не идут вовсе: этим поиском панель узнаёт родившийся
		// разговор, и подсунуть ей вместо него саму запись значило бы
		// пришивать панель к тому, на чём она и так стоит.
		proj := found.Name
		if r.URL.Query().Get("all") != "" {
			proj = ""
		}
		if blanks := s.chatBlankList(proj); len(blanks) > 0 {
			list = append(list, blanks...)
			sortEntries(list)
		}
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
	opts := s.chatModelOpts()
	resp := map[string]any{"project": found.Name, "chats": list, "models": opts,
		"days": days, "older": older}
	// Пустая лестница это не «моделей нет», а «выбирать нечем»: список моделей
	// дашборд не сочиняет, он целиком приезжает от agentctl, и без него
	// выпадающий список схлопывается в одну строку с текущей моделью. Молча это
	// читается как «модель не поменять» (замечание пользователя), поэтому
	// причина едет ответом и стоит на самом списке.
	if len(opts) == 0 {
		resp["models_note"] = s.chatModelNote()
	}
	if len(list) == 0 {
		if older {
			resp["note"] = fmt.Sprintf("за последние %d сут. разговоров нет, а раньше они есть: "+
				"откройте их кнопкой «показать раньше»", days)
		} else {
			resp["note"] = "чатов тут пока нет: заведите новый кнопкой «+»"
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// chatKeepSet разбирает ключ keep: ID разговоров через запятую, которые
// остаются в списке любого возраста. Панель называет им открытый разговор и
// последний разговор задачи, иначе окно уносило бы из списка то, на чём она
// прямо сейчас стоит.
func chatKeepSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			out[id] = true
		}
	}
	return out
}

// Незачатый разговор. Прежде разговор и сессия клиента были одним и тем же:
// разговор это транскрипт сессии, поэтому до первой реплики его не
// существовало нигде, кроме адресной строки вкладки, а адрес этот («new») был
// один на всю вкладку. Отсюда и жалоба пользователя: набранный текст оставался
// в следующем новом чате, в списке чата не было видно, а завести рядом второй
// было нечем, кнопка «+» уводила на тот же единственный адрес.
//
// Понятия разведены. Кнопка «+» заводит запись разговора сразу, со своим ID и
// своей строкой в списке; таких записей заводится сколько угодно, у каждой свой
// черновик. Сессия клиента поднимается по-прежнему первой репликой: поднимать
// её впустую дорого и незачем. Поднявшаяся сессия пришивается к записи (поле
// Grown), и дальше разговор живёт своим транскриптом, как все остальные.

// chatBlankID даёт имя незачатой записи. Приставка отличает её от ID сессии
// глазом, а сита ключа (chatKeyRe) и адреса (sessionIDRe) она проходит как
// обычный ID: панель адресует такую запись теми же ручками, что и разговор с
// транскриптом.
func chatBlankID() string {
	return chatBlankMark + msgID()
}

// chatBlankMark это приставка незачатой записи. По ней запись отличают от ID
// сессии и там, где памяти о ней уже нет: у закрытой рукой записи файла не
// остаётся, а повторное закрытие обязано сойтись, а не отбиться.
const chatBlankMark = "blank-"

// chatBlankLife это срок записи, в которой нет ни набранного текста, ни
// поднятой сессии. Хранить такую незачем вовсе: она след нажатия «+», а не
// разговор, и за день их набирается горсть («у меня в списке куча чатов
// Новый чат, при заходе в них они пустые», замечание пользователя; замер
// показал пять штук за два с половиной часа).
//
// Срок в час, а не в минуты и не в трое суток окна. Минуты стирали бы запись
// из-под рук: её заводят и уходят думать, а список перечитывается постоянно, и
// уборка идёт прямо в нём. Трое суток это срок разговора, у которого есть что
// хранить, а у пустой записи хранить нечего, и она копилась мусором ровно
// столько. Час переживает отлучку от машины и не переживает рабочего дня.
//
// Набранный текст меняет дело: он живёт в списке сколько угодно, и убирают его
// рукой (решение пользователя).
const chatBlankLife = time.Hour

// chatGrownLife это срок записи, выросшей в сессию. Она доживает дорожным
// знаком для панели, которая стоит на старом адресе, в том числе в соседней
// вкладке, и торопить её незачем: разговор к тому времени давно стоит в списке
// своей строкой.
const chatGrownLife = chatWindowDays * 24 * time.Hour

// chatBlankList отдаёт строки незачатых разговоров: они приходят не из обхода
// транскриптов, а из памяти диалогов, потому что транскрипта у них нет вовсе.
// Пустое имя проекта значит общий список машины, где у строки назван её проект.
// Заодно тут стоит уборка: список спрашивают постоянно, и отдельного сторожка
// для неё заводить незачем.
func (s *server) chatBlankList(proj string) []chatEntry {
	ents, err := os.ReadDir(chatStoreDir(s.cfg.Home))
	if err != nil {
		return nil
	}
	out := []chatEntry{}
	recs := map[string][]sessionBind(nil)
	for _, de := range ents {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "tmux-") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		st := s.chatStoreRead(id)
		if !st.Blank {
			continue
		}
		// Пришивание поднявшейся сессии: имя tmux, которым её подняла первая
		// реплика, реестр отдал родившемуся разговору, и с этой минуты запись
		// знает своего наследника. Дальше по этому полю переезжает панель,
		// стоящая на старом адресе, в том числе в соседней вкладке.
		if st.Grown == "" && st.Tmux != "" {
			if recs == nil {
				recs = s.bindsAll()
			}
			if owner := sessions.TmuxOwner(recs, st.Tmux); owner != "" && owner != id {
				st.Grown = owner
				if err := s.chatStoreWrite(id, st); err != nil {
					s.logf("запись чата %s не пришилась к сессии %s: %v", id, owner, err)
				} else {
					s.logf("незачатый чат %s вырос в сессию %s", id, owner)
				}
			}
		}
		if s.chatBlankSweep(id, st) {
			continue
		}
		if proj != "" && st.Project != proj {
			continue
		}
		e := chatEntry{ID: id, Blank: true, State: chatNotStarted, Idle: true,
			Grown: st.Grown, Draft: st.Draft, Tmux: st.Tmux,
			Model: st.Model, Archived: st.Archived}
		if proj == "" {
			e.Project = st.Project
		}
		if st.Task != "" {
			e.Tasks = []string{st.Task}
		}
		if st.Born > 0 {
			e.Mtime = time.Unix(st.Born, 0).UTC().Format(time.RFC3339)
		}
		out = append(out, e)
	}
	return out
}

// chatBlankSweep стирает отжившую запись и отвечает, стёрта ли она. Сроков тут
// два, и различает их то, есть ли у записи что хранить. Выросшая доживает
// дорожным знаком (chatGrownLife). Пустая, без единой буквы и без сессии, уходит
// через час (chatBlankLife): хранить в ней нечего. Начатая руками живёт вечно,
// её убирают рукой, и то же вечное хранение достаётся записи с поднятой, но ещё
// не назвавшейся сессией.
func (s *server) chatBlankSweep(id string, st chatStore) bool {
	if st.Born == 0 {
		return false
	}
	age := s.now().Sub(time.Unix(st.Born, 0))
	if st.Grown != "" {
		if age <= chatGrownLife {
			return false
		}
	} else {
		if st.Draft != "" || st.Tmux != "" {
			return false
		}
		if age <= chatBlankLife {
			return false
		}
	}
	if err := os.Remove(filepath.Join(chatStoreDir(s.cfg.Home), id+".json")); err != nil {
		s.logf("брошенная запись чата %s не стёрлась: %v", id, err)
		return false
	}
	why := "в ней так и не сказали ни слова"
	if st.Grown != "" {
		why = "разговор давно идёт сессией " + st.Grown
	}
	s.logf("запись чата %s стёрта: %s", id, why)
	return true
}

// handleChatDrop закрывает запись разговора рукой. Отсев по сроку убирает
// пустые сам, но ждать часа человеку незачем: мусор он видит сейчас и в том же
// списке, где стоит кнопка («закрыть их я не могу, просто нет такой
// возможности», замечание пользователя).
//
// Закрывается тут только незачатая запись. Разговор с транскриптом убирают
// архивом, у него своя строка и своя механика со снятием сессии; запись,
// выросшую в сессию, эта ручка не трогает и говорит, куда идти. Живой сессии
// закрытие не касается вовсе: снимать её походя нельзя, за этим стоит отдельное
// движение.
func (s *server) handleChatDrop(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "закрытие чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	// Незачатая запись узнаётся по приставке своего ID, а не по признаку в
	// памяти: у закрытой памяти нет вовсе, и по ней всякий повтор читался бы
	// разговором с лентой.
	if !strings.HasPrefix(sid, chatBlankMark) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "это разговор с лентой, а не пустая запись: его убирают в архив"})
		return
	}
	st := s.chatStoreRead(sid)
	if st.Grown != "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "запись выросла в разговор " + st.Grown + ": закрывать надо его, архивом"})
		return
	}
	if st.Tmux != "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "у записи поднята сессия " + st.Tmux + ": сперва остановите её"})
		return
	}
	if err := os.Remove(filepath.Join(chatStoreDir(s.cfg.Home), sid+".json")); err != nil {
		if os.IsNotExist(err) {
			// Записи уже нет: человек просил именно этого, и отказ тут говорил бы
			// о поломке там, где всё сошлось.
			writeJSON(w, http.StatusOK, map[string]any{"chat": sid, "message": "запись уже закрыта"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("запись чата не закрылась: %v", err)})
		return
	}
	s.logf("запись чата %s закрыта рукой", sid)
	writeJSON(w, http.StatusOK, map[string]any{"chat": sid, "message": "запись закрыта"})
}

// handleChatBlank заводит разговор кнопкой «+». Сессию тут никто не поднимает:
// клиент стоит денег и квоты, а человеку в эту минуту нужна строка в списке и
// поле, в которое можно писать. Поднимет сессию его первая реплика.
func (s *server) handleChatBlank(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "новый чат")
	if found == nil {
		return
	}
	var body struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"model\": \"opus\"}"})
		return
	}
	id := strings.ToUpper(strings.TrimSpace(body.ID))
	if id != "" && !taskParamRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на ID задачи", body.ID)})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = chatModelDefault
	}
	key := chatBlankID()
	rec := chatStore{Blank: true, Born: s.now().Unix(), Project: found.Name, Task: id, Model: model}
	if err := s.chatStoreWrite(key, rec); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("запись чата не завелась: %v", err)})
		return
	}
	s.logf("заведён чат %s в %s (модель %s, задача %q): сессию поднимет первая реплика", key, found.Name, model, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": key, "model": model, "task": id,
		"message": "чат заведён: сессия поднимется первой репликой"})
}

// handleChatDraft держит набранную, но не отправленную реплику незачатого
// разговора. У начатого черновик держит сама вкладка: там есть транскрипт, и
// разговор с чужого экрана виден и без него. У незачатого держать его негде,
// а он же и говорит уборке, что запись человеку нужна.
func (s *server) handleChatDraft(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	if s.findProject(w, r, "черновик реплики") == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	st := s.chatStoreRead(sid)
	if !st.Blank {
		// Отказом это не считается: пока вкладка дописывала черновик,
		// разговор мог начаться первой репликой, и ронять на человека ошибку
		// из-за гонки не за что.
		writeJSON(w, http.StatusOK, map[string]any{"kept": false,
			"message": "разговор уже начат: черновик остаётся во вкладке"})
		return
	}
	st.Draft = body.Text
	if err := s.chatStoreWrite(sid, st); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("черновик не записался: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kept": true})
}

// chatBlankLift пришивает поднятую сессию к незачатой записи: имя tmux ляжет в
// неё сразу, а ID сессии припишет список, когда клиент назовётся в реестре.
// Модель тут переписывается той, которой сессию подняли на самом деле: с этой
// минуты запись говорит о живом разговоре, а не о намерении.
func (s *server) chatBlankLift(sid, sess, model string) {
	if sid == "" || !chatKeyRe.MatchString(sid) {
		return
	}
	st := s.chatStoreRead(sid)
	if !st.Blank || st.Grown != "" {
		return
	}
	st.Tmux, st.Draft = sess, ""
	if model != "" {
		st.Model = model
	}
	if err := s.chatStoreWrite(sid, st); err != nil {
		s.logf("запись чата %s не запомнила сессию %s: %v", sid, sess, err)
	}
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
// собраны launchEnv.
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

// orderRules это приписки, общие заказам поднятой работы: план работ и правило
// канала. Собраны они одним местом нарочно. Правило канала жило только в заказе
// разговора, у груминга черновика заказ свой, и грумер DK-509 ответил человеку
// не текстом в ленту, а отправкой через канал сессий: рамку он прочёл как
// просьбу другой сессии, и человек вопроса в чате не увидел вовсе. Разговор с
// человеком идёт в любой поднятой работе, значит и правило про него едет в
// каждый заказ.
func orderRules(sess string) string {
	return planRuleFor(sess) + " " + channelRule
}

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

// launchEnv это пары окружения поднятой сессии, одни на все четыре дороги
// подъёма: разговор, конвейер задачи, цикл цели и разбор черновика. Своего
// набора у дороги нет ни одного: разнобой тут стоит слепых панелей и потерянных
// записей реестра. Прежде сборка звалась chatVars и жила у разговора, а
// конвейер с разбором звали её сбоку.
//
// Задачу и имя tmux-сессии поднятая сессия
// называет о себе в реестре сама, хуком старта.
func (s *server) launchEnv(id, sess string) string {
	env := "DEVKIT_TMUX=" + shQuote(sess) + " "
	if id != "" {
		env = "DEVKIT_TASK=" + shQuote(id) + " " + env
	}
	// Дом тут настоящий, машинный, а не дом самого дашборда. Причина в
	// раскладке подписок: agentctl exec разворачивает тильду ключа home, и под
	// подложным домом демона CLAUDE_CONFIG_DIR второй подписки указывает в
	// тонкий каталог без логина. Хуки и логин клиента ищутся там же.
	//
	// Плата за это разъезд реестров: хук старта пишет запись в
	// <настоящий дом>/.devkit/sessions.log, а дашборд со своим домом читает
	// свой файл. Живой случай DK-482..486: пять сессий разбора работали, а
	// чаты в панели стояли пустыми. Лечится это чтением обоих домов
	// (bindHomes в registry.go), а не подменой дома у поднятой сессии.
	if home := realHome(); home != "" {
		env = "HOME=" + shQuote(home) + " " + env
	}
	// Путь называется по той же причине, что и дом: своим он у поднятой сессии
	// быть не должен. Каталог экземпляра держит рядом с дашбордом собственные
	// копии agentctl и taskctl, plist ставит его в PATH первым, и всё, что
	// демон поднимает, зовёт эти копии. В POC-контуре чата так неделю и шло:
	// вердикт pick, снимок квоты и гейт бюджета целей считала сборка
	// трёхдневной давности без версии («dev (unknown)»), а разбор DK-457
	// наступил на это прямо, проверяя панель квоты чужим бинарём.
	if p := sessionPath(os.Getenv("PATH"), exeDir(), kitDir()); p != "" {
		env = "PATH=" + shQuote(p) + " " + env
	}
	// Опрос фокуса в сессии, поднятой дашбордом, не нужен вовсе: он ходит в
	// System Events, а macOS приписывает это дашборду и просит у него
	// разрешение на управление компьютером, заново после каждой пересборки
	// (находка одиннадцатого круга POC). Уведомления от такой сессии идут как
	// при неопределённом фокусе, то есть приходят.
	return noFocusEnv + " " + env
}

// kitBins это утилиты кита, которые поднятая сессия зовёт по имени. По ним
// каталог и узнаётся китовым.
var kitBins = []string{"agentctl", "taskctl"}

// kitDir это штатный каталог утилит кита на машине. Перебор тот же, каким
// выбирает каталог назначения установщик (bin_dir в devkitctl update): сперва
// ~/go/bin, потом ~/.local/bin. Дом берётся машинный, той же дорогой, что и у
// поднятой сессии: подложный дом демона тут увёл бы в тонкий каталог
// экземпляра. Пусто, когда кит лежит где-то ещё, и тогда путь остаётся как был.
// Подменяется прогонами: у стенда кит свой, фикстурами.
var kitDir = func() string {
	home := realHome()
	if home == "" {
		return ""
	}
	for _, rel := range []string{"go/bin", ".local/bin"} {
		dir := filepath.Join(home, rel)
		if hasKit([]string{dir}) {
			return dir
		}
	}
	return ""
}

// sessionPath это путь поднятой сессии: штатный каталог утилит кита первым, а
// каталога самого дашборда в нём нет вовсе. Свой каталог дашборду нужен ему
// самому (binPath ищет соседей раньше пути, и экземпляр от этого
// самодостаточен), а сессия им берёт копии вместо утилит машины.
//
// Одним выбрасыванием своего каталога тут не обойтись: экземпляр POC держит
// второй кит в подложном доме демона (~/.devkit/dashboard-poc/.local/bin,
// раскладка devkitctl update под его HOME), и он стоит в том же пути. Штатный
// каталог машины поэтому называется явно и ставится первым.
//
// Пусто значит «путь остаётся как был»: своего каталога в пути нет вовсе,
// путь и так собран как надо, либо штатного каталога мы не знаем, а кита без
// каталога дашборда в пути не остаётся, и выбрасывание оставило бы сессию без
// кита совсем.
func sessionPath(procPath, own, kit string) string {
	if procPath == "" {
		return ""
	}
	rest := []string{}
	found := false
	for _, p := range filepath.SplitList(procPath) {
		if p != "" && sameDir(p, own) {
			found = true
			continue
		}
		if p != "" && sameDir(p, kit) {
			continue
		}
		rest = append(rest, p)
	}
	// Своего каталога в пути нет, и раздавать сессии нечего: путь такой она
	// получит и без нас. Так живёт прогон из исходников, где бинарь собран во
	// временный каталог.
	if !found {
		return ""
	}
	if kit != "" {
		rest = append([]string{kit}, rest...)
	} else if !hasKit(rest) {
		return ""
	}
	out := strings.Join(rest, string(os.PathListSeparator))
	if out == procPath {
		return ""
	}
	return out
}

// sameDir отвечает, тот же это каталог; пустое имя не совпадает ни с чем.
func sameDir(path, dir string) bool {
	return dir != "" && filepath.Clean(path) == filepath.Clean(dir)
}

// hasKit отвечает, лежат ли в этих каталогах обе утилиты кита. Половины мало:
// без taskctl сессия так же беспомощна, как без agentctl.
func hasKit(dirs []string) bool {
	for _, name := range kitBins {
		found := false
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			fi, err := os.Stat(filepath.Join(dir, name))
			if err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
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
		// Chat называет незачатую запись, из которой человек пишет: подъём
		// пришьёт к ней поднятую сессию, и разговор останется той же строкой
		// списка, на которой он начался.
		Chat string `json:"chat"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	id := strings.ToUpper(strings.TrimSpace(body.ID))
	// Задачу называет сама запись, когда её не назвал заказ: чат заведён с
	// экрана задачи, и сессия обязана подняться в её дереве, чем бы ни была
	// занята вкладка.
	blank := strings.TrimSpace(body.Chat)
	if id == "" && blank != "" && chatKeyRe.MatchString(blank) {
		if st := s.chatStoreRead(blank); st.Blank {
			id = st.Task
		}
	}
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
	dir := chatTree(found.Path, id)
	sess := chatNewName(id, tmuxAliveFn())
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model}); err != nil {
		s.logf("модель чата %s не записалась: %v", sess, err)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(s.launchEnv(id, sess), model, "", text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("подъём чата в %s не удался: %s", found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.chatBlankLift(blank, sess, model)
	s.logf("чат поднят в %s (tmux-сессия %s, модель %s, дерево %s)", found.Name, sess, model, dir)
	writeJSON(w, http.StatusOK, map[string]string{
		"tmux": sess, "model": model, "tree": dir,
		"message": fmt.Sprintf("чат поднят в tmux-сессии %s моделью %s: ID сессии встанет в списке первым её ходом", sess, model)})
}

// chatTree выбирает каталог подъёма: боковое дерево задачи предпочитается
// корню проекта, потому что разговор про задачу, у которой дерево заведено,
// идёт там же, где её работа. Дороги подъёма две, с кнопки и первой репликой в
// незачатый разговор, и каталог у них обязан быть один.
func chatTree(projPath, id string) string {
	if id == "" {
		return projPath
	}
	tree := filepath.Join(filepath.Dir(projPath), filepath.Base(projPath)+"-"+strings.ToLower(id))
	if isDir(tree) {
		return tree
	}
	return projPath
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

// chatKill снимает tmux-сессию разговора целиком. Точка одна на всех зовущих:
// её зовёт и стоп под перезапуск, и уборка в архив, и подмена в тестах держится
// за неё же.
var chatKill = func(name string) error {
	_, err := runProc("tmux", "kill-session", "-t", name)
	return err
}

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
	last := sessions.Last(s.bindsAll()[sid])
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
		if err := chatKill(last.Tmux); err != nil {
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
	recs := s.bindsAll()
	last := sessions.Last(recs[sid])
	// Сессия стоит на вопросе инструмента ожидания: реплика идёт во вход
	// разговора, а не в сокет клиента. Ждёт её процесс taskctl ask внутри хода
	// Bash, и сокета он не слышит вовсе: реплика, ушедшая клиенту, легла бы в
	// очередь следующего хода, а ожидание тем временем добрало бы свой срок и
	// припарковало задачу с готовым ответом на руках.
	if done, ok := s.sayToAsk(found, info, sid, text); ok {
		s.chatSayDone(sid, claim, "ask")
		s.saidSay(saidSessionKey(sid), text, "ask")
		writeJSON(w, http.StatusOK, done)
		return
	}
	// Терминальная дорога первая (DK-480): реплика в свою живую tmux-сессию
	// подаётся клавишами и приходит агенту вводом человека, без рамки
	// межсессионного канала. Только на этой дороге строка с `!` исполняется
	// терминалом без витка модели, а ответ на запертый вопрос разрешения
	// отпускает диалог: сообщению соседней сессии харнес одобрением не верит,
	// и это правильно, чинится доставка, а не правила доверия.
	term := s.sayTermOf(sid, recs)
	stuckDialog := false
	if term != "" && s.chatStuck(sid) != "" {
		ask := tmuxAskOf(term)
		if opt := askOptionOf(ask, text); opt > 0 {
			if err := tmuxAnswer(term, ask, opt, ""); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
					"ответ не подался в tmux-сессию %s: %s", term, procErr(err))})
				return
			}
			pick := strings.TrimSpace(ask.Options[opt-1].Text)
			s.chatSayDone(sid, claim, "answer")
			s.saidSay(saidSessionKey(sid), text, "answer")
			s.logf("реплика чата %s отпустила запертый вопрос: пункт %d (%s)", sid, opt, pick)
			writeJSON(w, http.StatusOK, map[string]any{"way": "answer", "tmux": term, "option": opt,
				"note":    fmt.Sprintf("ответ подан клавишами в запертый вопрос: пункт %d (%s)", opt, pick),
				"message": "запертый вопрос отпущен: работа идёт дальше без терминала"})
			return
		}
		// Свободные слова в модальный вопрос не печатаются: латинская буква в
		// них сработала бы горячей клавишей диалога, и реплика человека нажала
		// бы кнопку за него. Сокет кладёт такую реплику в очередь клиента
		// (дорога ниже), а без сокета она остаётся у панели с причиной и
		// кнопкой повтора, а не теряется в диалоге молча.
		stuckDialog = true
		if _, ok := s.peers()[sid]; !ok {
			s.logf("реплика чата %s не поехала: %s", sid, stuckAskSayWord)
			writeJSON(w, http.StatusOK, map[string]any{"way": "held", "tmux": term,
				"stuck": stuckAskSayWord})
			return
		}
	}
	if term != "" && !stuckDialog {
		// Замороженный терминал глотает send-keys без эха (клин 69975):
		// живость событийного цикла спрашивает зонд сокета с памятью, когда
		// сокет у клиента есть. Свежий транскрипт снимает вопрос без зонда.
		if p, ok := s.peers()[sid]; ok && s.peerDeaf(p.Sock, info.mod) {
			s.logf("терминал чата %s заморожен, клавиши туда не едут (%s)", sid, stuckDeafWord)
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": "клиент не отвечает на зонд: событийный цикл стоит, и клавиши ушли бы в никуда",
				"stuck": stuckDeafWord})
			return
		}
		if err := chatSend(term, text); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
				"реплика не подалась в tmux-сессию %s: %s", term, procErr(err))})
			return
		}
		s.chatSayDone(sid, claim, "send-keys")
		s.saidSay(saidSessionKey(sid), text, "send-keys")
		s.logf("реплика подана в чат %s (tmux-сессия %s)", sid, term)
		out := map[string]any{"way": "send-keys", "tmux": term,
			"message": "реплика подана прямо в процесс агента: ответ придёт в ленту"}
		if bangLine(text) {
			out["note"] = "строка с ! ушла терминалу сессии: команда исполнится без витка модели, вывод ляжет в ленту"
		}
		if why := s.chatStuck(sid); why != "" {
			out["stuck"] = why
			s.logf("реплика чата %s легла в очередь: %s", sid, why)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// Канал самого клиента: сессия без своей tmux (окно vscode, чужой
	// терминал) слышна только сокетом. Реплика там приходит межсессионным
	// сообщением с подписью дашборда, а не вводом человека: текст доезжает, а
	// терминальные механики не работают, и про них дорога говорит словами.
	if p, ok := s.peers()[sid]; ok {
		err := peerSay(p.Sock, text)
		if err == nil {
			s.chatSayDone(sid, claim, "socket")
			s.saidSay(saidSessionKey(sid), text, "socket")
			s.logf("реплика ушла в сокет чата %s (pid %d, %s)", sid, p.PID, peerWord(p))
			out := map[string]any{"way": "socket", "pid": p.PID, "where": peerWord(p)}
			if bangLine(text) {
				out["note"] = "терминального входа у сессии нет: строка с ! уехала агенту текстом, терминал её не исполнит"
			}
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
	if term != "" {
		// Живой терминал есть, а доставка не случилась: сюда доходит только
		// реплика, которую держал запертый вопрос, когда её не взял и сокет.
		// Резюм поднял бы второго агента рядом с живым, отказ честнее.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": "реплика не доставлена: клиент стоит на запертом вопросе, а его сокет не отвечает",
			"stuck": stuckAskSayWord})
		return
	}
	alive := tmuxAliveFn()
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
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
		chatCmd(s.launchEnv(task, sess), model, sid, text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
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
	out := map[string]any{"way": "resume", "tmux": sess, "model": model,
		"message": fmt.Sprintf(
			"процесса у чата не было: поднят claude --resume в tmux-сессии %s, история продолжена", sess)}
	if bangLine(text) {
		out["note"] = "живого терминала у сессии не было: строка с ! уехала вводной резюма текстом, терминал её не исполнит"
	}
	writeJSON(w, http.StatusOK, out)
}

// stuckAskSayWord это слова недоставленной реплики при запертом вопросе без
// сокета: панель держит пузырь с этой причиной и кнопкой повтора.
const stuckAskSayWord = "агент ждёт разрешения в своём окне: свободная реплика в запертый вопрос не едет, " +
	"ответьте пунктом вопроса в ленте или словом варианта"

// sayTermOf находит терминальный вход разговора: имя живой tmux-сессии, чьим
// хозяином реестр называет этот же разговор. Пустой ответ значит, что дороги
// клавишами нет: сессию подняли не мы (окно vscode, чужой терминал), tmux на
// машине нет вовсе либо имя из реестра успел забрать другой разговор (DK-397
// POC: конвейер снимает сессию и поднимает новую тем же именем, а send-keys по
// нему уехал бы в чужую сессию).
func (s *server) sayTermOf(sid string, recs map[string][]sessionBind) string {
	last := sessions.Last(recs[sid])
	if last.Tmux == "" || tmuxMissingCheck() != "" || !tmuxAliveFn()(last.Tmux) {
		return ""
	}
	if held := sessions.TmuxOwner(recs, last.Tmux); held != "" && held != sid {
		s.logf("имя tmux %s занято разговором %s, а не %s: клавишами туда нельзя",
			last.Tmux, held, sid)
		return ""
	}
	return last.Tmux
}

// bangLine узнаёт строку терминальной команды: `!` первым знаком включает у
// клиента bash-режим, и строка исполняется без витка модели. Многострочная
// реплика едет скобками вставки и bash-режима не включает, поэтому она не в
// счёт.
func bangLine(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "!") && !strings.Contains(text, "\n")
}

// askOptionOf сопоставляет свободную реплику человека с вариантами запертого
// вопроса. Совпадение строгое нарочно: реплика превращается в нажатие, и
// угадывать за человека дороже, чем отказать. Понимаются номер пункта, слова
// согласия и отказа («да» это первый пункт Yes, «нет» это первый пункт No) и
// однозначное начало текста варианта. Служебные пункты виджета (свободный
// ответ, кнопки Next и Submit) так не выбираются: свободный ответ без слов
// открыл бы у клиента пустое поле, и человек думал бы, что ответил.
func askOptionOf(ask tmuxAsk, text string) int {
	said := strings.TrimRight(strings.ToLower(strings.TrimSpace(text)), ".!")
	if said == "" || len(ask.Options) == 0 {
		return 0
	}
	plain := func(n int) int {
		if n >= 1 && n <= len(ask.Options) && ask.Options[n-1].Kind == "" {
			return n
		}
		return 0
	}
	if n, err := strconv.Atoi(said); err == nil {
		return plain(n)
	}
	prefix := ""
	switch said {
	case "да", "yes", "y", "ок", "ok", "можно", "давай", "разрешаю", "подтверждаю":
		prefix = "yes"
	case "нет", "no", "n", "нельзя", "запрещаю", "отмена":
		prefix = "no"
	}
	hit := 0
	for i, o := range ask.Options {
		word := strings.ToLower(strings.TrimSpace(o.Text))
		if prefix != "" {
			if strings.HasPrefix(word, prefix) {
				return plain(i + 1)
			}
			continue
		}
		if strings.HasPrefix(word, said) {
			if hit != 0 {
				return 0
			}
			hit = i + 1
		}
	}
	return plain(hit)
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
		if n.Reason != "permission_prompt" {
			return ""
		}
		// Время хода тут не судья: запись транскрипта ложится и перед самым
		// вопросом (вызов инструмента, которым вопрос и вызван), и «ход свежее
		// вопроса» ответа на вопрос не доказывает. Закрывает запись следующее
		// событие сессии, и так это работало до сих пор.
		return "агент ждёт разрешения в своём окне: реплика встала в очередь и хода не даёт"
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
	info, hasInfo := findSession(s.transcriptRoots(), found.Path, sid)
	// Второй барьер после рубежа виджета: вопрос, чьи варианты уже стоят в
	// ленте репликой человека или ответом агента, это эхо вывода, а не виджет.
	// Лента тут читается только когда вопрос вообще разобрался, то есть редко.
	if len(ask.Options) > 0 && hasInfo {
		if askEchoesFeed(ask, sessionFeedOf(info.path, askEchoTail).items) {
			s.logf("вопрос клиента %s повторяет ленту разговора: показывать нечего", name)
			ask = tmuxAsk{}
		}
	}
	if len(ask.Options) == 0 {
		// Клиент, по всем признакам стоящий на вопросе, а вопрос с панели не
		// собрался: это повод чинить разбор, и говорится он строкой в журнал,
		// а не плашкой человеку (решение пользователя). Плашка тут ничего не
		// объясняла и ничего не предлагала, а вылезала и на уже отвеченном
		// опросе.
		s.askQuietLog(sid, name)
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "tmux": name,
			"note": fmt.Sprintf("клиент %s ни о чём не спрашивает", name)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "tmux": name, "ask": ask})
}

// askQuietWindow это как часто один и тот же молчащий виджет попадает в
// журнал. Панель переспрашивает вопрос каждые несколько секунд, и без окна
// журнал забило бы одной и той же строкой.
const askQuietWindow = 5 * time.Minute

// askQuietLog пишет в журнал дашборда, что клиент похоже ждёт ответа, а
// виджета панель не разобрала. Это наш сигнал чинить разбор: человеку про это
// сказать нечего, он и так видит своё окно.
func (s *server) askQuietLog(sid, name string) {
	if s.chatStuck(sid) == "" {
		return
	}
	s.mu.Lock()
	if s.askQuiet == nil {
		s.askQuiet = map[string]time.Time{}
	}
	last, seen := s.askQuiet[sid]
	now := s.now()
	if seen && now.Sub(last) < askQuietWindow {
		s.mu.Unlock()
		return
	}
	s.askQuiet[sid] = now
	s.mu.Unlock()
	s.logf("клиент %s похоже ждёт ответа, а виджета в снимке панели не разобрать: "+
		"разбор надо чинить, человеку показывать нечего", name)
}

// askEchoTail это сколько последних записей ленты сверяется с вопросом: эхо
// стоит в панели терминала последним, и копать глубже незачем.
const askEchoTail = 12

// askEchoesFeed отвечает, повторяет ли разобранный вопрос то, что уже сказано в
// ленте. Совпали варианты со словами реплики, значит клиент показывает не
// виджет, а свой же вывод: панель терминала режет длинные строки по ширине,
// поэтому вариант ищется в тексте ленты как подстрока, а не сверяется целиком.
// Барьер строгий нарочно: чтобы счесть вопрос эхом, в ленте должны найтись все
// его варианты, а не один.
func askEchoesFeed(ask tmuxAsk, items []reply) bool {
	if len(ask.Options) < 2 || len(items) == 0 {
		return false
	}
	var said strings.Builder
	for _, it := range items {
		said.WriteString(it.Text)
		said.WriteString("\n")
	}
	text := said.String()
	for _, o := range ask.Options {
		word := strings.TrimSpace(o.Text)
		// Короткое слово в ленте находится случайно: варианту виджета длина
		// тут не мешает, а «Да» встретится в любом разговоре.
		if len([]rune(word)) < 12 || !strings.Contains(text, word) {
			return false
		}
	}
	return true
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
	name := sessions.Last(s.bindsAll()[sid]).Tmux
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
	recs := s.bindsAll()
	task := ""
	if t := sessions.Touched(recs[sid]); len(t) > 0 {
		task = t[0]
	}
	// Незачатая запись сама называет свою задачу: реестр про неё ничего не
	// знает, сессии-то ещё не было, а разговор заводили с экрана задачи, и
	// поднимать его надо в её дереве.
	if store := s.chatStoreRead(sid); task == "" && store.Blank {
		task = store.Task
	}
	model := s.chatModel(sid, "")
	sess := chatNewName(task, tmuxAliveFn())
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, From: sid}); err != nil {
		s.logf("настройки чата %s не записались: %v", sess, err)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", chatTree(found.Path, task),
		chatCmd(s.launchEnv(task, sess), model, "", text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		msg := fmt.Sprintf("tmux не поднял сессию чата %s: %s", sid, procErr(err))
		s.logf("%s", msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	s.chatBlankLift(sid, sess, model)
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

// handleChatArchive убирает разговор в архив и возвращает обратно. Архив это
// уборка рукой: список прячет убранное, пока человек не попросит показать
// архивные, а окно по свежести тут не помощник, убирают как раз свежее
// (замечание пользователя про десяток отработавших чатов после разбора
// накопителя).
//
// Живая сессия убранного разговора снимается той же точкой, что и стоп под
// перезапуск: убирают отработавший чат, и оставлять за ним живой клиент значит
// держать процесс, за которым больше не следят. Снятие тут дело
// сопутствующее, и отказ tmux уборку не отменяет: признак уже лёг, а про
// несостоявшееся снятие сказано в ответе.
func (s *server) handleChatArchive(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "архив чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	var body struct {
		Archived bool `json:"archived"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"archived\": true}"})
		return
	}
	done, err := s.chatArchive(sid, body.Archived)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("архив не записался: %v", err)})
		return
	}
	resp := map[string]any{"session": sid, "archived": body.Archived, "message": done.message}
	if done.tmux != "" {
		resp["dropped"], resp["tmux"] = done.dropped, done.tmux
	}
	writeJSON(w, http.StatusOK, resp)
}

// archDone это исход уборки: слова для человека и судьба живой сессии.
type archDone struct {
	message string
	dropped bool
	tmux    string
}

// chatArchive это сама механика уборки, одна на руку и на автоматику: признак
// ложится в память диалога, а живая сессия убранного снимается той же точкой,
// что и стоп под перезапуск. Отказ tmux уборку не отменяет, признак уже лёг, и
// про несостоявшееся снятие сказано словами.
func (s *server) chatArchive(sid string, on bool) (archDone, error) {
	st := s.chatStoreRead(sid)
	st.Archived = on
	if err := s.chatStoreWrite(sid, st); err != nil {
		return archDone{}, err
	}
	if !on {
		s.logf("чат %s возвращён из архива", sid)
		return archDone{message: "разговор вернулся в список"}, nil
	}
	last := sessions.Last(s.bindsAll()[sid])
	if last.Tmux == "" || !tmuxAliveFn()(last.Tmux) {
		s.logf("чат %s убран в архив", sid)
		return archDone{message: "разговор убран в архив"}, nil
	}
	if err := chatKill(last.Tmux); err != nil {
		s.logf("чат %s убран в архив, но tmux-сессия %s не снялась: %v", sid, last.Tmux, err)
		return archDone{tmux: last.Tmux, message: fmt.Sprintf(
			"разговор убран в архив, но сессия %s не снялась: %s", last.Tmux, procErr(err))}, nil
	}
	s.logf("чат %s убран в архив, сессия %s снята", sid, last.Tmux)
	return archDone{dropped: true, tmux: last.Tmux, message: "разговор убран в архив, сессия снята"}, nil
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
		chatCmd(s.launchEnv(id, sess), model, sid, prompt(sess), s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
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
		// В незачатом разговоре ни слова не сказано, и называть его нечем:
		// заказ заголовка поднял бы сессию суммаризации на пустоту.
		if e.Blank {
			continue
		}
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
		chatCmd(s.launchEnv(id, sess), model, "", text, s.rotateTokens(), s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("tmux не поднял новый чат %s: %s", id, procErr(err))})
		return
	}
	s.logf("работа %s поднята новым чатом в tmux-сессии %s", id, sess)
	writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "fresh", "tmux": sess,
		"message": "чата не было: поднят новый в tmux-сессии " + sess})
}
