package main

import (
	"fmt"
	"io"
	"strings"
)

// sweepMarkers это тексты первой реплики, которыми узнаётся разговор,
// поднятый конвейером до признака `hidden` (DK-847): накопившееся уходит в
// архив разбором первой реплики, а не подгоняется под новый признак задним
// числом (решено человеком 2026-09-07). Список короткий и предметный:
// совпадение по нему должно быть надёжным без разбора остального текста,
// разговоры без явного маркера в кандидаты не попадают и остаются на глаз
// человеку.
var sweepMarkers = []string{
	// checkrun.OrderHead (internal/checkrun): прогон сценария проверки, зовут
	// выкат shipctl, тик `devkitctl watch` и `dashboard check`.
	"Прогони агентскую часть сценария проверки ",
	// roundOrder (round.go): второй круг ревью, зовёт тик сторожка.
	"Второй круг ревью ",
	// self.prompt в goal-run.py: каждый виток цикла цели, тот же текст и на
	// первом витке, и на возобновлении после стопа.
	"продолжай цель ",
}

// sweepMarker ищет маркер в первой реплике. Пустая строка вторым значением
// значит, что ни один маркер не подошёл: разговор не кандидат.
func sweepMarker(first string) string {
	for _, m := range sweepMarkers {
		if strings.Contains(first, m) {
			return m
		}
	}
	return ""
}

// sweepCandidate это одна строка разбора: кому она принадлежит, чем поднята,
// первые слова для глаза человека.
type sweepCandidate struct {
	id, project, marker, first string
}

// sweepScan ищет кандидатов на уборку среди открытых (не убранных в архив,
// не розданных делегированием) разговоров машины. Список полный, без окна
// свежести: старое ровно то, что и нужно найти.
func (s *server) sweepScan() []sweepCandidate {
	list, _ := s.chatEntriesAll(0, chatWindow{})
	list = withoutHandedOut(list)
	out := make([]sweepCandidate, 0, len(list))
	for _, e := range list {
		if e.Archived || e.Hidden {
			continue
		}
		if m := sweepMarker(e.First); m != "" {
			out = append(out, sweepCandidate{id: e.ID, project: e.Project, marker: m, first: firstWords(e.First)})
		}
	}
	return out
}

// firstWords режет первую реплику для печати в списке кандидатов: строка
// должна поместиться в терминал, а не пересказывать заказ целиком.
func firstWords(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const limit = 100
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "..."
}

// cmdChatSweep это вход команды `dashboard chatsweep`: без флага печатает
// список кандидатов и ничего не трогает, `--apply` уносит названные ID в
// архив той же ручкой, что кнопка панели. Разбор первой реплики этим и
// остаётся разовой уборкой (DK-847): список смотрит и подтверждает человек,
// признак `hidden` эти записи не получают, потому что подняты они были ещё
// до него.
func cmdChatSweep(home string, apply []string, out io.Writer) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	s := newServer(cfg, nil, nil)
	if len(apply) > 0 {
		for _, id := range apply {
			if _, err := s.chatArchive(id, true); err != nil {
				fmt.Fprintf(out, "%s: в архив не ушёл, %s\n", id, err)
				continue
			}
			fmt.Fprintf(out, "%s: в архиве\n", id)
		}
		return nil
	}
	cands := s.sweepScan()
	if len(cands) == 0 {
		fmt.Fprintln(out, "кандидатов на уборку не нашлось")
		return nil
	}
	for _, c := range cands {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", c.id, c.project, c.marker, c.first)
	}
	fmt.Fprintf(out, "кандидатов: %d; в архив уносит `dashboard chatsweep --apply <ID>...` по проверенному списку\n", len(cands))
	return nil
}
