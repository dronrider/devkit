package main

import (
	"fmt"
	"strings"
)

type verdict struct {
	Model  string
	Reason string
}

// pickModel выводит модель из метаданных строки доски. Порядок правил
// значим: грумминг и разбивка перебивают дешевизну, LLD сильнее всего.
func pickModel(r row) verdict {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD"):
		return verdict{"opus", "LLD: дизайн отдаётся сильной модели"}
	case unc >= 4:
		return verdict{"opus", fmt.Sprintf("неопределённость %d: сначала грумминг, исполнять рано", unc)}
	case r.Cost == "XL":
		return verdict{"opus", "цена XL: сначала разбить на серию, целиком не отдавать"}
	case r.Cost == "S" && unc >= 0 && unc <= 1:
		return verdict{"haiku", "мелочь с ясным подходом, дешёвой модели хватает"}
	case r.Cost == "" || r.Cost == "-":
		return verdict{"sonnet", "цена не оценена, до оценки модель по умолчанию"}
	default:
		return verdict{"sonnet", "обычная задача, модель по умолчанию"}
	}
}

func cmdPick(root, id string) (string, error) {
	rows, err := loadRows(root)
	if err != nil {
		return "", err
	}
	r := rowOf(rows, id)
	if r == nil {
		return "", fmt.Errorf("задачи %s нет на доске", id)
	}
	v := pickModel(*r)
	unc := "?"
	if n := uncertainty(r.Rank); n >= 0 {
		unc = fmt.Sprint(n)
	}
	return fmt.Sprintf("model: %s\n%s (%s, цена %s, неопределённость %s): %s",
		v.Model, r.ID, r.Type, r.Cost, unc, v.Reason), nil
}
