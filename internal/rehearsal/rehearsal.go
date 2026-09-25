// Package rehearsal держит ворот обкатки сценария. Выходов у задачи три
// (перевод в Check, слияние ветки и закрытие строки), а рубеж один: пока ворот
// стоял только на move check, задача уезжала в архив и в main мимо обкатки
// (DK-685). Разбор отметки и слова отказа живут здесь, чтобы у taskctl и
// shipctl не завелось двух расходящихся копий: копия, отставшая на строку,
// пускает ровно тем выходом, где проверки нет.
package rehearsal

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// Mark судит одно наличие отметки: обкатка прошла либо ворот погашен
// пометкой-исключением. Такой мерки хватает выходу, где свежесть спрашивали
// раньше. Закрытие идёт после Check, к тому часу в main приехали чужие
// слияния, и сверка коммита отбивала бы его на ровном месте, требуя обкатки
// кода, которого задача не писала.
func Mark(id, doc, again string) error {
	if taskform.Exception(doc, taskform.GateRehearsal) {
		return nil
	}
	if taskform.Rehearsed(doc) {
		return nil
	}
	return missing(id, again)
}

// Fresh это тот же ворот со сверкой свежести: отпечаток обкатанного сценария и
// коммит прогона. Стоит он там, где задача переходит рубеж впервые, на
// слиянии и на переводе в Check. Вне git сверять не с чем, и ворот
// довольствуется самой отметкой: доска живёт и в корп-контуре, где код лежит
// отдельно от неё.
func Fresh(root, id, doc, again string) error {
	if taskform.Exception(doc, taskform.GateRehearsal) {
		return nil
	}
	mark, print, ok := taskform.RehearsalStamp(doc)
	if !ok {
		return missing(id, again)
	}
	// Отпечаток сценария сверяется первым. Правка шагов лежит в том же файле
	// задачи, что и запись прогона, и по именам файлов эти коммиты
	// неразличимы: подменённый шаг проезжал бы под отметкой прогона, который
	// его не видел.
	if now := taskform.ScenarioPrint(doc); now != print {
		return fmt.Errorf("%s: сценарий менялся после обкатки (отпечаток отметки %s, у нынешнего текста %s): прогнать «taskctl rehearse %s» заново и повторить %s",
			id, print, now, id, again)
	}
	// Отметка привязана к коммиту, на котором шёл прогон. После обкатки ветка
	// уезжает вперёд, и вчерашняя отметка ручалась бы за сегодняшний код.
	head, err := gitLine(root, "rev-parse", "HEAD")
	if err != nil || head == "" {
		return nil
	}
	if strings.HasPrefix(head, mark) || onlyTaskDocSince(root, mark) {
		return nil
	}
	return fmt.Errorf("%s: отметка обкатки стоит на коммите %s, а HEAD уже %s: после прогона в ветку приехал код, которого обкатка не видела, прогнать «taskctl rehearse %s» заново и повторить %s",
		id, mark, head[:min(len(head), 12)], id, again)
}

// missing собирает отказ ворот без отметки: одними словами на всех выходах,
// потому что чинится он тоже одной командой.
func missing(id, again string) error {
	return fmt.Errorf("%s: сценарий не обкатан в чистом окружении, а проверять по нему будут всерьёз: прогнать «taskctl rehearse %s» (свежее дерево, временный HOME, вывод ляжет в «Проверку») и повторить %s; где шаги без выката не гоняются, загасить ворот пометкой «- Исключение: обкатка (причина)» в docs/tasks/%s.md",
		id, id, again, id)
}

// onlyTaskDocSince отвечает, что после обкатки в ветку приехали одни правки
// файла самой задачи. Сама запись прогона это такой коммит: обкатка пишет
// вывод и отметку в файл задачи, коммит с ними уезжает следом, и отметка
// устаревала бы ровно в ту минуту, когда её положили. Правку кода такой разбор
// не прощает: там в диффе коммита стоят чужие пути.
func onlyTaskDocSince(root, mark string) bool {
	out, err := gitLine(root, "log", "--format=%H", mark+"..HEAD")
	if err != nil {
		return false
	}
	shas := strings.Fields(out)
	if len(shas) == 0 {
		return false
	}
	files, err := gitLine(root, append([]string{"show", "--name-only", "--format=", "--no-renames"}, shas...)...)
	if err != nil {
		return false
	}
	for _, p := range strings.Fields(files) {
		if !boardDoc(p) {
			return false
		}
	}
	return true
}

// boardDoc: путь ведёт в доску или в запись задачи. Такой файл кода не несёт, и
// обкатка, прошедшая до него, ручается за тот же код.
//
// Сама доска сюда входит наравне с записями. Перевод строки соседа по поезду
// правит docs/TASKS.md и docs/tasks/<ID>.md одним коммитом, и без доски
// послабление накрывало бы половину случая.
//
// Раньше тут стоял файл одной названной задачи, и поезд из нескольких строк
// через ворота не проходил вовсе. Запись обкатки ложится в файл своей задачи и
// коммитится, а соседу по составу этот коммит уже чужой: обкатали первую,
// закоммитили, обкатали вторую, и отметка первой числилась протухшей (DK-1161).
func boardDoc(p string) bool {
	if p == "docs/TASKS.md" || p == "docs/TASKS-archive.md" {
		return true
	}
	return strings.HasPrefix(p, "docs/tasks/") && strings.HasSuffix(p, ".md")
}

func gitLine(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
