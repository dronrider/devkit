package main

import (
	"os/exec"
	"strings"
	"testing"
)

// goRunAgent запускает собранный агент в корне доски root с args и отдаёт
// совокупный вывод и ошибку. Тесты разбора аргументов гоняются processesм:
// разбор живёт в main, и из библиотечного вызова выход через os.Exit не виден.
func goRunAgent(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"run", "."}, args...)
	if root != "" {
		full = append([]string{"run", ".", "-C", root}, args...)
	}
	out, err := exec.Command("go", full...).CombinedOutput()
	return string(out), err
}

// TestPickFlagAnywhere: у agentctl pick флаг --role стоит где угодно относительно
// ID, и обе формы дают одинаковый вердикт. T-002 на доске из writeBoard это
// opus/medium для исполнителя и sonnet/high для ревьювера, поэтому разница
// хорошо видна: потеря --role review меняет вердикт целиком.
func TestPickFlagAnywhere(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)

	reviewSuffix := []string{"pick", "T-002", "--role", "review"}
	reviewPrefix := []string{"pick", "--role", "review", "T-002"}

	out, err := goRunAgent(t, root, reviewSuffix...)
	if err != nil {
		t.Fatalf("pick <ID> --role review: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
		t.Fatalf("pick <ID> --role review: жду sonnet/high, получил %q", out)
	}

	out2, err := goRunAgent(t, root, reviewPrefix...)
	if err != nil {
		t.Fatalf("pick --role review <ID>: %v\n%s", err, out2)
	}
	if !strings.HasPrefix(out2, "model: sonnet\neffort: high\n") {
		t.Fatalf("pick --role review <ID>: жду sonnet/high, получил %q", out2)
	}
}

// TestPickExtraPositionalRejects: лишний позиционный за ID раньше съедал хвост
// флагов молча, и «pick T-002 junk --role review» выдавал исполнительский
// вердикт opus/medium вместо ревьюверского sonnet/high (DK-236). После починки
// это отказ с «лишний аргумент», а не тихая потеря --role review. Падает на
// старом коде: там exited 0 и вердикт exec, а не ошибка.
func TestPickExtraPositionalRejects(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)

	cases := [][]string{
		{"pick", "T-002", "junk", "--role", "review"},
		{"pick", "--role", "review", "T-002", "junk"},
	}
	for _, args := range cases {
		out, err := goRunAgent(t, root, args...)
		if err == nil {
			t.Fatalf("%v: лишний позиционный не отбит, выход успешен:\n%s", args, out)
		}
		if !strings.Contains(out, "лишний аргумент") {
			t.Fatalf("%v: жду «лишний аргумент» в выводе, получил %q", args, out)
		}
		// Главный симптом DK-236: даже после отказа --role review не должен был
		// дойти до вердикта. Если в выводе появился исполнительский opus, починка
		// не работает: junk снова съел флаг.
		if strings.Contains(out, "model: opus") {
			t.Fatalf("%v: --role review потерян, exec-вердикт в выводе %q", args, out)
		}
	}
}

// TestPickMissingID: нехватка позиционного у pick отбивается, как и лишний.
// Регрессия: ранее форма «pick --role review» без ID падала на HasPrefix, а
// после перехода на frame.ParseArgs должна отбиваться единообразно через
// NeedArgs.
func TestPickMissingID(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	out, err := goRunAgent(t, root, "pick", "--role", "review")
	if err == nil {
		t.Fatalf("pick без ID: жду отказ, получил успешный выход:\n%s", out)
	}
	if !strings.Contains(out, "жду") && !strings.Contains(out, "лишний") {
		t.Fatalf("pick без ID: жду сообщение об ошибке разбора, получил %q", out)
	}
}
