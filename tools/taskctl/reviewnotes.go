package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/reviewnote"
)

// Режим заметки: ревью там, где доски нет. Правка агента ездит не только по
// доске (MR в чужом трекере, ветка без трекера вовсе), а скилл review один на
// все происхождения правки. Файла задачи в таком репозитории завести негде, и
// след ревью ложится git-заметкой на коммит, по которому ревью шло. Читает её
// ворот пуша (shipctl push --check-only), пишет level и clean; add, resolve и
// stats остаются доске, там у замечаний есть номера и место для разговора.
//
// Ярлык (номер MR, ключ тикета) едет отдельной строкой заметки, а не внутри
// строки следа: строка уровня в заметке читается тем же критерием, что строка
// в файле задачи, и вставка в её голову увела бы ворот от следа.

const noteLabel = "Ярлык: "

// noteRoot говорит, куда класть след ревью: пустая строка значит доску, непустая
// значит git-дерево, где ревью поедет заметкой. Порядок такой: доска вверх от
// dir главнее, и в проекте с доской ничего не меняется.
func noteRoot(dir string) string {
	if _, err := findRoot(dir); err == nil {
		return ""
	}
	out, err := gitRun(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// noteCommit отбивает флаги коммита: коммитить в режиме заметки нечего, доски
// нет, а заметка живёт вне рабочего дерева. Молча проглоченный -m оставил бы
// вызывающего в уверенности, что запись уехала в историю.
func noteCommit(c CommitOpts) error {
	if c.Msg != "" || c.Push {
		return fmt.Errorf("доски тут нет, след ревью едет git-заметкой: коммитить и пушить нечего, -m и --push не работают")
	}
	return nil
}

// noteTag собирает текст заметки: строка следа и ярлык под ней.
func noteTag(line, id string) string {
	return line + "\n" + noteLabel + id
}

// noteVerb говорит, записана заметка впервые или переписана поверх прежнего
// следа. Разницу видно в ответе команды: повторный вызов на том же коммите это
// обычное дело (пересмотр уровня, второй круг), и человеку стоит знать, что
// прежняя запись ушла.
func noteVerb(root string) string {
	if has, err := reviewnote.Has(root, "HEAD"); err == nil && has {
		return "переписан"
	}
	return "записан"
}

func noteID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("жду ярлык правки: номер MR, ключ тикета или имя ветки")
	}
	if strings.Contains(id, "\n") {
		return "", fmt.Errorf("ярлык пишется одной строкой")
	}
	return id, nil
}

// cmdNoteLevel пишет уровень тщательности ревью заметкой на HEAD. Проверки те
// же, что у записи в файл задачи: шкала 0-3 и обязательная причина на всех
// уровнях, включая нулевой, потому что незаписанный пропуск неотличим от
// забытого ревью.
func cmdNoteLevel(root, id string, level int, reason string, c CommitOpts) (string, error) {
	if err := noteCommit(c); err != nil {
		return "", err
	}
	id, err := noteID(id)
	if err != nil {
		return "", err
	}
	if level < 0 || level > 3 {
		return "", fmt.Errorf("уровень %d вне шкалы, жду 0-3 (шкала в скилле review)", level)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", fmt.Errorf("жду причину уровня: review level <ярлык> <0-3> \"причина\"")
	}
	if strings.Contains(reason, "\n") {
		return "", fmt.Errorf("причина пишется одной строкой")
	}
	sha, err := headSha(root)
	if err != nil {
		return "", err
	}
	verb := noteVerb(root)
	if err := reviewnote.Write(root, "HEAD", noteTag(reviewnote.LevelLine(level, sha, reason), id)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: уровень ревью %d до %s %s заметкой git (ref %s)", id, level, sha, verb, reviewnote.Ref), nil
}

// cmdNoteClean пишет заметкой круг ревью, прошедший без замечаний. Номер круга
// считается по заметкам предков: круг после правок ложится на новый коммит.
func cmdNoteClean(root, id, note string, c CommitOpts) (string, error) {
	if err := noteCommit(c); err != nil {
		return "", err
	}
	id, err := noteID(id)
	if err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if strings.Contains(note, "\n") {
		return "", fmt.Errorf("пояснение пишется одной строкой")
	}
	sha, err := headSha(root)
	if err != nil {
		return "", err
	}
	round, err := reviewnote.NextRound(root, "HEAD")
	if err != nil {
		return "", err
	}
	verb := noteVerb(root)
	if err := reviewnote.Write(root, "HEAD", noteTag(reviewnote.RoundLine(round, sha, note), id)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: круг %d ревью без замечаний до %s %s заметкой git (ref %s)", id, round, sha, verb, reviewnote.Ref), nil
}

// cmdNoteShow печатает заметку HEAD целиком. Ярлык в ответе не сверяется с
// заметкой: заметка на коммите одна, и показать надо то, что там лежит, а не
// то, что спросили.
func cmdNoteShow(root, id string) (string, error) {
	if _, err := noteID(id); err != nil {
		return "", err
	}
	text, err := reviewnote.Read(root, "HEAD")
	if err != nil {
		return "", err
	}
	sha, err := headSha(root)
	if err != nil {
		return "", err
	}
	if text == "" {
		return fmt.Sprintf("на %s нет заметки ревью (ref %s): ревью тут не шло или след не поставлен", sha, reviewnote.Ref), nil
	}
	return text, nil
}
