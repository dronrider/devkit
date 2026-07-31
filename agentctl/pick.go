package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type verdict struct {
	Model  string
	Effort string // reasoning effort субагента: low, medium, high, xhigh
	Reason string
	Groom  bool // вердикт «исполнять рано»: сначала грумминг или разбивка
}

// pickModel выводит модель из метаданных строки доски. Порядок правил значим:
// грумминг и разбивка перебивают дешевизну, LLD сильнее всего.
func pickModel(r row) verdict {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD") && (r.Cost == "L" || r.Cost == "XL"):
		return verdict{Model: "fable", Reason: "LLD ценой L/XL: сложное проектирование, fable делает его лучше opus"}
	case strings.EqualFold(r.Type, "LLD"):
		return verdict{Model: "opus", Reason: "LLD: дизайн отдаётся сильной модели"}
	case unc >= 4:
		return verdict{Model: "opus", Reason: fmt.Sprintf("неопределённость %d: сначала грумминг, исполнять рано", unc), Groom: true}
	case r.Cost == "XL":
		return verdict{Model: "opus", Reason: "цена XL: сначала разбить на серию, целиком не отдавать", Groom: true}
	case r.Cost == "S" && unc == 0:
		return verdict{Model: "haiku", Reason: "совсем атомарная правка с очевидным подходом, дешёвой модели хватает"}
	case r.Cost == "S" && unc == 1:
		return verdict{Model: "sonnet", Reason: "небольшая задача, подход уже выбран, дешёвая модель справится"}
	case r.Cost == "" || r.Cost == "-":
		return verdict{Model: "opus", Reason: "цена не оценена, до оценки модель по умолчанию, не забыть оценить"}
	default:
		return verdict{Model: "opus", Reason: "задача для дешёвых моделей не подходит, по умолчанию идёт сильной"}
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
// нужен не калибр автора, а внимательность на готовом диффе, поэтому модель
// опускается на ярус, но не ниже sonnet: haiku дифф читает бегло и замечаний
// не находит, а запросы sonnet стоят копейки. Два случая спуска не знают.
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
	i := tierIndex(v.Model)
	switch {
	case i < 0:
		return
	case v.Model == "sonnet":
		v.Reason += "; роль ревью: sonnet это пол ревьювера, ниже не опускаем"
	case i == 0:
		v.Reason += "; роль ревью: haiku -> sonnet, дифф надо читать внимательно, ниже sonnet ревью не опускаем"
		v.Model = "sonnet"
	default:
		v.Reason += fmt.Sprintf("; роль ревью: %s -> %s, ревьюверу нужен не калибр автора, а внимательность на диффе", v.Model, tiers[i-1])
		v.Model = tiers[i-1]
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

// floorSonnetEffort поднимает effort вердикта с моделью sonnet минимум до
// high. Запросы sonnet стоят копейки, а риск потерять качество при этом есть,
// поэтому экономить на глубине размышления смысла нет: low и medium
// подтягиваются, xhigh и выше не трогаются. Haiku пол не касается: у Haiku
// 4.5 effort в API не работает вовсе, его low остаётся формальной меткой.
func floorSonnetEffort(v *verdict) {
	if v.Model != "sonnet" {
		return
	}
	if v.Effort == "low" || v.Effort == "medium" {
		v.Reason += ", effort поднят до high: sonnet дёшев, экономить глубину смысла нет"
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

// validModels и validEfforts перечисляют допустимые значения override-строк
// файла задачи. Опечатка в значении не должна молча провалиться в обычный
// маппинг, поэтому неизвестное имя это ошибка pick, а не игнорируемая строка.
var validModels = map[string]bool{"haiku": true, "sonnet": true, "opus": true, "fable": true}

var validEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// overrides это ручные развилки из файла задачи. Оси независимы: домен может
// требовать другой модели, другого effort или того и другого сразу, а пустая
// ось берётся из обычного маппинга.
type overrides struct {
	Model  string
	Effort string
}

// readOverrides ищет в файле задачи строки override (форматы «Модель: opus»
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
		case strings.HasPrefix(t, "Модель:") && ov.Model == "":
			model := overrideValue(t, "Модель:")
			if !validModels[model] {
				return ov, fmt.Errorf("файл задачи %s: override-строка задаёт неизвестную модель %q, допустимы haiku, sonnet, opus, fable", id, model)
			}
			ov.Model = model
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
	v := pickModel(*r)
	v.Effort = pickEffort(*r)
	var c correction
	var warns []string
	if ov.Model != "" {
		// Ручное решение сильнее автоматики: override модели корректор не
		// двигает, иначе указанную руками модель пришлось бы отстаивать
		// повторно на каждый снимок квоты.
		v = verdict{Model: ov.Model, Effort: v.Effort, Reason: "модель задана override-строкой файла задачи"}
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
		c = correctModel(v.Model, v.Groom, s, now)
		v.Model = c.Model
	}
	// Спуск на роль ревью идёт последним по модельной оси: сдвигается то, что
	// осталось после override и корректора, иначе корректор увёл бы вердикт
	// ревьювера ещё на ярус ниже пола.
	if role == roleReview {
		reviewShift(&v, *r)
	}
	// Пол sonnet применяется здесь, а не сразу после pickEffort, потому что от
	// override модели и от сдвига корректора зависит, к какой модели его
	// применять; а явный override effort должен пол перебить целиком, поэтому
	// если он есть, пол не трогаем.
	if ov.Effort != "" {
		v.Effort = ov.Effort
		v.Reason += ", effort задан override-строкой"
	} else {
		floorSonnetEffort(&v)
	}
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
		if err := recordExecution(root, id, v, c, now, role); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("model: %s\neffort: %s\n%s (%s, цена %s, неопределённость %s): %s",
		v.Model, v.Effort, r.ID, r.Type, r.Cost, unc, v.Reason), nil
}

// recordExecution дописывает строку исполнения в конец раздела «Ход работы»
// файла задачи (перед хвостовыми пустыми строками, чтобы не оторваться от
// остальных записей); без раздела он добавляется в конец файла. Грумминговый
// вердикт пишется словом «Грумминг»: исполнение по нему не начинается, и
// строка не должна обещать то, чего не было. Сдвинутый вердикт несёт и
// маппинг, и причину сдвига: иначе по файлу задачи не понять, почему модель
// разошлась с таблицей. Роль ревью пишется словом «Ревью»: по «Ходу работы»
// тогда видно не только кто исполнял, но и кто читал дифф.
func recordExecution(root, id string, v verdict, c correction, now time.Time, role string) error {
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
		shift = fmt.Sprintf(" (маппинг %s, корректор: %s)", c.From, c.Note)
	}
	line := fmt.Sprintf("- %s: субагент %s/%s по вердикту pick%s, %s.",
		label, v.Model, v.Effort, shift, now.Format("2006-01-02"))

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
