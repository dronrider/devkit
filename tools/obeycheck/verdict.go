package main

import "fmt"

// Базы прогона: с чем сравнивается кандидат. Первый позиционный аргумент это
// всегда кандидат, второй база (LLD DK-805, решение 3).
const (
	baseOld   = "старый" // прежняя редакция того же текста, вопрос «не стало ли хуже»
	baseEmpty = "пусто"  // раскладка без этого текста вовсе, вопрос «есть ли польза»
)

// Слова вердикта строки.
const (
	verdictGain      = "польза"
	verdictNoWorse   = "не хуже"
	verdictBothGreen = "зелёный на обеих"
	verdictBothRed   = "красный на обеих"
	verdictDrop      = "просадка"
)

// alpha это порог значимости: разница считается неслучайной, когда случайность
// даёт её не чаще раза из двадцати. Допуск снимает погрешность деления, иначе
// ровно пограничный случай (3/3 против 0/3 при k=3 даёт p ровно 0.05) читался
// бы то так, то этак.
const alpha = 0.05 + 1e-9

// verdictOf считает вердикт строки по зелёным клеткам кандидата и базы.
// Значимость разницы решает односторонний точный тест Фишера, а не разница в
// клетках: на сценарии с вероятностью зелёной клетки 0.8 при k=5 кандидат
// отстаёт от базы на клетку и больше в трети прогонов при одинаковом тексте, и
// треть честных правок упиралась бы в такие ворота.
func verdictOf(cand, base cell, repeats int) string {
	a, b := cand.green(), base.green()
	switch {
	case a == repeats && b == repeats:
		return verdictBothGreen
	case a == 0 && b == 0:
		return verdictBothRed
	case a > b && fisherP(a, repeats-a, b, repeats-b) <= alpha:
		return verdictGain
	case b > a && fisherP(b, repeats-b, a, repeats-a) <= alpha:
		return verdictDrop
	default:
		return verdictNoWorse
	}
}

// counted отвечает, зачтена ли строка с таким вердиктом при такой базе. С базой
// «старый» вопрос стоит «не стало ли хуже», и «не хуже» его закрывает. С базой
// «пусто» вопрос стоит «есть ли польза», и текст, без которого агент ведёт себя
// так же, в резидент не едет. «Красный на обеих» не зачтён при любой базе:
// сценарий не измеряет этот текст, и чинить надо либо текст, либо сценарий.
func counted(verdict, base string) bool {
	switch verdict {
	case verdictGain:
		return true
	case verdictNoWorse, verdictBothGreen:
		return base == baseOld
	default:
		return false
	}
}

// why объясняет незачтённую строку одной фразой: без причины таблица говорит,
// что не зачтено, но не говорит, о чём это.
func why(verdict, base string) string {
	switch verdict {
	case verdictDrop:
		return "у кандидата значимо меньше зелёных, правка просадила послушание"
	case verdictBothRed:
		return "сценарий не измеряет этот текст: он красный и с ним, и без него"
	case verdictBothGreen:
		return "текст ничего не меняет: агент сделал шаг и без него"
	case verdictNoWorse:
		return "разница незначима, а с базой «пусто» зачтена только польза"
	}
	return ""
}

// fisherP считает односторонний точный тест Фишера для таблицы 2x2: какова
// вероятность увидеть перевес кандидата не меньше нынешнего, если текст ни на
// что не влияет. Гипергеометрика на k до двадцати считается точно: все
// сочетания при таком k влезают в double без потери знаков.
func fisherP(a, b, c, d int) float64 {
	n := a + b + c + d
	rowA, colGreen := a+b, a+c
	total := choose(n, rowA)
	if total == 0 {
		return 1
	}
	p := 0.0
	for x := a; x <= min(rowA, colGreen); x++ {
		p += choose(colGreen, x) * choose(n-colGreen, rowA-x)
	}
	return p / total
}

// choose считает число сочетаний умножением, без факториалов: так значение
// остаётся целым на каждом шаге и не набирает погрешности.
func choose(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := 1.0
	for i := 1; i <= k; i++ {
		r = r * float64(n-k+i) / float64(i)
	}
	return r
}

// checkBase отбивает базу, которой у стенда нет.
func checkBase(base string) error {
	if base != baseOld && base != baseEmpty {
		return fmt.Errorf("база %q неизвестна: %s или %s", base, baseOld, baseEmpty)
	}
	return nil
}
