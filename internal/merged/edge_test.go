package merged

import (
	"strings"
	"testing"
)

// edgeOf спрашивает снятость ребра свежей книгой: книга живёт одну команду, и
// между коммитами стенда её надо заводить заново.
func edgeOf(t *testing.T, root string, p Prereq) Edge {
	t.Helper()
	return Open(root).Edge(p)
}

// DoD: слияние X снимает ребро, откат и провал X держат его снова, вид user
// держит до закрытия. Правка только доски и файла задачи ребро не снимает.
func TestEdgeLifecycle(t *testing.T) {
	root := repo(t)
	x := Prereq{ID: "DK-10", Title: "Предпосылка"}
	if e := edgeOf(t, root, x); e.Lifted() || e.Park != ParkMerge || !strings.Contains(e.Why, "не слита") {
		t.Fatalf("без работы в main ребро %+v", e)
	}
	commit(t, root, "docs(tasks): DK-10 в работу", map[string]string{
		"docs/TASKS.md":       "# доска\n| DK-10 |\n",
		"docs/tasks/DK-10.md": "# DK-10\n",
	})
	if e := edgeOf(t, root, x); e.Lifted() {
		t.Fatalf("правка доски и файла задачи сняла ребро: %+v", e)
	}
	work := commit(t, root, "feat(x): DK-10 код", map[string]string{"x/code.go": "package x\n"})
	e := edgeOf(t, root, x)
	if !e.Lifted() || e.State != StateMerged || !strings.Contains(e.Why, work[:8]) {
		t.Fatalf("слитая работа не сняла ребро: %+v", e)
	}
	failed := Prereq{ID: "DK-10", Title: "Предпосылка [провал: прод отдаёт 500]"}
	if e := edgeOf(t, root, failed); e.Lifted() || !strings.Contains(e.Why, "провалена: прод отдаёт 500") {
		t.Fatalf("провал не держит ребро: %+v", e)
	}
	user := Prereq{ID: "DK-10", Title: "Предпосылка [приёмка: user]"}
	if e := edgeOf(t, root, user); e.Lifted() || e.Park != ParkClose {
		t.Fatalf("вид user отпустил ребро до закрытия: %+v", e)
	}
	mixed := Prereq{ID: "DK-10", Title: "Предпосылка [приёмка: mixed]"}
	if e := edgeOf(t, root, mixed); !e.Lifted() {
		t.Fatalf("вид mixed держит ребро после слияния: %+v", e)
	}
	commit(t, root, "revert: DK-10 откат", map[string]string{"x/code.go": ""})
	if e := edgeOf(t, root, x); e.Lifted() || !strings.Contains(e.Why, "откачена") || e.Park != ParkMerge {
		t.Fatalf("откат не вернул ребро: %+v", e)
	}
	closed := Prereq{ID: "DK-10", Title: "", Archived: true}
	if e := edgeOf(t, root, closed); !e.Lifted() || e.State != StateClosed {
		t.Fatalf("закрытие не сняло ребро: %+v", e)
	}
	userClosed := Prereq{ID: "DK-10", Title: "Предпосылка [приёмка: user]", Archived: true}
	if e := edgeOf(t, root, userClosed); !e.Lifted() {
		t.Fatalf("закрытая предпосылка вида user держит ребро: %+v", e)
	}
}

// DoD: правка в docs/lld снимает ребро. Зависимой от LLD нужен документ в main.
func TestEdgeLiftedByLLD(t *testing.T) {
	root := repo(t)
	commit(t, root, "docs(lld): DK-11 документ", map[string]string{"docs/lld/DK-11.md": "# LLD\n"})
	if e := edgeOf(t, root, Prereq{ID: "DK-11", Title: "Дизайн"}); !e.Lifted() {
		t.Fatalf("правка docs/lld не сняла ребро: %+v", e)
	}
}

// Предпосылка, пропавшая с доски мимо архива, ребро держит: опечатка в ID
// иначе пустила бы зависимую молча, даже когда в main лежит чужая работа.
func TestEdgeMissingPrereqHolds(t *testing.T) {
	root := repo(t)
	commit(t, root, "feat: DK-12 код", map[string]string{"x/code.go": "package x\n"})
	e := edgeOf(t, root, Prereq{ID: "DK-12"})
	if e.Lifted() || e.Park != "" || !strings.Contains(e.Why, "ни на доске, ни в архиве") {
		t.Fatalf("пропавшая предпосылка сняла ребро: %+v", e)
	}
}

// Без git признак не спросить, и ребро снимает одно закрытие, как до решения 2.
func TestEdgeOutsideGit(t *testing.T) {
	dir := t.TempDir()
	if e := edgeOf(t, dir, Prereq{ID: "DK-13", Title: "Предпосылка"}); e.Lifted() || !strings.Contains(e.Why, "не спрошен") {
		t.Fatalf("вне git ребро %+v", e)
	}
	if e := edgeOf(t, dir, Prereq{ID: "DK-13", Archived: true}); !e.Lifted() {
		t.Fatalf("вне git закрытие не сняло ребро: %+v", e)
	}
}

// Ever видит работу и после отката, а коммит доски работой не считает: по
// нему lint отличает строку, которую ворота старта когда-то пустили.
func TestEver(t *testing.T) {
	root := repo(t)
	b := Open(root)
	if ever, err := b.Ever("DK-14"); err != nil || ever {
		t.Fatalf("на пустом main работа нашлась: %v %v", ever, err)
	}
	commit(t, root, "docs(tasks): DK-14 в работу", map[string]string{"docs/tasks/DK-14.md": "# DK-14\n"})
	if ever, err := Open(root).Ever("DK-14"); err != nil || ever {
		t.Fatalf("коммит доски сочтён работой: %v %v", ever, err)
	}
	commit(t, root, "feat: DK-14 код", map[string]string{"x/code.go": "package x\n"})
	commit(t, root, "revert: DK-14 откат", map[string]string{"x/code.go": ""})
	ever, err := Open(root).Ever("DK-14")
	if err != nil || !ever {
		t.Fatalf("работа до отката не видна: %v %v", ever, err)
	}
	if v, _ := Open(root).Task("DK-14"); v.Merged || !v.Reverted {
		t.Fatalf("Ever сдвинул признак «слита»: %+v", v)
	}
	// Книга живёт одну команду: заведённая до коммитов, она их не видит.
	if ever, _ := b.Ever("DK-14"); ever {
		t.Fatal("книга перечитала лог на ходу")
	}
}

func TestDeps(t *testing.T) {
	cases := map[string]string{
		"Простая":                                   "",
		"С ребром [после DK-1]":                     "DK-1",
		"Два [после DK-1, DK-2] [приёмка: user]":    "DK-1,DK-2",
		"Путаный порядок [блок: ждём] [после DK-3]": "DK-3",
	}
	for title, want := range cases {
		if got := strings.Join(Deps(title), ","); got != want {
			t.Errorf("Deps(%q) = %q, ждал %q", title, got, want)
		}
	}
}
