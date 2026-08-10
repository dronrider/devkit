package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
)

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
	projects, errs := scanProjects(s.cfg.Roots)
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
// Ошибки не кэшируются вовсе: «taskctl не нашёлся» это причина, а не ответ, и
// поднятый бинарь обязан доезжать до экрана сразу, а не по выходе срока.
func (s *server) projectBoard(dir string) (json.RawMessage, error) {
	stamp, stamped := boardStamp(dir)
	now := s.now()
	if stamped {
		s.mu.Lock()
		e, hit := s.boards[dir]
		s.mu.Unlock()
		if hit && e.stamp == stamp && now.Sub(e.born) < boardTTL {
			return e.raw, nil
		}
	}
	raw, err := boardJSON(dir)
	if err != nil || !stamped {
		return raw, err
	}
	s.mu.Lock()
	s.boards[dir] = boardEntry{raw: raw, stamp: stamp, born: now}
	s.mu.Unlock()
	return raw, nil
}

// inParallel гоняет n дел разом, но не больше workers штук сразу.
func inParallel(workers, n int, fn func(i int)) {
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
