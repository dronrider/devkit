package mainchat

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
)

func stamp(at int) string {
	return time.Date(2026, 10, 23, 21, 14, at, 0, time.UTC).Format(Stamp)
}

// Строка журнала пишется именованными полями с дефисом у пустых, и тот же
// формат читается обратно: разбор держится за ключевые слова, а не за
// позиции, и строка передачи с прежним носителем читается наравне с простой.
func TestLineRoundTrip(t *testing.T) {
	cases := []Bind{
		{Record: "DK-1422", Chat: "tmux-task-DK-1422", Project: "devkit", Source: ByWork},
		{Record: "DK-1422", Chat: "chat-3", Project: "devkit", Source: ByMove, Prior: "tmux-task-DK-1422"},
		{Record: "DK-1422", Source: ByOff},
		{Record: "DK-9", Chat: "sess-a", Source: "незнакомое слово"},
	}
	for _, b := range cases {
		b.Time = stamp(2)
		got, ok := ParseLine(Line(time.Date(2026, 10, 23, 21, 14, 2, 0, time.UTC), b))
		if !ok {
			t.Fatalf("строка не разобралась: %+v", b)
		}
		if got != b {
			t.Fatalf("строка не прочлась собой: written %+v, read %+v", b, got)
		}
	}
	// Снятие пишется дефисом в разговоре, как решил LLD, и читается пустым
	// разговором, а не пропавшей записью.
	off := Line(time.Date(2026, 10, 23, 21, 14, 2, 0, time.UTC), Bind{Record: "DK-1422", Source: ByOff})
	if !strings.Contains(off, " разговор - ") {
		t.Fatalf("снятие без дефиса в разговоре: %s", off)
	}
	// Пустое поле проекта пишется дефисом, как у реестра сессий.
	work := strings.TrimRight(Line(time.Date(2026, 10, 23, 21, 14, 2, 0, time.UTC),
		Bind{Record: "DK-1422", Chat: "chat-3", Source: ByHand}), "\n")
	if !strings.Contains(work, " проект - ") || !strings.HasSuffix(work, " прежде -") {
		t.Fatalf("пустые поля не легли дефисом: %s", work)
	}
}

// Чужие строки журнала пропускаются без обрушения разбора: журнал машинный и
// общий, и обрушение на незнакомой строке стоило бы дороже пустой свёртки.
func TestParseSkipsForeignLines(t *testing.T) {
	data := "не журнал вовсе\n2026-10-23T21:14:02 назначение DK-1 разговор chat-1 источник работа\n" +
		"2026-10-23T21:14:03 сессия aaa задача DK-2 источник заказ\n"
	binds := Parse([]byte(data))
	if len(binds) != 1 || binds["DK-1"].Chat != "chat-1" {
		t.Fatalf("свёртка чужих строк: %+v", binds)
	}
}

// Свёртка хвоста: последняя строка по записи выигрывает, снятие читается как
// «разговора нет», а назначение следом за снятием возвращает разговор.
func TestFoldTailWins(t *testing.T) {
	data := strings.Join([]string{
		stamp(1) + " назначение DK-1 разговор chat-old проект devkit источник рука",
		stamp(2) + " назначение DK-1 разговор chat-new проект devkit источник передача прежде chat-old",
		stamp(3) + " назначение DK-1 разговор - источник снято",
		stamp(4) + " назначение DK-1 разговор chat-new проект devkit источник работа",
		stamp(5) + " назначение DK-2 разговор chat-2 источник работа",
	}, "\n")
	binds := Parse([]byte(data))
	if got := binds["DK-1"]; got.Chat != "chat-new" || got.Source != ByWork {
		t.Fatalf("свёртка не взяла последнюю строку: %+v", got)
	}
	off := Parse([]byte(data[:strings.Index(data, stamp(4))]))
	if got := off["DK-1"]; got.Chat != "" || got.Record != "DK-1" {
		t.Fatalf("снятие не прочиталось пустым разговором: %+v", got)
	}
	if _, hit := Parse([]byte(stamp(3)+" назначение DK-3 разговор - источник снято"))["DK-3"]; !hit {
		t.Fatal("снятая запись пропала из свёртки целиком, а читателю надо отличать её от неназначавшейся")
	}
}

// Отрезки собираются в порядке строк журнала: подряд идущие строки одного
// разговора сходятся в один, смена разговора и снятие закрывают прежний,
// действующий отрезок стоит без Until.
func TestSegmentsByJournalOrder(t *testing.T) {
	recs := []Bind{
		{Record: "DK-1", Chat: "chat-a", Source: ByHand, Time: stamp(1)},
		{Record: "DK-1", Chat: "chat-a", Source: ByWork, Time: stamp(2)},
		{Record: "DK-1", Chat: "chat-b", Source: ByMove, Prior: "chat-a", Time: stamp(3)},
		{Record: "DK-1", Source: ByOff, Time: stamp(4)},
		{Record: "DK-1", Chat: "chat-a", Source: ByWork, Time: stamp(5)},
	}
	segs := Segments(recs)
	want := []Segment{
		{Chat: "chat-a", Source: ByHand, Since: stamp(1), Until: stamp(3)},
		{Chat: "chat-b", Source: ByMove, Since: stamp(3), Until: stamp(4)},
		{Chat: "chat-a", Source: ByWork, Since: stamp(5)},
	}
	if len(segs) != len(want) {
		t.Fatalf("отрезков %d, жду %d: %+v", len(segs), len(want), segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("отрезок %d: %+v, жду %+v", i, segs[i], want[i])
		}
	}
}

func put(t *testing.T, home string, b Bind) error {
	t.Helper()
	return Put(home, b)
}

// Инвариант «разговор основной не больше чем для одной записи»: писатель
// отказывает под замком, журнал остаётся нетронутым, а отказ называет запись,
// держащую разговор.
func TestPutRefusesHeldChat(t *testing.T) {
	home := t.TempDir()
	if err := put(t, home, Bind{Record: "DK-1", Chat: "chat-x", Project: "devkit", Source: ByHand}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(Path(home))
	err := put(t, home, Bind{Record: "DK-2", Chat: "chat-x", Project: "devkit", Source: ByHand})
	var held *HeldError
	if !errors.As(err, &held) || held.Record != "DK-1" {
		t.Fatalf("захваченный разговор отдался второй записи: %v", err)
	}
	after, _ := os.ReadFile(Path(home))
	if string(before) != string(after) {
		t.Fatalf("отказ дописал строку:\n%s", after)
	}
	// Повторное назначение той же записи тем же разговором это не захват:
	// повторный запуск работы не создаёт второй чат и не меняет носителя.
	if err := put(t, home, Bind{Record: "DK-1", Chat: "chat-x", Project: "devkit", Source: ByWork}); err != nil {
		t.Fatalf("повтор того же назначения отказал: %v", err)
	}
}

// Инвариант «у записи не больше одного основного»: новая строка записи
// заменяет её действующее назначение свёрткой, прежний разговор освобождается
// и может стать основным другой записи.
func TestPutReplacesRecordsCarrier(t *testing.T) {
	home := t.TempDir()
	if err := put(t, home, Bind{Record: "DK-1", Chat: "chat-a", Project: "devkit", Source: ByHand}); err != nil {
		t.Fatal(err)
	}
	if err := put(t, home, Bind{Record: "DK-1", Chat: "chat-b", Project: "devkit", Source: ByMove, Prior: "chat-a"}); err != nil {
		t.Fatal(err)
	}
	binds := Load(home)
	if binds["DK-1"].Chat != "chat-b" || binds["DK-1"].Prior != "chat-a" {
		t.Fatalf("свёртка не заменила носителя записи: %+v", binds["DK-1"])
	}
	// Освободившийся разговор достаётся другой записи без отказа.
	if err := put(t, home, Bind{Record: "DK-2", Chat: "chat-a", Project: "devkit", Source: ByHand}); err != nil {
		t.Fatalf("освободившийся разговор не назначился: %v", err)
	}
	// У записи без ID назначения не бывает.
	if err := put(t, home, Bind{Chat: "chat-c", Source: ByHand}); err != ErrBadRecord {
		t.Fatalf("запись без ID прошла: %v", err)
	}
}

// Откат идёт обратными строками: назначение, снятие и новое назначение лежат
// в журнале по порядку, ничего не переписывается и не стирается, а снятая
// строка читается свёрткой как «разговора нет».
func TestRollbackByReverseLines(t *testing.T) {
	home := t.TempDir()
	for _, b := range []Bind{
		{Record: "DK-1", Chat: "chat-a", Project: "devkit", Source: ByWork},
		{Record: "DK-1", Source: ByOff},
		{Record: "DK-1", Chat: "chat-b", Project: "devkit", Source: ByWork},
	} {
		if err := put(t, home, b); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("журнал переписан, строк %d:\n%s", len(lines), data)
	}
	if !strings.Contains(lines[0], "chat-a") || !strings.Contains(lines[1], "разговор -") {
		t.Fatalf("строки отката легли не тем порядком:\n%s", data)
	}
	if got := Load(home)["DK-1"]; got.Chat != "chat-b" {
		t.Fatalf("после отката действует не новое назначение: %+v", got)
	}
}

// Замок журнала того же порядка, что вход разговора: пока замок держит
// соседний писатель, дописывание ждёт его и отказывает словами internal/chat,
// а не пишет поверх.
func TestPutWaitsForLock(t *testing.T) {
	home := t.TempDir()
	lock, err := chat.Lock(Path(home) + chat.LockSuffix)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	done := make(chan error, 1)
	go func() {
		done <- Put(home, Bind{Record: "DK-1", Chat: "chat-a", Source: ByHand})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, chat.ErrLocked) {
			t.Fatalf("занятый замок отдал не отказ замка: %v", err)
		}
	case <-time.After(chat.LockWait + 3*time.Second):
		t.Fatal("писатель не подождал замок и не отказал")
	}
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("под чужим замком журнал тронут: %v", err)
	}
}

// Разросшийся журнал режется хвостом по берегам реестра сессий, и действующее
// назначение хвостом не теряется: строки работы у назначений это состояние,
// а не переписываемый факт.
func TestAppendTrimsTailOnly(t *testing.T) {
	home := t.TempDir()
	path := Path(home)
	fat := strings.Repeat("x", 120)
	for i := 0; i < sessions.Keep+20; i++ {
		line := stamp(0) + " назначение DK-" + strconv.Itoa(i) + " разговор chat-" + strconv.Itoa(i) +
			" источник работа прежде " + fat + "\n"
		if err := appendLine(path, line); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < sessions.Keep || len(lines) > sessions.Keep+10 {
		t.Fatalf("после обрезки строк %d, жду %d с небольшим потолком", len(lines), sessions.Keep)
	}
	if strings.Contains(lines[0], "назначение DK-0 ") {
		t.Error("обрезка не тронула голову журнала")
	}
}

func TestPathJoinsHome(t *testing.T) {
	if got := Path("/tmp/h"); got != filepath.Join("/tmp/h", ".devkit", "main-chat.log") {
		t.Fatalf("путь журнала: %s", got)
	}
}
