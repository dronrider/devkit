package main

import (
	"os"
	"regexp"
	"strings"
)

// Вид приёмки задачи живёт суффиксом заголовка строки доски, а причина
// (барьер и перебор обходов) разделом «Приёмка» в файле задачи (LLD DK-292,
// решение 3). Этот файл держит машиночитаемую часть: разбор суффикса,
// закрытый список барьеров и чтение раздела «Приёмка».

const (
	acceptAgent = "agent"
	acceptMixed = "mixed"
	acceptUser  = "user"
)

// acceptKinds это три значения вида приёмки (LLD DK-292, решение 2). Суффикс
// несёт только user и mixed: агентский вид это умолчание, и вешать пометку на
// четыре строки из пяти дорого глазами.
var acceptKinds = []string{acceptAgent, acceptMixed, acceptUser}

// acceptBarriers это закрытый список шести барьеров (LLD DK-292, решение 1) и
// число обходов у каждого. Столько строк перебора обязано стоять в разделе
// «Приёмка» против названного барьера: у каждого обхода своя исход, и машинный
// счёт идёт по числу строк. Сам список обходов переедет в ACCEPTANCE.md задачей
// DK-299, а здесь только ключи и счёт, нужный воротам move check и lint.
var acceptBarriers = map[string]int{
	"глаза":         3,
	"доступ":        4,
	"необратимость": 4,
	"секрет":        3,
	"согласие":      1,
	"событие":       3,
}

// acceptSufRe разбирает суффикс «[приёмка: agent|mixed| user]». Агентский вид
// суффикса не несёт, но разбор принимает его для полной круговой прогонки
// (прошлая hand-правка могла его поставить).
var acceptSufRe = regexp.MustCompile(`\s*\[приёмка: (agent|mixed|user)\]\s*$`)

// acceptBarrierLineRe находит строку барьера в разделе «Приёмка» вида
// «- барьер «<ключ>»: причина». Ключ обязан быть из шести, скобки «ёлочки».
var acceptBarrierLineRe = regexp.MustCompile(`^- барьер «([^»]+)»`)

// acceptBypassRe узнаёт строку перебора обхода: подсписок второго уровня с
// исходом, например «  - headless-браузер с замером: годится, ширины уходят
// агенту». Отступ в два пробела отличает обход от строк вида и барьера.
var acceptBypassRe = regexp.MustCompile(`^  - `)

func validAccept(kind string) bool {
	for _, k := range acceptKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// acceptOf читает вид из заголовка строки. Без суффикса вид агентский: это
// умолчание, перевёрнутое относительно старого «при сомнениях пользовательский»
// (LLD DK-292, решение 1). Сопоставление идёт тем же путём, что splitTitle:
// хвосты [провал: ...] и [блок: ...] снимаются до поиска [приёмка: ...].
// Иначе они стоят после приёмки в фиксированном порядке суффиксов и
// регулярка, привязанная к концу строки, их прокусывает, читая user-задачу
// с суффиксом провала или блокировки как агентскую.
func acceptOf(title string) string {
	_, _, acceptSuf, _, _ := splitTitle(title)
	if m := acceptSufRe.FindStringSubmatch(acceptSuf); m != nil {
		return m[1]
	}
	return acceptAgent
}

// acceptSuffix собирает суффикс заголовка по виду: пустой для агентского (его
// умолчание не отмечается), « [приёмка: ...]» для user и mixed.
func acceptSuffix(kind string) string {
	if kind == "" || kind == acceptAgent {
		return ""
	}
	return " [приёмка: " + kind + "]"
}

// acceptanceHeading это заголовок раздела «Приёмка» файла задачи.
const acceptanceHeading = "## Приёмка"

// acceptanceSection читает текст раздела «Приёмка» файла задачи (без строки
// заголовка) с учётом ограждений блоков кода тем же порядком, что
// taskDocSections: заголовок внутри ``` это чужой вывод, а не раздел.
// ok=false значит, что файла нет; found=false что раздела в нём нет.
func acceptanceSection(root, id string) (text string, found, ok bool) {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return "", false, false
	}
	fence := ""
	inSection := false
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		if m := fenceRe.FindStringSubmatch(ln); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(ln[len(m[0]):]) == "":
				fence = ""
			}
			if inSection {
				out = append(out, ln)
			}
			continue
		}
		if fence != "" {
			if inSection {
				out = append(out, ln)
			}
			continue
		}
		if !inSection {
			if strings.HasPrefix(ln, acceptanceHeading) {
				inSection = true
			}
			continue
		}
		// Следующий заголовок уровня раздела кончает «Приёмка».
		if strings.HasPrefix(ln, "## ") {
			break
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n"), inSection, true
}

// parseAcceptance разбирает текст раздела «Приёмка» на ключ барьера и число
// строк перебора обходов. Ключ пуст, если строки барьера нет. Перебор считается
// по подсписку второго уровня после строки барьера: ровно те строки, что
// относятся к обходам названного барьера.
func parseAcceptance(text string) (barrier string, bypasses int) {
	lines := strings.Split(text, "\n")
	sawBarrier := false
	for _, ln := range lines {
		if m := acceptBarrierLineRe.FindStringSubmatch(ln); m != nil {
			barrier = m[1]
			sawBarrier = true
			continue
		}
		if sawBarrier && acceptBypassRe.MatchString(ln) {
			bypasses++
		}
	}
	return barrier, bypasses
}
