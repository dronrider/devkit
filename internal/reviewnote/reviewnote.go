// Package reviewnote держит след ревью там, где доски нет. На доске след живёт
// строкой в разделе «Ревью» файла задачи, и по ней ворот слияния отличает
// правку, прошедшую по скиллу review, от правки, прошедшей мимо. Вне доски
// файла задачи не бывает: правка едет в MR чужого трекера или в ветку без
// трекера вовсе, а ревью ей нужно то же самое. Место следу тогда git-заметка на
// коммите: она живёт рядом с кодом, переживает ребейз ветки не хуже сообщения
// коммита и не просит завести файл там, где его некуда положить.
//
// Пишет заметку `taskctl review level` и `taskctl review clean`, читает ворот
// пуша (`shipctl push --check-only`). Разбор строки лежит тут один на обоих:
// разойдись копии критерия, ворот отбивал бы пуш, след которому поставила
// соседняя утилита.
package reviewnote

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/gitrun"
	"github.com/dronrider/devkit/internal/taskform"
)

// Ref это имя ссылки заметок. Своя ссылка, а не notes/commits по умолчанию:
// чужие заметки коммита (замеры, ссылки на сборки) следом ревью не считаются,
// и мешать их в одну кучу значит принимать за ревью что угодно.
const Ref = "review"

// roundRe узнаёт строку круга ревью: «Круг 2 до a1b2c3d: пояснение». Круг это
// заход ревью по коду, доехавшему до этого коммита, и номер его растёт по
// заметкам предков. Судится голова строки, хвост с sha и пояснением свободный.
var roundRe = regexp.MustCompile(`^Круг ([0-9]+)( |$)`)

// LevelLine собирает строку уровня тщательности. Форма та же, что у строки в
// файле задачи (taskform.IsReviewLevel её и узнаёт): одна запись читается
// одинаково и с доски, и из заметки.
func LevelLine(level int, sha, reason string) string {
	return fmt.Sprintf("Уровень %d до %s: %s", level, sha, reason)
}

// RoundLine собирает строку круга, прошедшего без замечаний.
func RoundLine(round int, sha, note string) string {
	line := fmt.Sprintf("Круг %d до %s", round, sha)
	if note = strings.TrimSpace(note); note != "" {
		return line + ": " + note
	}
	return line + ": без замечаний"
}

// IsRound говорит, что строка это круг ревью.
func IsRound(line string) bool {
	return roundRe.MatchString(strings.TrimSpace(line))
}

// IsTrace говорит, что строка это след ревью: уровень тщательности либо круг.
// Ворот пуша судит по нему, а не по факту непустой заметки: заметка с чужим
// текстом ревью не заменяет.
func IsTrace(line string) bool {
	return taskform.IsReviewLevel(line) || IsRound(line)
}

// Has говорит, несёт ли коммит rev заметку со следом ревью.
func Has(root, rev string) (bool, error) {
	text, err := Read(root, rev)
	if err != nil {
		return false, err
	}
	for _, ln := range strings.Split(text, "\n") {
		if IsTrace(ln) {
			return true, nil
		}
	}
	return false, nil
}

// Read отдаёт текст заметки ревью на коммите rev. Заметки нет это пустая строка
// без ошибки: у большинства коммитов её и не бывает. А вот нечитаемый коммит
// (пустой репозиторий, чужой sha, каталог вне git) это отказ: молчание тут
// неотличимо от «ревью не было», и ворот пропускал бы код, которого не видел.
func Read(root, rev string) (string, error) {
	if _, err := git(root, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); err != nil {
		return "", fmt.Errorf("в %s не читается коммит %s, заметку ревью брать не с чего: %v", root, rev, err)
	}
	out, err := git(root, "notes", "--ref="+Ref, "show", rev)
	if err != nil {
		return "", nil
	}
	return strings.TrimRight(out, "\n"), nil
}

// Write кладёт строку заметкой на коммит rev, переписывая прежнюю. Переписывает
// намеренно: пересмотр уровня по ходу ревью и следующий круг это та же запись с
// новым основанием, а не вторая заметка рядом.
func Write(root, rev, line string) error {
	if _, err := git(root, "notes", "--ref="+Ref, "add", "-f", "-m", line, rev); err != nil {
		return fmt.Errorf("заметка ревью не записалась на %s в %s: %v", rev, root, err)
	}
	return nil
}

// NextRound говорит, каким по счёту идёт круг ревью на коммите rev. Считается
// он по заметкам предков, а не по заметке одного коммита: круг после правок
// ложится на новый коммит, и счёт с нуля на каждом коммите звал бы вторым
// кругом любой заход. Заметки читаются одним обходом истории (git log --notes),
// процесса на коммит тут нет.
func NextRound(root, rev string) (int, error) {
	out, err := git(root, "log", "--notes="+Ref, "--format=%N%x00", rev)
	if err != nil {
		return 0, fmt.Errorf("в %s не читается история %s, счёт кругов ревью брать не с чего: %v", root, rev, err)
	}
	max := 0
	for _, ln := range strings.Split(strings.ReplaceAll(out, "\x00", "\n"), "\n") {
		if m := roundRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			n := 0
			fmt.Sscanf(m[1], "%d", &n)
			if n > max {
				max = n
			}
		}
	}
	return max + 1, nil
}

func git(root string, args ...string) (string, error) {
	return gitrun.Run(root, args, gitrun.DefaultTimeout)
}
