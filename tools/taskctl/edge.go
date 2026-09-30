package main

import (
	"strings"

	"github.com/dronrider/devkit/internal/merged"
)

// edges отвечает снятостью рёбер «после» доски (решение 2 LLD DK-933). Правило
// живёт в internal/merged, общее с shipctl, а здесь к нему прибавлена доска:
// заголовок строки предпосылки и архив. Лог main читается одной книгой на
// команду, и `list --json` с десятками рёбер не зовёт git на каждое заново.
type edges struct {
	b    *Board
	arch *Archive
	book *merged.Book
}

func newEdges(root string, b *Board, arch *Archive) *edges {
	e := &edges{b: b, arch: arch, book: merged.Open(root)}
	e.book.Expect(boardIDs(b))
	return e
}

// boardIDs это ID, про которые обход доски спросит признак «слита»: сами строки
// и предпосылки их рёбер. Книга читает их пакетом, и обход доски обходится
// десятком подпроцессов git вместо восьмидесяти с лишним. Это считается не
// работой git, а ценой запуска процесса. Под соседним полным прогоном запуск
// дороже обычного в три-пять раз, и понижение приоритета не берёт его (замер
// DK-1168).
func boardIDs(b *Board) []string {
	if b == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, sect := range b.Sects {
		for _, r := range sect.Rows {
			add(r.ID)
			_, deps, _, _, _, _, _ := splitTitle(r.Title)
			for _, d := range deps {
				add(d)
			}
		}
	}
	return out
}

// of это снятость ребра на предпосылку dep.
func (e *edges) of(dep string) merged.Edge {
	p := merged.Prereq{ID: dep, Archived: e.arch != nil && e.arch.has(dep)}
	if r := e.b.find(dep); r != nil {
		p.Title = r.Title
	}
	return e.book.Edge(p)
}

// held возвращает неснятые рёбра строки в порядке маркера.
func (e *edges) held(r *Row) []merged.Edge {
	_, deps, _, _, _, _, _ := splitTitle(r.Title)
	var out []merged.Edge
	for _, d := range deps {
		if ed := e.of(d); !ed.Lifted() {
			out = append(out, ed)
		}
	}
	return out
}

// heldIDs это ID неснятых рёбер строки.
func (e *edges) heldIDs(r *Row) []string {
	var out []string
	for _, ed := range e.held(r) {
		out = append(out, ed.Dep)
	}
	return out
}

// startable отвечает, пустили бы ворота старта строку по ребру на dep хоть
// раз: предпосылка в архиве либо её работа ложилась в main, и вид её не user.
// По нему lint судит начатую строку. Ребро, вернувшееся откатом или провалом,
// ей не находка: такую строку держат ворота слияния (решение 3 LLD DK-933).
func (e *edges) startable(dep string) (bool, string) {
	ed := e.of(dep)
	if ed.State == merged.StateClosed {
		return true, ""
	}
	if r := e.b.find(dep); r != nil && acceptOf(r.Title) == acceptUser {
		return false, dep + " с приёмкой user держит ребро до закрытия"
	}
	ever, err := e.book.Ever(dep)
	if err != nil {
		return false, "признак «слита» у " + dep + " не спрошен: " + err.Error()
	}
	if !ever {
		return false, dep + " ни разу не сливалась"
	}
	return true, ""
}

// edgeWords это неснятые рёбра словами для отказа: «DK-1 не слита: ...».
func edgeWords(held []merged.Edge) string {
	var out []string
	for _, ed := range held {
		out = append(out, ed.Why)
	}
	return strings.Join(out, "; ")
}
