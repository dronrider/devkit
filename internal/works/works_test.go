package works

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseSessions гоняет разбор вывода tmux ls: пустой вывод, мусорные
// строки и честные строки с окнами и временем создания.
func TestParseSessions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Session
	}{
		{"пусто", "", []Session{}},
		{"одна", "task-XR-5\t2\t1700000000\n", []Session{{"task-XR-5", 2, 1700000000}}},
		{"без полей", "голое-имя\n", []Session{{"голое-имя", 0, 0}}},
		{"пустая строка пропущена", "task-XR-6\t1\t8\n\ntask-XR-7\t1\t9\n",
			[]Session{{"task-XR-6", 1, 8}, {"task-XR-7", 1, 9}}},
	}
	for _, c := range cases {
		if got := ParseSessions([]byte(c.in)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseSessions = %+v, ожидал %+v", c.name, got, c.want)
		}
	}
}

// TestSessionTask опознаёт работы конвейера по имени сессии: свой префикс
// проходит, чужой, производный хвост с моментом запуска и пустой префикс
// опознаны не бывают.
func TestSessionTask(t *testing.T) {
	cases := []struct {
		name, session, prefix, id, kind string
	}{
		{"своя задача", "task-XR-5", "XR", "XR-5", "task"},
		{"своя цель", "goal-XR-112", "XR", "XR-112", "goal"},
		{"чужой префикс", "task-ZZ-5", "XR", "", ""},
		{"производная сессия", "task-XR-208_1_1786532648", "XR", "", ""},
		{"не работа конвейера", "window-1", "XR", "", ""},
		{"доска без префикса", "task-XR-5", "", "", ""},
	}
	for _, c := range cases {
		id, kind := SessionTask(c.session, c.prefix)
		if id != c.id || kind != c.kind {
			t.Errorf("%s: SessionTask(%q, %q) = %q/%q, ожидал %q/%q",
				c.name, c.name, c.prefix, id, kind, c.id, c.kind)
		}
	}
}

// TestRegistryGoals: реестр отдаёт цели своего корня, чужие корни и записи без
// полей не считаются. Порядок сортированный.
func TestRegistryGoals(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"a.watch": "# цель\nroot = " + root + "\ngoal = DK-112\n",
		"b.watch": "root = /чужой/корень\ngoal = DK-113\n",
		"c.watch": "root = " + root + "\n",
		"d.watch": "root = " + root + "\ngoal = DK-095\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := RegistryGoals(home, root)
	if !reflect.DeepEqual(got, []string{"DK-112", "DK-095"}) {
		t.Fatalf("RegistryGoals = %v, ожидал [DK-112 DK-095]", got)
	}
}

// TestBusyTmux: занятость собирается из tmux без реестра. tmux подменяется
// скриптом в PATH, как его подменяют тесты дашборда.
func TestBusyTmux(t *testing.T) {
	fakeTmux(t, "task-XR-5|1|100\ngoal-XR-9|1|200\ntask-ZZ-1|1|300\n",
		"task-XR-5|claude|0\ngoal-XR-9|claude|0\ntask-ZZ-1|claude|0\n")
	busy, _ := Busy("XR", t.TempDir(), t.TempDir(), nil)
	if !busy["XR-5"] || !busy["XR-9"] {
		t.Fatalf("свои сессии не заняли задачи: %v", busy)
	}
	if busy["ZZ-1"] {
		t.Fatal("чужой префикс занял задачу")
	}
}

// fakeTmux кладёт в PATH подставной tmux: `ls` отдаёт список сессий, а
// `list-panes` пейны. Пустой ls это отказ tmux ls со словами tmuxRefusal в
// stderr и ненулевым кодом, как у настоящего tmux без сервера. Данные лежат файлами рядом со скриптом, чтобы кавычки
// вывода не приходилось прятать внутрь шелла. Пустой panes это отказ спросить
// пейны, как у tmux, которого на машине нет.
func fakeTmux(t *testing.T, ls, panes string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"ls) [ -f \"$0.ls\" ] && cat \"$0.ls\" || { cat \"$0.err\" >&2; exit 1; } ;;\n" +
		"list-panes) [ -f \"$0.panes\" ] && cat \"$0.panes\" || exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if ls != "" {
		if err := os.WriteFile(filepath.Join(bin, "tmux.ls"), []byte(ls), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "tmux.err"), []byte(tmuxRefusal), 0o644); err != nil {
		t.Fatal(err)
	}
	if panes != "" {
		if err := os.WriteFile(filepath.Join(bin, "tmux.panes"), []byte(panes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// tmuxRefusal это слова, которыми подставной tmux ls отказывает без списка.
// Тесты сорванного опроса подменяют их через файл tmux.err.
const tmuxRefusal = "no server running on /tmp/tmux-501/default"

// TestSessionsNoServerIsEmpty: tmux без сервера отвечает ненулевым кодом, и
// это штатное «сессий нет», а не сорванный опрос.
func TestSessionsNoServerIsEmpty(t *testing.T) {
	fakeTmux(t, "", "")
	got, err := Sessions()
	if err != nil {
		t.Fatalf("отказ tmux без сервера принят за сорванный опрос: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("сессий нет, ждал пустой список, получил %v", got)
	}
}

// TestSessionsNoTmuxIsErrNoTmux: машина без tmux отвечает своим ответом,
// и занятость на ней считается по остальным источникам, как до DK-904.
// Сорванным опросом ненайденный бинарь не считается (замечание ревью).
func TestSessionsNoTmuxIsErrNoTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Sessions()
	if !errors.Is(err, ErrNoTmux) {
		t.Fatalf("без tmux ждала ErrNoTmux, получила %v", err)
	}
	busy, err := Busy("XR", t.TempDir(), t.TempDir(), nil)
	if err != nil || busy == nil || len(busy) != 0 {
		t.Fatalf("без tmux занятость %v, ошибка %v; ждала пустую карту без ошибки", busy, err)
	}
}

// TestSessionsSocketRefusedIsError: жалоба на соединение с живым сервером за
// недоступным сокетом это сорванный опрос, а не «сессий нет».
func TestSessionsSocketRefusedIsError(t *testing.T) {
	fakeTmux(t, "", "")
	tmuxRefuse(t, "error connecting to /tmp/tmux-501/default (Permission denied)\n")
	if _, err := Sessions(); err == nil {
		t.Fatal("недоступный сокет живого сервера прочитан как «сессий нет»")
	}
	tmuxRefuse(t, "error connecting to /tmp/tmux-501/default (No such file or directory)\n")
	if got, err := Sessions(); err != nil || len(got) != 0 {
		t.Fatalf("сокета нет, ждала пустой список, получила %v, %v", got, err)
	}
}

// TestSessionsBareExitIsError: ненулевой код без слов это сорванный опрос.
// Настоящий tmux всякий отказ называет словами, и код без слов приходит
// только от чужой обёртки или подставной программы; стенды говорят словами
// про сервер, а правило в каркасе одно (замечание ревью DK-904, круг 3).
func TestSessionsBareExitIsError(t *testing.T) {
	fakeTmux(t, "", "")
	tmuxRefuse(t, "")
	if got, err := Sessions(); err == nil {
		t.Fatalf("код без слов принят за «сессий нет»: %v", got)
	}
}

// TestSessionsSilentIsError: регрессия DK-904. Незнакомый отказ tmux ls
// отдавался пустым списком, и потребители читали его как «никого нет»:
// сторож дашборда хоронил все окна разом, а занятость задач обнулялась.
// Сорванный опрос это ошибка со словами tmux, а не пустота.
func TestSessionsSilentIsError(t *testing.T) {
	fakeTmux(t, "", "")
	tmuxRefuse(t, "lost server\n")
	got, err := Sessions()
	if err == nil {
		t.Fatalf("сорванный опрос отдан списком %v без ошибки", got)
	}
	if !strings.Contains(err.Error(), "lost server") {
		t.Fatalf("ошибка не несёт слов tmux: %v", err)
	}
}

// TestBusySilentTmuxIsError: занятость по сорванному опросу не считается.
// Пустая карта здесь значила бы «все деревья свободны», и планировщик слота
// поднимал бы вторую сессию по занятой задаче (DK-904).
func TestBusySilentTmuxIsError(t *testing.T) {
	fakeTmux(t, "", "")
	tmuxRefuse(t, "lost server\n")
	busy, err := Busy("XR", t.TempDir(), t.TempDir(), nil)
	if err == nil {
		t.Fatalf("занятость по сорванному опросу посчитана: %v", busy)
	}
}

// tmuxRefuse подменяет слова отказа подставного tmux ls: список он больше не
// отдаёт, а в stderr пишет msg.
func tmuxRefuse(t *testing.T, msg string) {
	t.Helper()
	bin, _, _ := strings.Cut(os.Getenv("PATH"), string(os.PathListSeparator))
	os.Remove(filepath.Join(bin, "tmux.ls"))
	if err := os.WriteFile(filepath.Join(bin, "tmux.err"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestParsePanes гоняет разбор вывода tmux list-panes: имя сессии, команда
// переднего плана и признак мёртвого пейна.
func TestParsePanes(t *testing.T) {
	in := "task-XR-5|claude|0\ntask-XR-6|zsh|0\ntask-XR-7|node|1\n\n"
	want := []Pane{{"task-XR-5", "claude", false}, {"task-XR-6", "zsh", false},
		{"task-XR-7", "node", true}}
	if got := ParsePanes([]byte(in)); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePanes = %+v, ожидал %+v", got, want)
	}
}

// TestBusySkipsDeadWindows: регрессия DK-967. Работой считалась всякая
// tmux-сессия с именем task-<ID>, и брошенные окна копились на машине
// неделями: живых заходов два, а счётчик занятых показывал девять, и ворота
// ёмкости взвода отказывали подъёму словами «потолок пачки 3 исчерпан, живых
// работ 9». Работу даёт только живой заход: в окне сидит клиент, а строка на
// доске взята.
func TestBusySkipsDeadWindows(t *testing.T) {
	ls := "task-XR-5|1|100\ntask-XR-6|1|200\ntask-XR-7|1|300\ntask-XR-8|1|400\n"
	panes := "task-XR-5|claude|0\ntask-XR-6|zsh|0\ntask-XR-7|claude|0\ntask-XR-8|claude|0\n"
	fakeTmux(t, ls, panes)
	sect := map[string]string{"XR-5": "in-progress", "XR-6": "in-progress", "XR-7": "backlog"}
	busy, _ := Busy("XR", t.TempDir(), t.TempDir(), func(id string) string { return sect[id] })
	if !busy["XR-5"] {
		t.Fatal("живой заход не занял задачу")
	}
	if busy["XR-6"] {
		t.Fatal("окно с одной оболочкой посчитано работой")
	}
	if busy["XR-7"] {
		t.Fatal("строка в Backlog посчитана работой")
	}
	if busy["XR-8"] {
		t.Fatal("строка, которой нет на доске, посчитана работой")
	}
	if len(busy) != 1 {
		t.Fatalf("занятых %d, ожидал одну: %v", len(busy), busy)
	}
}

// TestBusyKeepsWorkWhenPanesUnknown: пейнов спросить не удалось, и режущее
// правило не работает. Отказ в другую сторону хуже: живая работа пропала бы из
// счёта, и машина взяла бы сверх потолка.
func TestBusyKeepsWorkWhenPanesUnknown(t *testing.T) {
	fakeTmux(t, "task-XR-5|1|100\n", "")
	busy, _ := Busy("XR", t.TempDir(), t.TempDir(), func(string) string { return "in-progress" })
	if !busy["XR-5"] {
		t.Fatalf("без ответа о пейнах работа пропала: %v", busy)
	}
}

// DK-968: ветка задачи узнаётся с хвостом-слагом и в любом регистре, а чужая
// ветка задачей не считается: по этому разбору ворота ёмкости отличают дерево
// работы от одноразового дерева прогона.
func TestBranchTask(t *testing.T) {
	cases := []struct{ branch, want string }{
		{"dk-470", "DK-470"},
		{"dk-470-lld-link", "DK-470"},
		{"DK-470", "DK-470"},
		{"main", ""},
		{"dk-", ""},
		{"dk-lld", ""},
		{"xr-470", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := BranchTask(c.branch, "DK"); got != c.want {
			t.Errorf("ветка %q: %q, жду %q", c.branch, got, c.want)
		}
	}
	if got := BranchTask("dk-470", ""); got != "" {
		t.Errorf("доска без префикса не должна узнавать задач: %q", got)
	}
}
