// Контроль задачи по сроку (DK-1470): строка, взятая на карандаш суффиксом
// «[контроль: N]», за N дней до срока получает проверку работ. Проверяющий
// кладёт риски срыва черновиком и собирает напоминание исполнителю, а подъём
// идёт тем же отбором и теми же носителями, что прогон сценария после выката:
// у контроля нет своей лестницы и своих правил независимости, вторая копия
// разошлась бы с первой на первой правке. Свой у контроля только повод
// (окно у срока, а не выкат), заказ и выбор инспектора из команды.

package checkrun

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/dronrider/devkit/internal/control"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// ControlOrderHead это начало заказа инспектору контроля. По нему оболочка
// конвейера отличает контрольную сессию от прогона сценария.
const ControlOrderHead = "Проверь работы по "

// ControlNoLift это слова отказа контроля, когда инспектора поднять нечем.
const ControlNoLift = "проверка контроля не поднята"

// ControlNeed решает, нужен ли строке контрольный подъём сейчас. Контроль
// живёт на строках в работе: In progress это идущая работа, Backlog работа,
// которую к сроку не начали, и то и другое риск срыва. Check и Blocked контроль
// не трогает: Check уже проверяется своим прогоном, Blocked держит собственную
// механику пробуждения. Пустой ответ значит «подъём нужен», непустой это
// причина «поднимать нечего».
func ControlNeed(now time.Time, row Row) string {
	if row.Sect != "in-progress" && row.Sect != "backlog" {
		return "строка не в работе (" + row.Section + "), контроль проверяет работы, а не сдачу"
	}
	if row.Control < 0 {
		return "строка без контроля"
	}
	if row.Due == "" {
		return "контроль без срока: линтер зовёт поставить срок либо снять контроль"
	}
	if !control.Window(row.Due, row.Control, now.Format(time.DateOnly)) {
		return fmt.Sprintf("окно контроля ещё не настало: проверка за %d дней до срока %s", row.Control, row.Due)
	}
	return ""
}

// ControlOverdue считает просрочку контроля словами заказа: ноль и меньше
// значит «срок не просрочен», и дожимать в заказе нечего.
func ControlOverdue(now time.Time, row Row) int {
	return control.Overdue(row.Due, now.Format(time.DateOnly))
}

// inspectorChoice решает модель проверяющего контроля. Инспектор это участник
// команды с ролью inspect (DK-1468 объявила роль, контроль её первый
// потребитель): агентского инспектора контроль поднимает его моделью, человека
// сессией не поднимают вовсе. Инспектора, совпавшего с исполнителем разработки,
// сменяет лестница: инспектор смотрит работу со стороны. Без инспектора в
// команде выбор тот же, что у прогона сценария.
func inspectorChoice(dev string, known bool) Choice {
	list, err := team.Load(stage.Home())
	if err != nil {
		return Choice{}
	}
	for _, p := range list {
		if !p.HasRole(team.RoleInspect) || p.Kind != team.Agent || p.Model == "" {
			continue
		}
		if known && sameModel(p.Model, dev) {
			continue
		}
		return Choice{Tier: p.Tier, Model: p.Model}
	}
	return Choice{}
}

// ControlOrder это заказ инспектору контроля: проверить работы к сроку, риски
// положить черновиком, напоминание исполнителю собрать текстом для канала.
// Слова про потолки DK-400 в заказе стоят не для красоты: контроль поднимает
// сессию без человека, и единственное, что держит его внутри потолков, это
// слова, по которым скиллы доски разводят работу.
func ControlOrder(id, dev string, overdue int) string {
	out := ControlOrderHead + id + " к сроку: состояние работ из файла задачи, доски и живых сессий." +
		" Риски срыва положи черновиком `taskctl draft --prio high \"Риски срыва сроков по " + id + ": ...\"`," +
		" отдельных строк не заводи." +
		" Напоминание исполнителю собери `taskctl remind " + id + " --note \"...\"` и текст положи в тот же черновик:" +
		" модель пишет текст, человек копирует его в канал." +
		" Потолки DK-400 объезжать нельзя: вопросов человеку не больше двух на цель и только штатным инструментом ожидания," +
		" риски кладутся черновиком, дерево задачи не заводи, работай в основном чекауте."
	if overdue > 0 {
		out += fmt.Sprintf(" Срок просрочен на %d дней: это дожим, ходи к исполнителю репликами в чат задачи,"+
			" пока работы не сдвинутся либо срок не пересмотрен.", overdue)
	}
	if dev != "" {
		out += " Разработку вёл " + dev + "."
	}
	return out
}

// ControlRun проходит по названным строкам контроля, а без имён по всем
// строкам работы с контролем. Форма исходов та же, что у прогона сценария:
// на каждую строку приходится свой отчёт.
func ControlRun(c Carrier, root string, rows map[string]Row, ids []string, now time.Time) []Report {
	if len(ids) == 0 {
		for id, row := range rows {
			if row.Sect == "in-progress" || row.Sect == "backlog" {
				if row.Control >= 0 {
					ids = append(ids, id)
				}
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return []Report{{Line: "строк с контролем нет: поднимать нечего"}}
		}
	}
	var out []Report
	for _, id := range ids {
		out = append(out, controlOne(c, root, rows, id, now))
	}
	return out
}

func controlOne(c Carrier, root string, rows map[string]Row, id string, now time.Time) Report {
	row, ok := rows[id]
	if !ok {
		return Report{ID: id, Line: id + ": строки нет на доске проекта " + filepath.Base(root), Failed: true}
	}
	if why := ControlNeed(now, row); why != "" {
		return Report{ID: id, Line: id + ": " + NoNeed + ", " + why}
	}
	if line, failed := c.Ready(id); line != "" {
		return Report{ID: id, Line: id + ": " + line, Failed: failed}
	}
	tier, why := c.Tier(id)
	dev, known := DevExecutor(root, id)
	var ch Choice
	if multi, ok := c.(interface{ Ladders() []Ladder }); ok {
		ch = ChooseAvailable(tier, multi.Ladders(), dev, known)
	} else {
		ch = Choose(tier, c.Ladder(), dev, known)
	}
	// Инспектор команды сильнее лестницы, но не сильнее независимости: он
	// смотрит работу со стороны, и инспектор, совпавший с исполнителем
	// разработки, сменяется лестницей.
	if insp := inspectorChoice(dev, known); insp.Model != "" {
		ch = insp
		why = "инспектор контроля из команды, ярус " + insp.Tier
	}
	if ch.Refusal != "" {
		return Report{ID: id, Line: id + ": " + ControlNoLift + ", " + ch.Refusal, Failed: true}
	}
	p := Plan{Row: row, Dev: dev, DevKnown: known, Choice: ch, TierWhy: why,
		Order: ControlOrder(id, dev, ControlOverdue(now, row)), Again: Again(id)}
	rep := c.Raise(p)
	rep.ID = id
	return rep
}
