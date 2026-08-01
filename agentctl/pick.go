package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type verdict struct {
	Tier   string // ступень лестницы моделей: mini, base, pro, max
	Model  string // ярус, развёрнутый маппингом активного харнеса; «-», если разворачивать нечем
	Effort string // reasoning effort субагента: low, medium, high, xhigh
	Reason string
	Groom  bool // вердикт «исполнять рано»: сначала грумминг или разбивка
}

// pickTier выводит ярус из метаданных строки доски. Порядок правил значим:
// грумминг и разбивка перебивают дешевизну, LLD сильнее всего. Считается всё в
// ярусах и ни на какой инструмент не опирается: в модель ярус разворачивается
// последним шагом, маппингом активного харнеса.
func pickTier(r row) verdict {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD") && (r.Cost == "L" || r.Cost == "XL"):
		return verdict{Tier: tierMax, Reason: "LLD ценой L/XL: сложное проектирование, max делает его лучше pro"}
	case strings.EqualFold(r.Type, "LLD"):
		return verdict{Tier: tierPro, Reason: "LLD: дизайн отдаётся сильной модели"}
	case unc >= 4:
		return verdict{Tier: tierPro, Reason: fmt.Sprintf("неопределённость %d: сначала грумминг, исполнять рано", unc), Groom: true}
	case r.Cost == "XL":
		return verdict{Tier: tierPro, Reason: "цена XL: сначала разбить на серию, целиком не отдавать", Groom: true}
	case r.Cost == "S" && unc == 0:
		return verdict{Tier: tierMini, Reason: "совсем атомарная правка с очевидным подходом, дешёвой модели хватает"}
	case r.Cost == "S" && unc == 1:
		return verdict{Tier: tierBase, Reason: "небольшая задача, подход уже выбран, дешёвая модель справится"}
	case r.Cost == "" || r.Cost == "-":
		return verdict{Tier: tierPro, Reason: "цена не оценена, до оценки модель по умолчанию, не забыть оценить"}
	default:
		return verdict{Tier: tierPro, Reason: "задача для дешёвых моделей не подходит, по умолчанию идёт сильной"}
	}
}

// Роли, под которые считается вердикт. Исполнитель пишет код, ревьювер читает
// готовый дифф, и калибр им нужен разный.
const (
	roleExec   = "exec"
	roleReview = "review"
)

var validRoles = map[string]bool{roleExec: true, roleReview: true}

// reviewShift переводит исполнительский вердикт в ревьюверский. Ревьюверу
// нужен не калибр автора, а внимательность на готовом диффе, поэтому вердикт
// опускается на ярус, но не ниже base: mini дифф читает бегло и замечаний не
// находит, а base это дешёвая рабочая ступень. Два случая спуска не знают.
// Дизайн (тип LLD) читается тем же калибром, каким пишется, спуск тут не
// экономия, а потеря. Грумминговый вердикт значит, что работы ещё не было, и
// ревьюить нечего. Effort роль не трогает: глубина размышления идёт за
// неопределённостью задачи, а она от роли не меняется.
func reviewShift(v *verdict, r row) {
	switch {
	case v.Groom:
		v.Reason += "; роль ревью: вердикт грумминговый, работа по нему не начиналась, ревьюить пока нечего"
		return
	case strings.EqualFold(r.Type, "LLD"):
		v.Reason += "; роль ревью: дизайн читается тем же калибром, каким пишется, спуска нет"
		return
	}
	i := tierIndex(v.Tier)
	switch {
	case i < 0:
		return
	case v.Tier == tierBase:
		v.Reason += "; роль ревью: base это пол ревьювера, ниже не опускаем"
	case i == 0:
		v.Reason += "; роль ревью: mini -> base, дифф надо читать внимательно, ниже base ревью не опускаем"
		v.Tier = tierBase
	default:
		v.Reason += fmt.Sprintf("; роль ревью: %s -> %s, ревьюверу нужен не калибр автора, а внимательность на диффе", v.Tier, tierNames[i-1])
		v.Tier = tierNames[i-1]
	}
}

// costAtLeastM отделяет цены, на которых сдвиг вниз стоит проговаривать
// отдельно от LLD: M, L и XL по DK-015 достойны opus не меньше дизайна, S
// дешёвая модель тянет и без всякого сдвига.
func costAtLeastM(cost string) bool {
	switch cost {
	case "M", "L", "XL":
		return true
	default:
		return false
	}
}

// floorBaseEffort поднимает effort вердикта яруса base минимум до high. Это
// дешёвая рабочая ступень лестницы, а риск потерять качество на ней есть,
// поэтому экономить на глубине размышления смысла нет: low и medium
// подтягиваются, xhigh и выше не трогаются. Ярус mini пол не касается: модель
// вроде Haiku 4.5 про effort в API вовсе не знает, её low остаётся формальной
// меткой. На харнесе, где base развёрнут в дорогую модель, пол теряет исходное
// обоснование, но вреда не наносит: он только поднимает глубину размышления.
func floorBaseEffort(v *verdict) {
	if v.Tier != tierBase {
		return
	}
	if v.Effort == "low" || v.Effort == "medium" {
		v.Reason += ", effort поднят до high: base дёшев, экономить глубину смысла нет"
		v.Effort = "high"
	}
}

// pickEffort считает уровень размышления по неопределённости из разбивки ранга.
// Тип и цена входят только там, где по ним видно, что решение ещё не найдено
// (LLD, вердикты «исполнять рано») или что метаданным верить рано: цена не
// оценена, значит грумминг не доведён до конца, и нулю в третьем слагаемом
// доверия не больше, чем прочерку. Уровень max маппингом не выдаётся, он
// остаётся ручным решением через override-строку файла задачи.
func pickEffort(r row) string {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD"), unc >= 4, r.Cost == "XL":
		return "xhigh"
	case unc < 0, r.Cost == "", r.Cost == "-":
		return "high"
	case unc == 0:
		return "low"
	case unc <= 2:
		return "medium"
	default:
		return "high"
	}
}

// overrideTiers это допустимые значения строки «Модель:» в файле задачи. Кроме
// имён ярусов принимаются старые имена моделей: строки в уже написанных файлах
// задач ломать об жёсткую ошибку нельзя, а псевдоним переводит их в ярус без
// потери смысла. Конкретную модель инструмента писать нельзя сознательно:
// строка в файле задачи переживает смену харнеса, ярус переносим, имя модели
// нет. Опечатка не должна молча провалиться в обычный маппинг, поэтому любое
// другое значение это ошибка pick, а не игнорируемая строка.
var overrideTiers = map[string]string{
	tierMini: tierMini, tierBase: tierBase, tierPro: tierPro, tierMax: tierMax,
	"haiku": tierMini, "sonnet": tierBase, "opus": tierPro, "fable": tierMax,
}

var validEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// overrides это ручные развилки из файла задачи. Оси независимы: домен может
// требовать другого яруса, другого effort или того и другого сразу, а пустая
// ось берётся из обычного маппинга.
type overrides struct {
	Tier   string
	Effort string
}

// readOverrides ищет в файле задачи строки override (форматы «Модель: pro»
// и «- Эффорт: xhigh», поясняющий хвост в скобках допустим и отбрасывается);
// по каждой оси берётся первая встреченная строка. Нет файла или строк,
// пустой результат без ошибки: работает обычный маппинг pick.
func readOverrides(root, id string) (overrides, error) {
	var ov overrides
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ov, nil
		}
		return ov, err
	}
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		t = strings.TrimPrefix(t, "- ")
		switch {
		case strings.HasPrefix(t, "Модель:") && ov.Tier == "":
			name := overrideValue(t, "Модель:")
			tier, ok := overrideTiers[name]
			if !ok {
				return ov, fmt.Errorf("файл задачи %s: override-строка задаёт неизвестный ярус %q, допустимы mini, base, pro, max и старые имена haiku, sonnet, opus, fable; конкретную модель инструмента в override писать нельзя, она не переносима между харнесами", id, name)
			}
			ov.Tier = tier
		case strings.HasPrefix(t, "Эффорт:") && ov.Effort == "":
			effort := overrideValue(t, "Эффорт:")
			if !validEfforts[effort] {
				return ov, fmt.Errorf("файл задачи %s: override-строка задаёт неизвестный effort %q, допустимы low, medium, high, xhigh, max", id, effort)
			}
			ov.Effort = effort
		}
	}
	return ov, nil
}

func overrideValue(line, prefix string) string {
	v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if i := strings.Index(v, "("); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// timeNow это часы утилиты, отдельной переменной ради тестов: формула
// корректора и дата записи считаются от одного момента.
var timeNow = time.Now

func cmdPick(root, id string, record bool, role string) (string, error) {
	if !validRoles[role] {
		return "", fmt.Errorf("неизвестная роль %q, допустимы exec и review", role)
	}
	rows, err := loadRows(root)
	if err != nil {
		return "", err
	}
	r := rowOf(rows, id)
	if r == nil {
		return "", fmt.Errorf("задачи %s нет на доске", id)
	}
	ov, err := readOverrides(root, id)
	if err != nil {
		return "", err
	}
	now := timeNow()
	v := pickTier(*r)
	v.Effort = pickEffort(*r)
	var c correction
	var warns []string
	if ov.Tier != "" {
		// Ручное решение сильнее автоматики: override корректор не двигает,
		// иначе указанный руками ярус пришлось бы отстаивать повторно на
		// каждый снимок квоты.
		v = verdict{Tier: ov.Tier, Effort: v.Effort, Reason: "модель задана override-строкой файла задачи"}
	} else {
		var s snapshot
		path := quotaPath()
		s, err = readSnapshot(path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("снимок квоты не прочитан (%v), вердикт без корректора", err))
		} else if w := s.ageWarn(path, now); w != "" {
			warns = append(warns, w)
		}
		warns = append(warns, s.Warns...)
		c = correctTier(v.Tier, v.Groom, s, now)
		v.Tier = c.Tier
	}
	// Спуск на роль ревью идёт последним по ярусной оси: сдвигается то, что
	// осталось после override и корректора, иначе корректор увёл бы вердикт
	// ревьювера ещё на ярус ниже пола.
	if role == roleReview {
		reviewShift(&v, *r)
	}
	// Пол base применяется здесь, а не сразу после pickEffort, потому что от
	// override и от сдвига корректора зависит, к какому ярусу его применять; а
	// явный override effort должен пол перебить целиком, поэтому если он есть,
	// пол не трогаем.
	if ov.Effort != "" {
		v.Effort = ov.Effort
		v.Reason += ", effort задан override-строкой"
	} else {
		floorBaseEffort(&v)
	}
	// Разворачивание яруса в модель идёт последним шагом: до него весь расчёт
	// про ступени лестницы и ни от какого инструмента не зависит.
	tm := resolveTierModels(root)
	v.Model = tm.model(v.Tier)
	if tm.Note != "" {
		warns = append(warns, tm.Note)
	}
	// Сложенные в одну модель соседние ярусы это законный маппинг, и сдвиг по
	// такой паре модель не меняет. Молчать про него нельзя: холостой ход
	// неотличим от отсутствия сдвига.
	// Разворачивать нечем, значит про модель сказать нечего вовсе, и хвост про
	// холостой ход был бы обещанием несуществующего.
	c.SameModel = c.shifted() && tm.model(c.Tier) != unmappedModel && tm.model(c.From) == tm.model(c.Tier)
	if tail := c.tail(); tail != "" {
		v.Reason += "; " + tail
	}
	// Совет отложить адресован тому, кто решает, браться ли за работу сейчас,
	// поэтому в вердикте ревьювера его нет: дифф к этому моменту уже написан,
	// откладывать нечего, а исполнительский вердикт по той же задаче совет и
	// так печатает.
	switch {
	case role == roleReview:
	case c.Down && strings.EqualFold(r.Type, "LLD"):
		v.Reason += "; дизайн слабой моделью это долгий ущерб, а сброс близко, так что если не горит, лучше отложить"
	case c.Down && costAtLeastM(r.Cost):
		v.Reason += "; сдвиг вниз на цене M и выше это заметная потеря качества в исполнении, а сброс близко, так что если не горит, лучше отложить"
	}
	for _, w := range warns {
		v.Reason += "; " + w
	}
	unc := "?"
	if n := uncertainty(r.Rank); n >= 0 {
		unc = fmt.Sprint(n)
	}
	if record {
		if err := recordExecution(root, id, v, c, tm, now, role); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("model: %s\neffort: %s\ntier: %s\n%s (%s, цена %s, неопределённость %s): %s",
		v.Model, v.Effort, v.Tier, r.ID, r.Type, r.Cost, unc, v.Reason), nil
}

// recordExecution дописывает строку исполнения в конец раздела «Ход работы»
// файла задачи (перед хвостовыми пустыми строками, чтобы не оторваться от
// остальных записей); без раздела он добавляется в конец файла. Грумминговый
// вердикт пишется словом «Грумминг»: исполнение по нему не начинается, и
// строка не должна обещать то, чего не было. Сдвинутый вердикт несёт и
// маппинг, и причину сдвига: иначе по файлу задачи не понять, почему модель
// разошлась с таблицей. Роль ревью пишется словом «Ревью»: по «Ходу работы»
// тогда видно не только кто исполнял, но и кто читал дифф.
func recordExecution(root, id string, v verdict, c correction, tm tierModels, now time.Time, role string) error {
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("файла задачи нет, завести: taskctl file %s", id)
		}
		return err
	}
	content := strings.TrimRight(string(data), "\n") + "\n"
	label := "Исполнение"
	switch {
	case role == roleReview:
		label = "Ревью"
	case v.Groom:
		label = "Грумминг"
	}
	shift := ""
	if c.shifted() {
		shift = fmt.Sprintf(" (маппинг %s, корректор: %s)", tm.model(c.From), c.Note)
	}
	// Строка несёт модель, а не ярус: по ней восстанавливают, чем задача
	// делалась. Развернуть ярус нечем, значит в строку идёт он сам, иначе
	// исполнителя в записи не осталось бы вовсе.
	name := v.Model
	if name == unmappedModel {
		name = v.Tier
	}
	line := fmt.Sprintf("- %s: субагент %s/%s по вердикту pick%s, %s.",
		label, name, v.Effort, shift, now.Format("2006-01-02"))

	lines := strings.Split(content, "\n")
	head := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "## Ход работы") {
			head = i
			break
		}
	}
	if head < 0 {
		content += "\n## Ход работы\n\n" + line + "\n"
		return os.WriteFile(path, []byte(content), 0o644)
	}
	end := len(lines)
	for i := head + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	for end > head+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	ins := []string{line}
	if end == head+1 { // раздел был пуст, отбить запись от заголовка
		ins = []string{"", line}
	}
	lines = append(lines[:end], append(ins, lines[end:]...)...)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
