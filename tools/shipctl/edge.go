package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/merged"
)

// Ребро «после» глазами shipctl (решение 3 LLD DK-933). Снятость считает
// internal/merged, та же функция, что у ворот старта в taskctl, а строку
// предпосылки и архив даёт здешний терпимый разбор доски.

func prereqOf(root string, b *board, id string) (merged.Prereq, error) {
	p := merged.Prereq{ID: id}
	if r := b.rowOf(id); r != nil {
		p.Title = r.Title
	}
	arch, err := archiveHas(root, id)
	if err != nil {
		return p, err
	}
	p.Archived = arch
	return p, nil
}

// edgeGate держит слияние строки, у которой ребро «после» не снято. Так бывает,
// когда зависимая стартовала на коде предпосылки, а ту потом откатили: ветка
// несёт код, которого в main уже нет, и слияние встретило бы откаченное
// основание на тестах, если повезёт, а если нет, то молча. Отказ называет
// предпосылку и печатает готовую парковку, строку потом поднимет обход ждущих
// на новом слиянии или закрытии предпосылки (DK-932).
func edgeGate(root string, b *board, id string) error {
	r := b.rowOf(id)
	if r == nil {
		return nil
	}
	book := merged.Open(root)
	var why []string
	park := ""
	for _, d := range merged.Deps(r.Title) {
		p, err := prereqOf(root, b, d)
		if err != nil {
			return err
		}
		e := book.Edge(p)
		if e.Lifted() {
			continue
		}
		why = append(why, e.Why)
		if park != "" {
			continue
		}
		if e.Park != "" {
			park = fmt.Sprintf("taskctl move %s blocked --reason \"%s: %s\"", id, e.Park, e.Dep)
		} else {
			park = fmt.Sprintf("поправить маркер: taskctl dep rm %s %s", id, e.Dep)
		}
	}
	if len(why) == 0 {
		return nil
	}
	return fmt.Errorf("%s ждёт предпосылку, ребро «после» не снято (%s): ветка несёт основание, которого в main нет, слияние ждёт нового слияния или закрытия предпосылки; парковка: %s",
		id, strings.Join(why, "; "), park)
}

// dependentsInMain называет строки доски с ребром на id, чья работа уже лежит в
// main, словами признака «слита». Откат id их не трогает: по коммитам не
// видно, нужен ли их коду код id, и решает это тот, кто откатывает.
func dependentsInMain(root string, b *board, id string) ([]string, error) {
	book := merged.Open(root)
	var out []string
	for _, key := range []string{"in-progress", "check", "blocked", "backlog"} {
		for _, r := range b.sects[key] {
			if !hasDep(r.Title, id) {
				continue
			}
			v, err := book.Task(r.ID)
			if err != nil {
				return nil, err
			}
			if v.Merged {
				out = append(out, v.Said(r.ID))
			}
		}
	}
	return out, nil
}

func hasDep(title, id string) bool {
	for _, d := range merged.Deps(title) {
		if d == id {
			return true
		}
	}
	return false
}
