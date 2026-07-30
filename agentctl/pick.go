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

// pick складывает вердикт из двух независимых осей. Модель это калибр
// исполнителя, он выводится из типа и цены; effort это глубина размышления, и
// заставляет размышлять неопределённость. Одна ось другую не диктует: та же
// модель на задачах с разной неопределённостью получает разный effort. Для
// sonnet результат ещё проходит пол: ниже high эта модель не отдаётся.
func pick(r row) verdict {
	v := pickModel(r)
	v.Effort = pickEffort(r)
	floorSonnetEffort(&v)
	return v
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

func cmdPick(root, id string, record bool) (string, error) {
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
		s, err = readSnapshot(quotaPath())
		if err != nil {
			warns = append(warns, "снимок квоты не прочитан, вердикт без корректора")
		}
		warns = append(warns, s.Warns...)
		c = correctModel(v.Model, v.Groom, s, now)
		v.Model = c.Model
	}
	// Пол sonnet применяется здесь, а не через pick(), потому что от override
	// модели и от сдвига корректора зависит, к какой модели его применять; а
	// явный override effort должен пол перебить целиком, поэтому если он есть,
	// пол не трогаем.
	if ov.Effort != "" {
		v.Effort = ov.Effort
		v.Reason += ", effort задан override-строкой"
	} else {
		floorSonnetEffort(&v)
	}
	if tail := c.tail(); tail != "" {
		v.Reason += "; " + tail
	}
	if c.Down && strings.EqualFold(r.Type, "LLD") {
		v.Reason += "; дизайн слабой моделью это долгий ущерб, а сброс близко, так что если не горит, лучше отложить"
	}
	for _, w := range warns {
		v.Reason += "; " + w
	}
	unc := "?"
	if n := uncertainty(r.Rank); n >= 0 {
		unc = fmt.Sprint(n)
	}
	if record {
		if err := recordExecution(root, id, v, c, now); err != nil {
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
// разошлась с таблицей.
func recordExecution(root, id string, v verdict, c correction, now time.Time) error {
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
	if v.Groom {
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
