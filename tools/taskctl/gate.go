package main

import (
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// branchOfTask повторяет опознание ветки задачи из shipctl
// (tools/shipctl/worktree.go): ветка называется по ID строчными, с
// хвостом-слагом или без (`dk-005`, `dk-005-worktree`). Общего пакета у утилит
// нет, а функция в две строки.
func branchOfTask(branch, id string) bool {
	b, low := strings.ToLower(branch), strings.ToLower(id)
	return b == low || strings.HasPrefix(b, low+"-")
}

// gitMainBranch называет ветку main или master, пустую строку вне git и там,
// где ни той, ни другой нет.
func gitMainBranch(root string) string {
	for _, b := range []string{"main", "master"} {
		if _, err := gitRevParse(root, "--verify", b); err == nil {
			return b
		}
	}
	return ""
}

// unmergedTaskBranch ищет живую ветку задачи, у которой есть коммиты, не
// доехавшие до main. Такая ветка значит, что код задачи лежит только у автора,
// каким бы ни был статус строки. Вне git и в корп-контуре, где доска отдельно
// от кода, ветки не найдётся, и рубеж молча пропускает: врать про несделанное
// слияние хуже, чем его не заметить.
func unmergedTaskBranch(root, id string) (branch string, ahead int) {
	main := gitMainBranch(root)
	if main == "" {
		return "", 0
	}
	out, err := exec.Command("git", "-C", root, "for-each-ref",
		"--format=%(refname:short)", "refs/heads").Output()
	if err != nil {
		return "", 0
	}
	for _, b := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if b == "" || b == main || !branchOfTask(b, id) {
			continue
		}
		cnt, err := exec.Command("git", "-C", root, "rev-list", "--count", main+".."+b).Output()
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(cnt)))
		if err != nil || n == 0 {
			continue
		}
		return b, n
	}
	return "", 0
}

// scenarioElsewhere отвечает, ведёт ли ссылка строки не в файл задачи, а в
// другой документ: LLD-задача описывает проверку прямо в дизайне
// (RULES.board.md, «Трекинг задач» п. 6), и файла задачи у неё может не быть.
// Заглушка вида «(LLD позже)» ссылкой не считается: смотреть по ней некуда.
func scenarioElsewhere(link, id string) bool {
	m := linkRe.FindStringSubmatch(link)
	if m == nil {
		return false
	}
	return path.Base(m[1]) != id+".md"
}

// checkGate держит два рубежа перевода в Check: готовый сценарий проверки и
// слитый код. Оба нарушения приходят от слабой модели, которая прозу правил не
// держит, а через move ходит обязательно, поэтому отказ называет причину и
// следующий шаг.
func checkGate(root string, row *Row) error {
	id := row.ID
	if !scenarioElsewhere(row.Link, id) {
		has, hasFile := hasScenario(root, id)
		rel := "docs/tasks/" + id + ".md"
		switch {
		case !hasFile:
			return fmt.Errorf("%s: в Check пускают только с готовым сценарием проверки, а файла %s нет: завести «taskctl file %s», описать шаги и ожидаемый итог разделом «Сценарий проверки» и повторить move", id, rel, id)
		case !has:
			return fmt.Errorf("%s: в %s нет раздела «Сценарий проверки», без него в Check нельзя: описать шаги и ожидаемый итог (RULES.board.md, «Трекинг задач» п. 6) и повторить move", id, rel)
		}
	}
	if br, ahead := unmergedTaskBranch(root, id); br != "" {
		return fmt.Errorf("%s: ветка %s не слита, впереди main её коммитов %d, а Check значит, что код уже доехал до пользователя: сначала «shipctl merge %s», он и переведёт строку", id, br, ahead, id)
	}
	// Не агентский вид требует раздел «Приёмка» с перебором обходов
	// (LLD DK-292, решение 4): машина считает строки, ревьювер судит причины.
	if kind := acceptOf(row.Title); kind != acceptAgent {
		if err := acceptGate(root, id, kind); err != nil {
			return err
		}
	}
	return nil
}

// acceptGate проверяет, что у не агентского вида в файле задачи стоит раздел
// «Приёмка» с ключом барьера из шести и строкой перебора на каждый обход этого
// барьера. Судить убедительность причины обхода машина не берётся, это работа
// ревьювера; здесь только счёт по закрытому списку.
func acceptGate(root, id, kind string) error {
	rel := "docs/tasks/" + id + ".md"
	text, found, ok := acceptanceSection(root, id)
	if !ok {
		return fmt.Errorf("%s: вид приёмки %s, а файла %s нет: вид с барьером требует раздел «Приёмка» в файле задачи (LLD DK-292, решение 3)", id, kind, rel)
	}
	if !found {
		return fmt.Errorf("%s: вид приёмки %s, а раздела «Приёмка» в %s нет: назвать барьер и перебрать обходы (LLD DK-292, решение 1) и повторить move", id, kind, rel)
	}
	barrier, bypasses := parseAcceptance(text)
	if barrier == "" {
		return fmt.Errorf("%s: в разделе «Приёмка» нет строки «- барьер «<ключ>»:», а вид %s её требует", id, kind)
	}
	want, known := acceptBarriers[barrier]
	if !known {
		return fmt.Errorf("%s: барьер «%s» в разделе «Приёмка» не из закрытого списка (глаза, доступ, необратимость, секрет, согласие, событие)", id, barrier)
	}
	if bypasses != want {
		return fmt.Errorf("%s: у барьера «%s» обходов %d, а перебор в «Приёмка» имеет строк %d (нужно столько же, по строке на обход с исходом)", id, barrier, want, bypasses)
	}
	return nil
}

// lintUnmerged ловит обратную сторону того же рубежа: строка уже в Check, а
// ветка с её ID живёт с неслитыми коммитами. Так выглядит перевод в обход
// ворот (правку доски руками отменить некому) и брошенное на полпути слияние.
func lintUnmerged(root string, b *Board, bp string) []string {
	var finds []string
	for _, r := range b.Sects[SectCheck].Rows {
		if br, ahead := unmergedTaskBranch(root, r.ID); br != "" {
			finds = append(finds, fmt.Sprintf("%s:%d: %s в Check, а ветка %s не слита (коммитов впереди main: %d), слить: shipctl merge %s",
				bp, r.LineIdx+1, r.ID, br, ahead, r.ID))
		}
	}
	return finds
}
