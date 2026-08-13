package frame

import (
	"flag"
	"strings"
	"testing"
)

// TestParseArgsMixesFlagsAndPositionals: позиционный аргумент возвращается
// независимо от того, где стоят флаги, а «--» кончает флаги совсем. До DK-236
// формы «флаг перед позиционным» и «флаг между позиционными» у трёх утилит
// (shipctl, agentctl, trackctl) разбирались через args[1] до fs.Parse, поэтому
// хвост fs.Args() выбрасывался молча; тест пришёл из taskctl вместе с самим
// ParseArgs (LLD DK-237, волна из DK-236) и держит регрессию общего кода.
func TestParseArgsMixesFlagsAndPositionals(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		pos  []string
		dir  string
		msg  string
	}{
		{"флаг после позиционных", []string{"XR-001", "текст", "-m", "коммит"}, []string{"XR-001", "текст"}, ".", "коммит"},
		{"флаг перед позиционными", []string{"-m", "коммит", "XR-001", "текст"}, []string{"XR-001", "текст"}, ".", "коммит"},
		{"флаг между позиционными", []string{"XR-001", "-C", "/x", "текст"}, []string{"XR-001", "текст"}, "/x", ""},
		{"только флаги", []string{"-C", "/x"}, nil, "/x", ""},
		{"только позиционные", []string{"XR-001"}, []string{"XR-001"}, ".", ""},
		{"дефисный текст за терминатором", []string{"-m", "коммит", "--", "-m"}, []string{"-m"}, ".", "коммит"},
		{"терминатор после позиционного", []string{"XR-001", "--", "-C"}, []string{"XR-001", "-C"}, ".", ""},
		// «лишний позиционный между флагами»: флаг --role стоит за junk'ом, и до
		// DK-236 разбирающий args[1] до fs.Parse его терял. ParseArgs снимает
		// оба позиционных, а флаг устанавливается, поэтому лишний виден
		// вызывающему NeedArgs, а не молчит. Полный сценарий (junk не съедает
		// --role review у agentctl pick) держит отдельный CLI-тест в agentctl.
		{"лишний позиционный между флагами", []string{"DK-146", "junk", "--role", "review"}, []string{"DK-146", "junk"}, ".", ""},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("тест", flag.ContinueOnError)
		dir := fs.String("C", ".", "стартовая директория")
		msg := fs.String("m", "", "сообщение коммита")
		fs.String("role", "", "роль")
		got := ParseArgs(fs, c.in)
		if strings.Join(got, "|") != strings.Join(c.pos, "|") {
			t.Errorf("%s: позиционные %v, ожидал %v", c.name, got, c.pos)
		}
		if *dir != c.dir || *msg != c.msg {
			t.Errorf("%s: -C=%q -m=%q, ожидал -C=%q -m=%q", c.name, *dir, *msg, c.dir, c.msg)
		}
	}
}

// TestNeedArgs: нехватка и лишний позиционный это ошибки, допустимое число
// проходит. Лишний аргумент это потерянные кавычки или промах мимо флага, и
// молчаливый выброс означал бы потерю данных (DK-236).
func TestNeedArgs(t *testing.T) {
	usage := "pick <ID>"
	if err := NeedArgs([]string{}, 1, 1, usage); err == nil {
		t.Errorf("нехватка позиционного не отбита")
	}
	if err := NeedArgs([]string{"a", "b"}, 1, 1, usage); err == nil {
		t.Errorf("лишний позиционный не отбит")
	}
	if err := NeedArgs([]string{"a"}, 1, 1, usage); err != nil {
		t.Errorf("допустимый позиционный отбит: %v", err)
	}
	// max < 0 снимает верхний порог: любое число позиционных проходит.
	if err := NeedArgs([]string{"a", "b", "c"}, 1, -1, usage); err != nil {
		t.Errorf("max=-1 сняло порог, но отбито: %v", err)
	}
	// min=0, max=0: ни одного позиционного, ловит лишний у подкоманд без
	// позиционных (status, budget, sync).
	if err := NeedArgs(nil, 0, 0, usage); err != nil {
		t.Errorf("ноль позиционных отбит при max=0: %v", err)
	}
	if err := NeedArgs([]string{"x"}, 0, 0, usage); err == nil {
		t.Errorf("лишний позиционный у подкоманды без позиционных не отбит")
	}
}
