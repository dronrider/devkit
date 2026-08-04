package main

import (
	"strings"
	"testing"
)

// Эстимейт считается из цены зеркальной строки по таблице контура: это затраты
// полного цикла, работа агента вместе с сопровождением инженером. Уезжает он
// при take, отдельного планировщика нет.
func TestTakeSendsEstimateFromCost(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", "L", ticketLink("ABC-12")})
	fakeState.ticket.Estimate = ""
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "estimate ABC-12 3d") {
		t.Fatalf("цена L обязана уехать оценкой 3d: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "оценка: цена L строки XR-1 -> 3d") {
		t.Fatalf("вывод не рассказал про оценку:\n%s", msg)
	}
}

// Оценку, уже стоящую в тикете, команда не трогает: её мог поправить человек, и
// затирать чужую цифру автоматика права не имеет.
func TestTakeKeepsExistingEstimate(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", "S", ticketLink("ABC-12")})
	fakeState.ticket.Estimate = "2d"
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "estimate") {
		t.Fatalf("чужая оценка затёрта: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "уже стоит «2d»") {
		t.Fatalf("вывод не объяснил, почему оценка не уехала:\n%s", msg)
	}
}

// Цена, которую контур в оценку не переводит (XL режут, а не оценивают, и «-»
// значит «не оценено»), эстимейта не даёт, и команда об этом говорит, а не
// молчит.
func TestTakeSaysWhyNoEstimate(t *testing.T) {
	for _, tc := range []struct{ name, cost string }{
		{"цена XL", "XL"}, {"цена не проставлена", "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupEnv(t, contourFile, bindingFile)
			writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", tc.cost, ticketLink("ABC-12")})
			fakeState.ticket.Estimate = ""
			msg, err := cmdTake(root, "ABC-12")
			if err != nil {
				t.Fatal(err)
			}
			if hasCall(fakeState, "estimate") {
				t.Fatalf("оценка уехала из непереводимой цены: %v", fakeState.calls)
			}
			if !strings.Contains(msg, "в оценку не переводит") {
				t.Fatalf("вывод не объяснил отсутствие оценки:\n%s", msg)
			}
		})
	}
}

// Зеркальной строки на доске нет: считать эстимейт не из чего, и команда
// говорит об этом вслух.
func TestTakeWithoutMirrorRow(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	fakeState.ticket.Estimate = ""
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "estimate") {
		t.Fatalf("оценка уехала без строки доски: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "зеркальной строки") {
		t.Fatalf("вывод не объяснил отсутствие оценки:\n%s", msg)
	}
}

// Разбивка ранга кормит необязательную операцию rank: сумма уезжает в числовое
// поле приоритета, если контур его назвал и адаптер операцию умеет.
func TestTakeSendsRankWhenAdapterCan(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "33 (25+5+1+0+2)", "M", ticketLink("ABC-12")})
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "rank ABC-12 customfield_100 33") {
		t.Fatalf("сумма ранга не уехала в поле приоритета: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "приоритет: R=33") {
		t.Fatalf("вывод не рассказал про приоритет:\n%s", msg)
	}
}

// Поле контур назвал, а операции у адаптера нет: это молчаливое бездействие, и
// команда называет его вслух, вместо того чтобы промолчать.
func TestTakeSaysRankIsMissing(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "33 (25+5+1+0+2)", "M", ticketLink("ABC-12")})
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "операции rank не умеет") {
		t.Fatalf("вывод промолчал про отсутствующую ось:\n%s", msg)
	}
}

// Контур поля приоритета не назвал: говорить не о чем, лишней строки в выводе
// нет.
func TestTakeQuietWithoutRankField(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, "rank_field = \"customfield_100\"\n", "", 1), bindingFile)
	writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "33 (25+5+1+0+2)", "M", ticketLink("ABC-12")})
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "приоритет") {
		t.Fatalf("контур поля не назвал, а вывод про него говорит:\n%s", msg)
	}
}

// Ссылка строки ведёт на тикет, и по ней зеркальная строка и опознаётся. Ключ с
// тем же префиксом и лишней цифрой это чужой тикет.
func TestBoardRowsFindTicketByLink(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root,
		boardRowText{sectInProgress, "XR-1", "33 (25+5+1+0+2)", "M", ticketLink("ABC-120")},
		boardRowText{sectCheck, "XR-2", "13 (0+8+1+0+4)", "S", ticketLink("ABC-12")},
		boardRowText{sectBacklog, "XR-3", "5 (0+3+1+0+1)", "-", "[tasks/XR-3.md](tasks/XR-3.md)"},
	)
	rows, err := loadBoardRows(root, "ABC")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("строк доски: %v", rows)
	}
	row := rowForTicket(rows, "ABC-12")
	if row == nil || row.ID != "XR-2" || row.Sect != sectCheck || row.Cost != "S" || row.Rank != 13 {
		t.Fatalf("зеркальная строка ABC-12 разобрана как %+v", row)
	}
	if r := rowForTicket(rows, "ABC-1"); r != nil {
		t.Fatalf("ключ ABC-1 нашёлся в чужой ссылке: %+v", r)
	}
	if rows[2].Ticket != "" {
		t.Fatalf("обычная строка доски сошла за зеркальную: %+v", rows[2])
	}
}
