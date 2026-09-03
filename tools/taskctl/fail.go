package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Признак провала проверки живёт суффиксом заголовка «[провал: причина]», по
// образцу причины блокировки: это состояние задачи здесь и сейчас, а не запись
// в журнале, и лежит оно там же, где статус, то есть в доске. Очередь выката
// считают по нему обе утилиты, читая одну строку, а не десяток файлов задач,
// и пометку видно глазом в list, show и в диффе коммита доски.
// Скобки в причине запрещены тем же checkReason, что и у блокировки: иначе
// разбор заголовка не отличил бы её от следующего суффикса.
var failSufRe = regexp.MustCompile(`\s*\[провал: [^|\[]*\]\s*$`)

type FailParams struct {
	ID, Reason string
	Clear      bool
	Commit     CommitOpts
}

// cmdFail держит исход проверки задачи из Check. Провал значит сломанный прод:
// строка возвращается в In progress с пометкой, и пока пометка висит, shipctl
// не сливает и не выкатывает ничего чужого (RULES.board.md, «Ветки, ревью и
// деплой» п. 7). Второй исход, приёмка с замечаниями, это обычный
// move в in-progress: работа принята, очередь свободна, доработка идёт
// следующим кругом.
func cmdFail(root string, p FailParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(p.ID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", p.ID)
	}
	base, deps, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
	paths := []string{filepath.Join("docs", "TASKS.md")}
	if p.Clear {
		if failSuf == "" {
			return "", fmt.Errorf("у %s нет признака провала проверки, снимать нечего", p.ID)
		}
		row.Title = joinTitle(base, deps, acceptSuf, "", blockSuf)
		b.Lines[row.LineIdx] = formatRow(row)
		if err := b.Save(); err != nil {
			return "", err
		}
		tail, err := p.Commit.apply(root, paths)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: признак провала снят, прод считается починенным%s", p.ID, tail), nil
	}
	if strings.TrimSpace(p.Reason) == "" {
		return "", fmt.Errorf("провалу обязателен --reason, одна строка чем сломан прод")
	}
	if err := checkReason(p.Reason); err != nil {
		return "", err
	}
	if failSuf != "" {
		return "", fmt.Errorf("%s уже помечена провалом%s", p.ID, failSuf)
	}
	if row.Sect != SectCheck && row.Sect != SectInProgress {
		return "", fmt.Errorf("%s в %s: провал ставится задаче из Check или из In progress", p.ID, sectTitles[row.Sect])
	}
	moved := *row
	moved.Title = joinTitle(base, deps, acceptSuf, " [провал: "+p.Reason+"]", blockSuf)
	line := formatRow(&moved)
	from := row.Sect
	if from == SectCheck {
		if b, err = relocate(b, row, SectInProgress, &moved, line); err != nil {
			return "", err
		}
	} else {
		b.Lines[row.LineIdx] = line
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	// Повод громкий не меньше блокера: прод сломан, и в автономном режиме об
	// этом иначе некому узнать (RULES.board.md, «Ветки, ревью и деплой» п. 8).
	note := notify(root, reasonFail, p.ID, fmt.Sprintf("%s: %s провал проверки", filepath.Base(root), p.ID), p.Reason)
	tail, err := p.Commit.apply(root, paths)
	if err != nil {
		return "", err
	}
	// Уровень ревью и причина его выбора печатаются тут же: провал это тот
	// самый повод поднять тщательность, и он должен быть виден человеку, а не
	// только тому, кто полез читать файл задачи (DK-731).
	levelNote := "ревью шло без уровня"
	if lvl, reason, ok := reviewLevelReason(root, p.ID); ok {
		levelNote = fmt.Sprintf("ревью было уровня %d: %s", lvl, reason)
	}
	return fmt.Sprintf("%s: %s -> %s, провал проверки (%s), %s; очередь выката стоит, чинить: shipctl revert %s либо форвард-фикс и shipctl merge %s%s%s",
		p.ID, from, SectInProgress, p.Reason, levelNote, p.ID, p.ID, tail, note), nil
}
