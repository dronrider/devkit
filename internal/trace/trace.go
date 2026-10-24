// Package trace держит словарь следов этапа (LLD DK-972, решение 6). Слово
// называет материал, который остаётся после этапа, и у каждого слова один
// проверяющий: он читает то же, что читали ворота до словаря, и печатает,
// чего не хватает. Проверяющие переехали сюда из кода ворот taskctl и
// shipctl, ворота зовут их отсюда, а этап плана спрашивает след словом из
// шаблона.
package trace

import (
	"strings"

	"github.com/dronrider/devkit/internal/stage"
)

// Слова словаря. Семейство stage:<вид> берёт вид из словаря этапов
// internal/stage, отдельного перечня для него нет.
const (
	StagePrefix = "stage:"

	WordTests     = "tests"
	WordRegcheck  = "regcheck"
	WordStand     = "stand"
	WordScenario  = "scenario"
	WordRehearsal = "rehearsal"
	WordReview    = "review"
	WordMerged    = "merged"
	WordVerify    = "verify"
	// WordWord это этап без следа: он закрывается на слове агента, и «слово»
	// здесь не изъятие, а честная надпись о том, что проверить этап нечем.
	WordWord = "слово"
)

// words это слова словаря вне семейства stage.
var words = []string{WordTests, WordRegcheck, WordStand, WordScenario,
	WordRehearsal, WordReview, WordMerged, WordVerify, WordWord}

// Words перечисляет слова словаря для отказа с перечнем.
func Words() []string {
	return append([]string(nil), words...)
}

// Known отвечает, есть ли слово в словаре. Вид у stage:<вид> сверяется со
// словарём этапов: незнакомое слово в trace шаблона это опечатка, и этап с ней
// выглядел бы закрытым, хотя проверяющего у слова нет.
func Known(word string) bool {
	if strings.HasPrefix(word, StagePrefix) {
		return stage.Known(strings.TrimPrefix(word, StagePrefix))
	}
	return among(words, word)
}

// StageWord собирает слово семейства stage по виду этапа.
func StageWord(kind string) string { return StagePrefix + kind }

// StageKind достаёт вид этапа из слова семейства stage, пустая строка у
// остальных слов.
func StageKind(word string) string {
	return strings.TrimPrefix(word, StagePrefix)
}

// TaskBound отвечает, живёт ли след в файле задачи или её записи этапов: у
// сессии без задачи его неоткуда читать, и plan done закрывает такой этап
// словом. Дерево и git проверяются как у задачи.
func TaskBound(word string) bool {
	switch word {
	case WordStand, WordScenario, WordRehearsal, WordVerify:
		return true
	}
	return strings.HasPrefix(word, StagePrefix)
}

func among(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
