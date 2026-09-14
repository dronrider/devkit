package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

// Экраны дашборда стоят ровно столько, сколько стоят подпроцессы под ними, и
// вся правда о них лежит в утилитах и файлах: своей базы у дашборда нет (LLD
// DK-112). Поэтому ускорение тут двумя средствами, и оба ничего не хранят
// дольше жизни процесса. Первое это память процесса на ответ, который
// доказуемо свеж: обход корней по короткому сроку, доска по отпечатку своего
// файла и потолку срока. Второе это опрос разом, а не по очереди: и обход
// кандидатов, и опрос проектов упираются в чужие процессы, и ждать их
// поодиночке значит складывать сроки вместо выбора худшего.
//
// Толпа за одним ключом когда-то была принята осознанно, с условием пересмотра
// числом. Число пришло 2026-09-14: 45 живых taskctl list по 12 деревьям при 21
// доске в списке, средняя нагрузка 154, и дашборд не отвечал вовсе (DK-992).
// Теперь за ключом идёт одиночный полёт: запрос, заставший чужой taskctl по
// тому же дереву, ждёт его ответа, а не поднимает свой, и сверху лежит общий на
// процесс потолок разом живых детей. Устаревший ответ с тем же отпечатком файла
// отдаётся сразу, а свежий поднимается один раз в фоне: под нагрузкой экран
// обязан отвечать старой строкой, а не висеть до перезапуска демона.

const (
	// scanTTL это срок памяти на обход корней. Корни меняются реже, чем
	// открываются экраны, а заведённый рядом проект показывается через
	// секунды, а не через перезапуск демона.
	scanTTL = 5 * time.Second
	// boardTTL это потолок срока для ответа taskctl. Отпечаток файла ловит
	// правку доски сам, но часть вывода утилита считает не по файлу (возраст
	// строки, метки слитого кода), и правка мимо файла обязана доезжать без
	// стука.
	boardTTL = 10 * time.Second
	// scanWorkers и projectWorkers держат число разом живых подпроцессов:
	// корень с сотней досок иначе поднял бы сотню git одним запросом.
	scanWorkers    = 8
	projectWorkers = 8
	// taskctlLimit это общий на процесс потолок разом живых taskctl list.
	// Одиночный полёт держит по одному опросу на дерево, а этот потолок держит
	// сумму по всем деревьям: деревьев задач на машине бывает больше десятка, и
	// разом поднятые дети складывают свою цену на одну машину с исполнителями.
	taskctlLimit = 6
)

// taskctlGate это тот самый потолок. Один на процесс, потому что и считать надо
// по процессу: детей плодит не один запрос, а все ручки экрана вместе.
var taskctlGate = newGate(taskctlLimit)

// gate это семафор, который умеет сказать, сколько дел под ним живёт и сколько
// ждёт входа. Счёт нужен наружу: отставание опроса иначе видно только по
// нагрузке машины (/healthz, DK-992).
type gate struct {
	slots   chan struct{}
	mu      sync.Mutex
	live    int
	waiting int
}

func newGate(n int) *gate {
	if n < 1 {
		n = 1
	}
	return &gate{slots: make(chan struct{}, n)}
}

func (g *gate) enter() {
	g.mu.Lock()
	g.waiting++
	g.mu.Unlock()
	g.slots <- struct{}{}
	g.mu.Lock()
	g.waiting--
	g.live++
	g.mu.Unlock()
}

func (g *gate) leave() {
	g.mu.Lock()
	g.live--
	g.mu.Unlock()
	<-g.slots
}

// counts отдаёт живых детей и ждущих входа.
func (g *gate) counts() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.live, g.waiting
}

type scanEntry struct {
	projects []Project
	errs     []string
	born     time.Time
	ok       bool
}

type boardEntry struct {
	raw   json.RawMessage
	stamp string
	born  time.Time
}

// boardFlight это идущий опрос доски по одному дереву. Пока он летит, свой
// taskctl никто больше не поднимает: запросы либо ждут его ответа, либо берут
// устаревший ответ из памяти.
type boardFlight struct {
	done chan struct{}
	raw  json.RawMessage
	err  error
}

// projects отдаёт обход корней, свежий или из памяти. Результат читают, но не
// правят: он лежит в кэше и уезжает следующему запросу тем же.
func (s *server) projects() ([]Project, []string) {
	now := s.now()
	s.mu.Lock()
	e := s.scan
	s.mu.Unlock()
	if e.ok && now.Sub(e.born) < scanTTL {
		return e.projects, e.errs
	}
	projects, errs := scanProjects(s.cfg.Roots, s.wt)
	s.mu.Lock()
	s.scan = scanEntry{projects: projects, errs: errs, born: now, ok: true}
	s.mu.Unlock()
	return projects, errs
}

// boardStamp это отпечаток файла доски: время правки и размер. По нему память
// узнаёт чужую правку, чьей бы рукой она ни пришла: taskctl дашборда, агент в
// соседнем окне или git.
func boardStamp(dir string) (string, bool) {
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(boardRel)))
	if err != nil || fi.IsDir() {
		return "", false
	}
	return fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size()), true
}

// projectBoard отдаёт доску проекта байтами ответа taskctl, по возможности из
// памяти процесса. Ответ оттуда идёт, только пока файл доски тот же и не вышел
// потолок срока; отпечаток снимается до запуска утилиты, поэтому правка,
// пришедшая во время запуска, читается заново следующим запросом.
//
// Дальше три дороги вместо прежней одной. Ответ свеж, значит он и уезжает.
// Ответ той же доски, но старше потолка, уезжает как есть, а свежий поднимается
// один раз в фоне: возраст строки догонит следующим запросом, а ждать перед
// пустым экраном человеку нечем. Ответа нет вовсе, значит запрос либо поднимает
// опрос сам, либо ждёт чужой, уже летящий по тому же дереву.
//
// Ошибки не кэшируются вовсе: «taskctl не нашёлся» это причина, а не ответ, и
// поднятый бинарь обязан доезжать до экрана сразу, а не по выходе срока.
func (s *server) projectBoard(dir string) (json.RawMessage, error) {
	stamp, stamped := boardStamp(dir)
	now := s.now()
	s.mu.Lock()
	e, hit := s.boards[dir]
	same := hit && stamped && e.stamp == stamp
	if same && now.Sub(e.born) < boardTTL {
		// Круг поспел за экраном: следующее отставание по этому дереву снова
		// стоит строки в журнале.
		delete(s.lagSaid, dir)
		s.mu.Unlock()
		return e.raw, nil
	}
	fl, flying := s.flights[dir]
	if same {
		s.noteLag(dir)
		if !flying {
			fl = s.takeoff(dir)
			go s.fly(dir, stamp, stamped, fl)
		}
		s.mu.Unlock()
		return e.raw, nil
	}
	if flying {
		s.boardWaiting++
		s.noteLag(dir)
		s.mu.Unlock()
		<-fl.done
		s.mu.Lock()
		s.boardWaiting--
		s.mu.Unlock()
		return fl.raw, fl.err
	}
	fl = s.takeoff(dir)
	s.mu.Unlock()
	s.fly(dir, stamp, stamped, fl)
	return fl.raw, fl.err
}

// takeoff заводит запись о полёте; зовётся под s.mu.
func (s *server) takeoff(dir string) *boardFlight {
	fl := &boardFlight{done: make(chan struct{})}
	s.flights[dir] = fl
	return fl
}

// fly это один опрос доски: подпроцесс под общим потолком, запись в память и
// подъём ждущих. Зовётся и своей горутиной запроса, и фоновой.
//
// Уборка идёт через defer вся целиком, и паника ловится тут по двум разным
// причинам. В фоновой горутине она уносит процесс целиком, как и любая паника
// вне обработчика (тот же довод стоит у inParallel ниже). В горутине запроса её
// погасил бы recover самого net/http, но запись полёта и слот семафора остались
// бы за ней навсегда, и следующий запрос по этому дереву встал бы на <-fl.done
// без срока. Паника уезжает ждущим ошибкой со словами, а стек в журнал.
func (s *server) fly(dir, stamp string, stamped bool, fl *boardFlight) {
	taskctlGate.enter()
	defer func() {
		if v := recover(); v != nil {
			rec := &recovered{val: v, stack: debug.Stack()}
			fl.raw, fl.err = nil, rec
			s.notePanic("опрос доски "+dir, rec)
		}
		s.mu.Lock()
		delete(s.flights, dir)
		s.mu.Unlock()
		taskctlGate.leave()
		close(fl.done)
	}()
	if s.boardProbe != nil {
		s.boardProbe(dir)
	}
	raw, err := boardJSON(dir)
	s.mu.Lock()
	fl.raw, fl.err = raw, err
	// Срок считается от ответа, а не от запроса: опрос под нагрузкой сам идёт
	// дольше потолка, и ответ, устаревший в момент своего прихода, гнал бы фоновый
	// опрос на каждый запрос экрана.
	if err == nil && stamped {
		s.boards[dir] = boardEntry{raw: raw, stamp: stamp, born: s.now()}
	}
	s.mu.Unlock()
}

// noteLag пишет в журнал первый пропущенный круг по дереву: запрос обошёлся
// чужим опросом или устаревшим ответом вместо своего taskctl. Строка одна на
// полосу отставания, а не на каждый запрос: ручек экрана десяток, и без этого
// журнал забило бы одной и той же жалобой. Зовётся под s.mu.
func (s *server) noteLag(dir string) {
	if s.lagSaid[dir] {
		return
	}
	s.lagSaid[dir] = true
	live, waiting := taskctlGate.counts()
	s.logf("опрос доски %s не поспел за экраном: круг пропущен, живых taskctl %d, ждут %d",
		dir, live, waiting+s.boardWaiting)
}

// boardLoad отдаёт нагрузку опроса досок для /healthz: живых детей taskctl и
// ждущих запросов (и тех, что стоят за потолком, и тех, что ждут чужой полёт).
func (s *server) boardLoad() (int, int) {
	live, waiting := taskctlGate.counts()
	s.mu.Lock()
	waiting += s.boardWaiting
	s.mu.Unlock()
	return live, waiting
}

// forgetBoard снимает память ответа taskctl по этому проекту. Отпечаток файла
// ловит чужие правки (агент в соседнем окне, git), а этот сброс закрывает свои:
// строка, заведённая нашей же ручкой, обязана быть в первом же ответе доски, а
// не через секунду-другую (замечание пользователя про «приходится обновлять
// страницу»). Отпечаток от сброса не страдает: следующий запрос снимет его
// заново вместе со свежим ответом.
func (s *server) forgetBoard(dir string) {
	s.mu.Lock()
	delete(s.boards, dir)
	s.mu.Unlock()
}

// recovered это паника подзадачи, пойманная и превращённая в ошибку: словами
// для экрана, стеком для журнала.
type recovered struct {
	val   any
	stack []byte
}

func (r *recovered) Error() string {
	return fmt.Sprintf("паника при опросе: %v", r.val)
}

// Stack отдаёт стек паники. Он едет в журнал и только туда: на экране от него
// пользы нет, а разбирать поломку всё равно по журналу.
func (r *recovered) Stack() string { return string(r.stack) }

// notePanic называет панику подзадачи: в журнал строкой со стеком, наружу
// словами. Стек в ответ не уезжает, чтобы экран не показывал потроха.
func (s *server) notePanic(what string, err error) string {
	var rec *recovered
	if errors.As(err, &rec) {
		s.logf("%s: %v\n%s", what, err, rec.Stack())
	} else {
		s.logf("%s: %v", what, err)
	}
	return err.Error()
}

// inParallel гоняет n дел разом, но не больше workers штук сразу; отдаёт
// ошибку на каждое дело, где ошибка это пойманная паника, а не отказ самого
// дела (свой отказ каждое дело возвращает как умеет).
//
// Паника ловится тут потому, что горутина уносит с собой весь процесс. Пока
// опрос шёл в горутине http-обработчика, панику гасил recover самого net/http
// и она стоила одного запроса; в своей горутине она стоила бы демона целиком,
// а дашборд обязан пережить один сломанный проект и назвать его словами.
func inParallel(workers, n int, fn func(i int)) []error {
	if workers < 1 {
		workers = 1
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if v := recover(); v != nil {
					errs[i] = &recovered{val: v, stack: debug.Stack()}
				}
			}()
			fn(i)
		}(i)
	}
	wg.Wait()
	return errs
}
