package machload

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

// Живой вывод ps с выключенными заголовками: колонки pid, ppid, pcpu и rss,
// между ними пробелы разной ширины. Битые строки стоят среди живых нарочно:
// разбор обязан пропустить их молча, не роняя снимок.
const psFixture = "  501   480  12.3  45678\n" +
	" 1001   501   3.4 123456\n" +
	"эта строка битая\n" +
	" 1002   501  -1.0     0\n"

func TestParseProcsSkipsBrokenLines(t *testing.T) {
	procs, err := ParseProcs(psFixture)
	if err != nil {
		t.Fatal(err)
	}
	want := []Proc{
		{PID: 501, PPID: 480, PCPU: 12.3, RSSKB: 45678},
		{PID: 1001, PPID: 501, PCPU: 3.4, RSSKB: 123456},
		{PID: 1002, PPID: 501, PCPU: -1.0, RSSKB: 0},
	}
	if !reflect.DeepEqual(procs, want) {
		t.Fatalf("снимок %v, жду %v", procs, want)
	}
}

func TestParseProcsRefusesSilence(t *testing.T) {
	if _, err := ParseProcs(""); err == nil {
		t.Fatal("пустой вывод ps разобрался: молчание читалось бы машиной без процессов")
	}
	if _, err := ParseProcs("только слова без чисел\n"); err == nil {
		t.Fatal("вывод без процессов разобрался: снимок обязан отказать")
	}
}

// Дерево процессов развилки атрибуции: у сессии свои дети и внуки, дашборд
// поднят из сессии, но корнем остаётся себе, а одинокий процесс без корня
// уезжает в прочее.
var treeFixture = []Proc{
	{PID: 100, PPID: 1, PCPU: 10, RSSKB: 100000},
	{PID: 110, PPID: 100, PCPU: 20, RSSKB: 200000},
	{PID: 111, PPID: 110, PCPU: 5, RSSKB: 50000},
	{PID: 200, PPID: 100, PCPU: 7, RSSKB: 70000},
	{PID: 210, PPID: 200, PCPU: 3, RSSKB: 30000},
	{PID: 300, PPID: 1, PCPU: 1, RSSKB: 1000},
}

func TestAttributeNearestRootWins(t *testing.T) {
	roots := map[int]string{100: "сессия один", 200: "дашборд"}
	got := Attribute(treeFixture, roots)
	want := map[string]Spread{
		// Дети и внуки считаются за сессию целиком.
		"сессия один": {CPU: 35, RSSKB: 350000},
		// Дашборд поднят из сессии, но ближайший корень ему это он сам, и его
		// дети уходят с ним, а не в сессию.
		"дашборд": {CPU: 10, RSSKB: 100000},
		// Процесс без корня это прочее.
		Other: {CPU: 1, RSSKB: 1000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("доли %v, жду %v", got, want)
	}
}

func TestAttributeEmptyRootsAllOther(t *testing.T) {
	got := Attribute(treeFixture, nil)
	total := Total(treeFixture)
	if got[Other] != total {
		t.Fatalf("без корней всё прочее %v, жду всю сумму %v", got[Other], total)
	}
	if len(got) != 1 {
		t.Fatalf("без корней долей %d, жду одну", len(got))
	}
}

// Цикл в родителях бывает у живого ps при гонке съёма: процесс умер, таблица
// переехала, и цепочка сошлась на себе. Разбор обязан закончить обход, а не
// кружить по таблице.
func TestAttributeSurvivesParentCycle(t *testing.T) {
	cycle := []Proc{
		{PID: 1, PPID: 2, PCPU: 1, RSSKB: 10},
		{PID: 2, PPID: 1, PCPU: 2, RSSKB: 20},
	}
	got := Attribute(cycle, map[int]string{999: "сессия"})
	if got[Other] != (Spread{CPU: 3, RSSKB: 30}) {
		t.Fatalf("цикл в родителях не свёрнут в прочее: %v", got)
	}
}

// Живой вывод vm_stat: размер страницы в заголовке, страницы по состояниям с
// точкой на конце, строка компрессора в кавычках. Строки-новички, которых
// разбор не знает, стоят среди живых и пропускаются.
const vmStatFixture = "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n" +
	"Pages free:                             100000.\n" +
	"Pages speculative:                       5000.\n" +
	"Pages inactive:                         20000.\n" +
	"Pages active:                           60000.\n" +
	"Pages wired down:                       30000.\n" +
	"Pages throttled:                          100.\n" +
	"Pages purgeable:                         2000.\n" +
	"\"Pages occupied by compressor\":         10000.\n" +
	"Pages testline:                             1.\n"

func TestParseVMStatCountsPressure(t *testing.T) {
	const total = int64(16 * 1024 * 1024 * 1024)
	mem, err := ParseVMStat(vmStatFixture, total)
	if err != nil {
		t.Fatal(err)
	}
	// Занятая это активные, прибитые, сжатые и придушенные без освобождаемых.
	const wantUsed = (60000 + 30000 + 10000 + 100 - 2000) * 16384
	if mem.Used != wantUsed {
		t.Fatalf("занято %d байт, жду %d", mem.Used, wantUsed)
	}
	if mem.Total != total {
		t.Fatalf("вся память %d, жду %d", mem.Total, total)
	}
	wantPct := float64(wantUsed) / float64(total) * 100
	if math.Abs(mem.UsedPct-wantPct) > 1e-9 {
		t.Fatalf("доля занятой %g, жду %g", mem.UsedPct, wantPct)
	}
}

func TestParseVMStatRefusesSilence(t *testing.T) {
	if _, err := ParseVMStat("Mach Virtual Memory Statistics: (page size of 16384 bytes)\n", 1); err == nil {
		t.Fatal("вывод без страниц разобрался: снимок обязан отказать")
	}
	if _, err := ParseVMStat(vmStatFixture, 0); err == nil {
		t.Fatal("объём всей памяти не назван: снимок обязан отказать")
	}
}

// loadavgRaw собирает структуру средней нагрузки с масштабом в длинном поле:
// три числа с фиксированной точкой и масштаб u64 на смещении шестнадцать.
func loadavgRaw(one, five, fifteen uint32, scale uint64) []byte {
	out := make([]byte, 24)
	binary.LittleEndian.PutUint32(out[0:], one)
	binary.LittleEndian.PutUint32(out[4:], five)
	binary.LittleEndian.PutUint32(out[8:], fifteen)
	binary.LittleEndian.PutUint64(out[16:], scale)
	return out
}

func TestParseLoadAvgLongScale(t *testing.T) {
	got, err := ParseLoadAvg(loadavgRaw(1536, 1280, 1024, 1024))
	if err != nil {
		t.Fatal(err)
	}
	want := [3]float64{1.5, 1.25, 1}
	if got != want {
		t.Fatalf("средняя %v, жду %v", got, want)
	}
}

// Живой vm.loadavg приходит усечённым до двадцати трёх байт: срезанный ноль
// принадлежал масштабу, и разбор дочитывает его нулём.
func TestParseLoadAvgTrimmedScale(t *testing.T) {
	got, err := ParseLoadAvg(loadavgRaw(2048, 4096, 6144, 2048)[:23])
	if err != nil {
		t.Fatal(err)
	}
	if got != [3]float64{1, 2, 3} {
		t.Fatalf("средняя %v, жду единицу, двойку и тройку", got)
	}
}

// Короткая структура отдаёт масштаб целым на смещении двенадцать, и разбор
// обязан прочесть и её.
func TestParseLoadAvgShortScale(t *testing.T) {
	raw := loadavgRaw(2048, 2048, 2048, 1)[:16]
	binary.LittleEndian.PutUint32(raw[12:], 2048)
	got, err := ParseLoadAvg(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != [3]float64{1, 1, 1} {
		t.Fatalf("средняя %v, жду единицы", got)
	}
}

func TestParseLoadAvgRefusesBrokenScale(t *testing.T) {
	if _, err := ParseLoadAvg([]byte{1, 2, 3}); err == nil {
		t.Fatal("обрубок структуры разобрался: чтение обязано отказать")
	}
	// Масштаб ноль подменяется умолчанием, а вот число сверх всякого смысла
	// отказывает словами, а не едет на экран.
	broken := loadavgRaw(^uint32(0), 0, 0, 1)
	if _, err := ParseLoadAvg(broken); err == nil {
		t.Fatal("бессмысленное значение средней нагрузки прошло на экран")
	}
}

func TestParseBytes(t *testing.T) {
	raw := make([]byte, 8)
	binary.LittleEndian.PutUint64(raw, 17179869184)
	got, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != 17179869184 {
		t.Fatalf("прочитано %d, жду 17179869184", got)
	}
	// Живой hw.memsize машины с памятью 32 ГБ: библиотека срезала замыкающий
	// ноль, и число пришло семью байтами от восьми.
	got, err = ParseBytes([]byte{0, 0, 0, 0, 8, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if got != 34359738368 {
		t.Fatalf("обрезанное число прочитано %d, жду 34359738368", got)
	}
	if _, err := ParseBytes(raw[:4]); err == nil {
		t.Fatal("обрубок числа прочитался: разбор обязан отказать")
	}
}

// LoadAvg идёт через шов системного вызова: стенд кладёт сырой ответ и не
// зависит от системы прогона.
func TestLoadAvgThroughSeam(t *testing.T) {
	old := sysctlRead
	sysctlRead = func(name string) ([]byte, error) { return loadavgRaw(1024, 2048, 3072, 1024), nil }
	defer func() { sysctlRead = old }()
	got, err := LoadAvg()
	if err != nil {
		t.Fatal(err)
	}
	if got != [3]float64{1, 2, 3} {
		t.Fatalf("средняя %v, жду единицу, двойку и тройку", got)
	}
}
