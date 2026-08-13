package main

import (
	"os/exec"
	"strings"
	"testing"
)

// goRunShip запускает собранный shipctl в корне доски root с args. Разбор
// аргументов живёт в main, поэтому формы гоняются процессом: иначе выход через
// os.Exit и путь до cmdStart из библиотеки не видны.
func goRunShip(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"run", ".", "-C", root}, args...)
	out, err := exec.Command("go", full...).CombinedOutput()
	return string(out), err
}

// TestStartFlagAnywhere: у shipctl start флаг --slug стоит где угодно
// относительно ID, и обе формы доходят до одной и той же ошибки команды.
// XR-999 нет на доске, поэтому cmdStart отвечает «нет на доске» одинаково для
// «start XR-999 --slug хвост» и «start --slug хвост XR-999». До DK-236 вторая
// форма отбивалась в разборе «жду: start <ID>», потому что args[1] проверяли
// до fs.Parse и флаг принимали за пропущенный ID.
func TestStartFlagAnywhere(t *testing.T) {
	root, _ := setup(t, rowInProg, "")

	outAfter, errA := goRunShip(t, root, "start", "XR-999", "--slug", "хвост")
	outBefore, errB := goRunShip(t, root, "start", "--slug", "хвост", "XR-999")

	// Обе формы доходят до команды и одинаково падают на «нет на доске»: до
	// починки форма «флаг перед ID» падала раньше и с другим текстом.
	if !strings.Contains(outAfter, "XR-999 нет на доске") {
		t.Fatalf("start <ID> --slug: жду «нет на доске», получил %q (err=%v)", outAfter, errA)
	}
	if !strings.Contains(outBefore, "XR-999 нет на доске") {
		t.Fatalf("start --slug <ID>: жду «нет на доске», получил %q (err=%v)", outBefore, errB)
	}
}

// TestStartExtraPositionalRejects: лишний позиционный за ID раньше съедал хвост
// флагов молча (DK-236). После починки это отказ с «лишний аргумент», а не
// тихое продвижение. Обе формы «флаг до» и «флаг после» проверяются.
func TestStartExtraPositionalRejects(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	cases := [][]string{
		{"start", "XR-001", "junk", "--slug", "хвост"},
		{"start", "--slug", "хвост", "XR-001", "junk"},
	}
	for _, args := range cases {
		out, err := goRunShip(t, root, args...)
		if err == nil {
			t.Fatalf("%v: лишний позиционный не отбит, выход успешен:\n%s", args, out)
		}
		if !strings.Contains(out, "лишний аргумент") {
			t.Fatalf("%v: жду «лишний аргумент» в выводе, получил %q", args, out)
		}
	}
}

// TestStatusExtraPositionalRejects: статус не принимает позиционных, и лишний
// аргумент у него раньше молчал. now NeedArgs(pos, 0, 0, ...) его отбивает.
func TestStatusExtraPositionalRejects(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	out, err := goRunShip(t, root, "status", "junk")
	if err == nil {
		t.Fatalf("status junk: лишний позиционный не отбит:\n%s", out)
	}
	if !strings.Contains(out, "лишний аргумент") {
		t.Fatalf("status junk: жду «лишний аргумент», получил %q", out)
	}
}
