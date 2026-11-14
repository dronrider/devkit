package main

import (
	"errors"
	"fmt"

	"github.com/dronrider/devkit/internal/sessions"
)

// liftOrder это заказ подъёма мёртвого разговора. Дорога подъёма одна, и всё,
// чем её звонки различаются, лежит здесь, а не в копиях последовательности.
type liftOrder struct {
	found     *Project    // проект разговора
	info      sessionInfo // транскрипт поднимаемой сессии
	sid       string      // разговор, который продолжают
	task      string      // задача разговора, пустая у свободного чата
	tmux      string      // прежнее имя окна разговора
	text      string      // реплика, уезжающая вводной резюма
	name      string      // имя головы свежей сессии, у резюма пустое
	effort    string
	road      string // метка переведённого резюма (DK-1455)
	reason    string // повод записи реестра у резюма
	reasonNew string // повод записи реестра у свежей сессии
}

// liftDone это итог подъёма: способ, имя поднятого окна, вводная, которой
// уехал заказ, и модель, которой поднято продолжение.
type liftDone struct {
	way   string
	sess  string
	text  string
	model string
}

// liftChat поднимает продолжение мёртвого разговора (LLD DK-1422, решение 5):
// резюм там, где харнес умеет продолжение, свежая сессия с подкладкой
// состояния записи там, где нет. Последовательность жила двумя копиями,
// репликой кончившегося разговора и кнопкой «Продолжить работу», пока ответ по
// вручённому вопросу не получил ту же дорогу (DK-1569): дальше подъём один, и
// дороги различаются только вводной и поводом записи реестра.
func (s *server) liftChat(o liftOrder) (liftDone, error) {
	if m := tmuxMissingCheck(); m != "" {
		return liftDone{}, errors.New(m)
	}
	dir, okTree := sessionTree(o.found.Path, o.info.suffix)
	if !okTree {
		dir = o.found.Path
	}
	model, harness := s.chatChoice(o.sid, o.tmux)
	if m := s.chatHooksGap(model, harness); m != "" {
		return liftDone{}, errors.New(m)
	}
	// Резюм чужой подписке перед подъёмом переводится (DK-1455): транскрипт
	// лежит в дереве подписки-писателя, и целевой клиент без переноса его не
	// найдёт. Неудача перевода откатывается на ту же свежую сессию с подкладкой,
	// а не на смерть резюма.
	way, resumeSid, moved := "resume", o.sid, ""
	if s.resumeWay(model, harness) {
		note, err := s.translateChat(o.info, model, harness)
		if err != nil {
			way, resumeSid = "fresh", ""
			o.text = s.chatStateLine(o.task) + o.text
			line := "перевод разговора в дерево подписки подъёма не удался, продолжение поднято свежей сессией с подкладкой состояния: " + err.Error()
			s.saidMark(saidSessionKey(o.sid), line)
			s.logf("%s", line)
		} else if note != "" {
			moved = o.road
			s.logf("чат %s переведён в дерево подписки подъёма: %s", o.sid, note)
		}
	} else {
		way, resumeSid = "fresh", ""
		o.text = s.chatStateLine(o.task) + o.text
	}
	sess := liftWindow(o.task, o.tmux, tmuxAliveFn())
	if err := s.preserveGroomContext(o.sid, o.task); err != nil {
		return liftDone{}, fmt.Errorf("режим разбора не сохранён: %v", err)
	}
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, Harness: harness, From: o.sid}); err != nil {
		s.logf("настройки чата %s не записались: %v", sess, err)
	}
	if m := s.chatClientMissing(model, harness); m != "" {
		return liftDone{}, errors.New(m)
	}
	if err := startManaged(o.found.Path, dir, o.task, sess, func(ack string) string {
		return chatCmd(s.launchEnv(o.task, sess, o.sid)+effortPair(o.effort)+"DEVKIT_CLOUD_ACK="+shQuote(ack)+" ", model, resumeSid, o.text, o.name, s.chatHarnessOf(model, harness), binPath(agentctlBin))
	}); err != nil {
		return liftDone{}, fmt.Errorf("tmux не поднял продолжение чата %s: %s", o.sid, procErr(err))
	}
	// Имя tmux-сессии ложится в реестр на старый ID сразу: хук старта запишет
	// свою строку, когда клиент родится, а до того мера живости диалога уже
	// обязана работать, иначе вторая реплика подряд подняла бы второй резюм.
	reason := o.reason
	if way != "resume" {
		reason = o.reasonNew
	}
	sessions.Append(sessions.Path(s.cfg.Home),
		sessions.Line(s.now(), o.sid, sessions.Bind{Task: o.task, Source: "заказ",
			Project: o.found.Name, Tree: dir, Tmux: sess}, reason))
	s.chatRaised(sess, o.sid, o.task, o.found.Name)
	if moved != "" {
		s.chatMovedArm(sess, moved)
	}
	return liftDone{way: way, sess: sess, text: o.text, model: model}, nil
}

// deliverToCarrier доставляет ответ вручённого вопроса носителю записи, у
// которого нет живого терминала (DK-1569): живой сокет клиента берёт реплику
// межсессионным сообщением, мёртвая сессия поднимается продолжением той же
// сессии с ответом вводной. Прежде дорога одна клала адресную строку в вход
// разговора, и носитель, умерший между парковкой и ответом, не узнавал о ней
// никогда: файловая дорога считалась успехом, сторожок адресную строку ответом
// не считает, и молчание было неотличимо от работы. Возврат false значит, что
// дорога не справилась и ответу нужен запасной путь: безадресная строка, её
// сторожок считает ответом и будит припаркованную строку тиком.
func (s *server) deliverToCarrier(p *Project, sid, task, wire string) bool {
	if s.chatStoreRead(sid).Protocol != "" {
		if err := s.appCarrierTurn(p, sid, wire); err != nil {
			s.logf("ответ не доставлен в app-server %s: %v", sid, err)
			return false
		}
		return true
	}
	recs := s.bindsAll()
	if term := s.sayTermOf(sid, recs); term != "" {
		if err := chatSend(term, wire); err != nil {
			s.logf("ответ не подался в окно %s носителя %s, иду дальше: %v", term, sid, err)
		} else {
			return true
		}
	}
	if peer, ok := s.peers()[sid]; ok {
		err := peerSay(peer.Sock, wire)
		if err == nil {
			s.logf("ответ уехал в сокет носителя %s (pid %d, %s)", sid, peer.PID, peerWord(peer))
			return true
		}
		if errors.Is(err, errPeerNoAck) {
			// Клиент принял байты, но не подтвердил доставку: событийный цикл
			// стоит, и резюм поверх живого процесса завёл бы второго агента.
			// Клин снимается кнопкой на плашке разговора, а ответ уйдёт
			// запасной безадресной строкой.
			s.markDeaf(peer.Sock)
			s.logf("клиент носителя %s не подтвердил доставку ответа (pid %d): %v", sid, peer.PID, err)
			return false
		}
		s.logf("сокет носителя %s не взял ответ, пробую подъём: %v", sid, err)
	}
	info, ok := findSession(s.transcriptRoots(), p.Path, sid)
	if !ok {
		return false
	}
	// Недоставленные реплики едут в вводной вместе с ответом, как у реплики
	// кончившегося разговора: потерянная это молчание в ответ на сказанное.
	order := withLost(s.lostSaid(sid, info.mod), wire)
	if _, err := s.liftChat(liftOrder{
		found: p, info: info, sid: sid, task: task, tmux: sessions.Last(recs[sid]).Tmux,
		text: order, road: movedRoadSay, reason: "резюм чата", reasonNew: "свежая сессия чата",
	}); err != nil {
		s.logf("подъём носителя %s под ответ не удался: %v", sid, err)
		return false
	}
	s.logf("ответ вручённого вопроса поднял продолжение носителя %s", sid)
	return true
}
