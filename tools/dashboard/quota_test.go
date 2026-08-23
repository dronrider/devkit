package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Снимки квоты сняты с живого каталога ~/.devkit/quota: формат «key = value»,
// момент снятия и даты сброса местным временем без секунд.
const (
	quotaFixtureA = "taken = 2026-08-11T13:23\n" +
		"week_all = 12% сброс 2026-08-17T15:00\n" +
		"week_max = 13% сброс 2026-08-17T15:00\n"
	quotaFixtureB = "# снято руками\ntaken = 2026-08-11T13:10\n" +
		"month_tokens = 40% сброс 2026-09-01T00:00\n"
)

// quotaNow это «сейчас» тестов: через семь минут после снимка A.
var quotaNow = time.Date(2026, 8, 11, 13, 30, 0, 0, time.Local)

func writeQuota(t *testing.T, home, harness, body string) {
	t.Helper()
	dir := filepath.Join(home, ".devkit", "quota")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, harness+".local"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getQuota(t *testing.T, e *testEnv) QuotaView {
	t.Helper()
	c := e.loggedClient(t)
	resp, err := c.Get(e.srv.URL + "/api/quota")
	if err != nil {
		t.Fatal(err)
	}
	var out QuotaView
	if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil {
		t.Fatalf("ответ квоты не разобрался: %v", err)
	}
	return out
}

// Две подписки разных харнесов читаются одним разбором: имя берётся из имени
// файла, бакеты из его строк, и порядок в ответе устойчивый.
func TestQuotaTwoHarnesses(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }
	writeQuota(t, e.home, "harness-two", quotaFixtureB)
	writeQuota(t, e.home, "harness-one", quotaFixtureA)

	view := getQuota(t, e)
	if view.Note != "" {
		t.Fatalf("снимки есть, а ответ жалуется: %s", view.Note)
	}
	if len(view.Harnesses) != 2 {
		t.Fatalf("жду две подписки, вижу %d: %+v", len(view.Harnesses), view.Harnesses)
	}
	one, two := view.Harnesses[0], view.Harnesses[1]
	if one.Name != "harness-one" || two.Name != "harness-two" {
		t.Fatalf("имена подписок не по каталогу: %s, %s", one.Name, two.Name)
	}
	if one.Stale {
		t.Fatalf("снимок семиминутной давности не протух, а помечен: %+v", one)
	}
	if one.Age != "7м" {
		t.Fatalf("возраст снимка %q, жду 7м", one.Age)
	}
	if one.Taken != "2026-08-11T13:23" {
		t.Fatalf("момент снятия %q", one.Taken)
	}
	if len(one.Buckets) != 2 {
		t.Fatalf("жду два бакета, вижу %+v", one.Buckets)
	}
	if one.Buckets[0].Name != "week_all" || one.Buckets[0].Used != 12 ||
		one.Buckets[0].Reset != "2026-08-17T15:00" {
		t.Fatalf("первый бакет разобран не так: %+v", one.Buckets[0])
	}
	if len(two.Buckets) != 1 || two.Buckets[0].Name != "month_tokens" || two.Buckets[0].Used != 40 {
		t.Fatalf("второй харнес разобран не так: %+v", two.Buckets)
	}
}

// Протухший снимок подписан честно: показ остатка не должен выглядеть свежим
// там, где сам выбор моделей снимку уже не верит.
func TestQuotaStale(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow.Add(3 * time.Hour) }
	writeQuota(t, e.home, "harness-one", quotaFixtureA)

	view := getQuota(t, e)
	h := view.Harnesses[0]
	if !h.Stale {
		t.Fatalf("снимок трёхчасовой давности при пороге %s не помечен протухшим: %+v", quotaMaxAge, h)
	}
	if h.Age != "3ч 7м" {
		t.Fatalf("возраст протухшего снимка %q, жду 3ч 7м", h.Age)
	}
	if len(h.Buckets) != 2 {
		t.Fatalf("протухший снимок потерял бакеты: %+v", h.Buckets)
	}
}

// Снимок без момента снятия это неизвестный возраст, а не свежесть.
func TestQuotaNoTaken(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }
	writeQuota(t, e.home, "harness-one", "week_all = 12% сброс 2026-08-17T15:00\n")

	h := getQuota(t, e).Harnesses[0]
	if !h.Stale || !strings.Contains(h.Note, "момента снятия") {
		t.Fatalf("снимок без момента снятия подписан не так: %+v", h)
	}
}

// Каталога нет и каталог без снимков это разные пустоты, и обе говорят
// словами, чего не хватает и чем это чинится.
func TestQuotaEmpties(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }

	view := getQuota(t, e)
	if len(view.Harnesses) != 0 || !strings.Contains(view.Note, "каталога") ||
		!strings.Contains(view.Note, "agentctl quota refresh") {
		t.Fatalf("пустота без каталога названа не так: %+v", view)
	}

	if err := os.MkdirAll(filepath.Join(e.home, ".devkit", "quota"), 0o755); err != nil {
		t.Fatal(err)
	}
	view = getQuota(t, e)
	if len(view.Harnesses) != 0 || !strings.Contains(view.Note, "нет ни одного снимка") {
		t.Fatalf("пустой каталог назван не так: %+v", view)
	}
}

// Битая строка теряет своё, а соседи по снимку остаются: снимок пишут и
// руками, и опечатка в одной строке не должна гасить блок целиком.
func TestQuotaBrokenLines(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }
	writeQuota(t, e.home, "harness-one", "taken = 2026-08-11T13:23\n"+
		"week_all 12%\nweek_max = много% сброс 2026-08-17T15:00\n"+
		"week_opus = 7% сброс 2026-08-17T15:00\n")

	h := getQuota(t, e).Harnesses[0]
	if len(h.Buckets) != 1 || h.Buckets[0].Name != "week_opus" {
		t.Fatalf("целая строка снимка потерялась: %+v", h.Buckets)
	}
	if len(h.Warns) != 2 {
		t.Fatalf("жду два предупреждения на две битые строки, вижу %+v", h.Warns)
	}
}

// Бакет без даты сброса читается: сброс это подпись, а не условие показа.
func TestQuotaBucketWithoutReset(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }
	writeQuota(t, e.home, "harness-one", "taken = 2026-08-11T13:23\nweek_all = 12%\n")

	h := getQuota(t, e).Harnesses[0]
	if len(h.Buckets) != 1 || h.Buckets[0].Used != 12 || h.Buckets[0].Reset != "" {
		t.Fatalf("бакет без сброса разобран не так: %+v", h.Buckets)
	}
}

func TestQuotaNeedsLogin(t *testing.T) {
	e := newTestEnv(t)
	resp, err := plainClient().Get(e.srv.URL + "/api/quota")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("квота без входа отдала %d", resp.StatusCode)
	}
}

// Ни одного имени харнеса в коде: список подписок это содержимое каталога, и
// стоит завести новую, она появится на экране без правки дашборда. Имена
// берутся из профилей репозитория, поэтому новый профиль попадает под проверку
// сам; список рядом держит рубеж и там, где профили не под рукой.
func TestQuotaNoHarnessNamesInCode(t *testing.T) {
	names := map[string]bool{"claude-code": true, "glm-code": true}
	if entries, err := os.ReadDir(filepath.FromSlash("../../kit/harness")); err == nil {
		for _, e := range entries {
			if n := strings.TrimSuffix(e.Name(), ".toml"); !e.IsDir() && n != e.Name() {
				names[n] = true
			}
		}
	}
	for _, dir := range []string{".", "static"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for name := range names {
				if strings.Contains(string(data), name) {
					t.Errorf("%s называет харнес %q: подписки берутся из каталога снимков", path, name)
				}
			}
		}
	}
}

// Живой случай пользователя: снимок квоты стоял трёхчасовой давности, а в
// журнале каждые десять минут повторялось «claude спрашивает про доверие
// каталогу». Обновление шло из рабочего каталога демона, а под launchd он к
// дереву пользователя отношения не имеет, и клиент упирался в вопрос доверия
// вместо панели остатка.

// quotaCatch подменяет вызов обновления и запоминает каталог, из которого
// звали. Настоящий agentctl тут не поднимается: предмет проверки это каталог.
func quotaCatch(t *testing.T, err error) *string {
	t.Helper()
	dir := new(string)
	was := quotaRefreshRun
	quotaRefreshRun = func(got, bin string) error {
		*dir = got
		return err
	}
	t.Cleanup(func() { quotaRefreshRun = was })
	return dir
}

// quotaTrustSays подменяет чтение доверия клиента: список машины, на которой
// гоняют тест, к предмету проверки отношения не имеет.
func quotaTrustSays(t *testing.T, dirs ...string) {
	t.Helper()
	was := quotaTrust
	quotaTrust = func(string) map[string]bool {
		out := map[string]bool{}
		for _, d := range dirs {
			out[d] = true
		}
		return out
	}
	t.Cleanup(func() { quotaTrust = was })
}

// Каталог вызова не зависит от того, откуда запущен демон: он один и тот же до
// и после смены рабочего каталога, и рабочим каталогом процесса не является.
// Берётся дерево, доверие которому клиент уже помнит, иначе он спросит про
// доверие вместо панели остатка.
func TestQuotaRefreshDirNotCwd(t *testing.T) {
	e := newTestEnv(t)
	quotaTrustSays(t, e.proj)
	dir := quotaCatch(t, nil)

	e.s.quotaRefresh("agentctl")
	first := *dir
	if first == "" {
		t.Fatal("обновление зовётся с пустым каталогом: вызов уйдёт в рабочий каталог демона")
	}
	if !filepath.IsAbs(first) {
		t.Fatalf("каталог вызова не абсолютный: %q", first)
	}
	if first != e.proj {
		t.Fatalf("обновление зовётся не из доверенного дерева: %q, доверено %q", first, e.proj)
	}

	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	e.s.quotaRefresh("agentctl")
	if *dir != first {
		t.Fatalf("каталог вызова уехал вместе с рабочим: было %q, стало %q", first, *dir)
	}
	if *dir == wd {
		t.Fatalf("обновление зовётся из рабочего каталога демона: %q", wd)
	}
}

// Недоверенное дерево не берётся: вызов из него упёрся бы в вопрос про доверие,
// и снимок остался бы вчерашним. Откат тут дом человека, а не каталог демона.
func TestQuotaRefreshDirSkipsUntrusted(t *testing.T) {
	e := newTestEnv(t)
	quotaTrustSays(t)
	dir := quotaCatch(t, nil)

	e.s.quotaRefresh("agentctl")
	if *dir == e.proj {
		t.Fatalf("вызов ушёл в дерево, доверия которому у клиента нет: %q", *dir)
	}
	if *dir != realHome() {
		t.Fatalf("откатом взят не дом человека: %q, дом %q", *dir, realHome())
	}
}

// Доверие читается из конфига клиента, а не угадывается по списку проектов.
func TestClientTrustedReadsConfig(t *testing.T) {
	home := t.TempDir()
	conf := `{"projects":{"/a/trusted":{"hasTrustDialogAccepted":true},` +
		`"/a/asked":{"hasTrustDialogAccepted":false},"/a/plain":{}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	got := clientTrusted(home)
	if !got["/a/trusted"] {
		t.Fatalf("подтверждённое доверие не прочиталось: %+v", got)
	}
	if got["/a/asked"] || got["/a/plain"] {
		t.Fatalf("недоверенное дерево сошло за доверенное: %+v", got)
	}
	// Ни файла, ни разбора: доверенных каталогов просто нет, и падать тут нечему.
	if len(clientTrusted(t.TempDir())) != 0 {
		t.Fatal("без конфига клиента взялись доверенные каталоги")
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(clientTrusted(home)) != 0 {
		t.Fatal("битый конфиг клиента дал доверенные каталоги")
	}
}

// Причина отказа доезжает до плашки квоты: ручка отдаёт её вместе со снимками,
// коротко и с каталогом вызова, чтобы человек не гадал, почему снимок старый.
func TestQuotaFailReachesPlate(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }
	writeQuota(t, e.home, "harness-one", quotaFixtureA)
	quotaTrustSays(t, e.proj)
	quotaCatch(t, fmt.Errorf("ошибка: claude спрашивает про доверие каталогу, панель за этим "+
		"вопросом недоступна: подтвердить доверие руками (claude в этом каталоге) либо "+
		"гонять refresh из каталога, которому клиент уже доверяет"))

	if view := getQuota(t, e); view.Fail != nil {
		t.Fatalf("отказ взялся до единой попытки: %+v", view.Fail)
	}

	e.s.quotaRefresh("agentctl")
	view := getQuota(t, e)
	if view.Fail == nil {
		t.Fatal("плашка молчит об отказе: человек остаётся со старым снимком без объяснения")
	}
	if !strings.Contains(view.Fail.Reason, "доверие каталогу") {
		t.Fatalf("причина отказа не названа: %q", view.Fail.Reason)
	}
	if strings.Contains(view.Fail.Reason, "подтвердить доверие руками") {
		t.Fatalf("в плашку уехал совет с командами, а не причина: %q", view.Fail.Reason)
	}
	if view.Fail.Dir != e.proj {
		t.Fatalf("отказ не назвал каталог вызова: %q", view.Fail.Dir)
	}
	if view.Fail.Age == "" {
		t.Fatal("отказ без возраста: свежая попытка неотличима от вчерашней")
	}
	// Снимки при этом на месте: отказ обновления не отменяет того, что уже снято.
	if len(view.Harnesses) != 1 {
		t.Fatalf("снимки пропали вместе с отказом: %+v", view.Harnesses)
	}

	// Обновление прошло, отказ снят: старая причина не висит на экране вечно.
	quotaCatch(t, nil)
	e.s.quotaRefresh("agentctl")
	if view := getQuota(t, e); view.Fail != nil {
		t.Fatalf("отказ остался после удачного обновления: %+v", view.Fail)
	}
}

// Журнал пишется сменой исхода, а не каждым тиком: тик десятиминутный, и одна
// и та же строка про доверие каталогу росла в журнале часами.
func TestQuotaFailLoggedOnce(t *testing.T) {
	e := newTestEnv(t)
	said := []string{}
	e.s.logf = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }
	quotaTrustSays(t, e.proj)
	quotaCatch(t, fmt.Errorf("ошибка: claude спрашивает про доверие каталогу, панель за этим вопросом недоступна"))

	for i := 0; i < 3; i++ {
		e.s.quotaRefresh("agentctl")
	}
	if len(said) != 1 {
		t.Fatalf("одна и та же причина написана %d раз: %q", len(said), said)
	}
	if !strings.Contains(said[0], "доверие каталогу") || !strings.Contains(said[0], e.proj) {
		t.Fatalf("в журнале нет ни причины, ни каталога вызова: %q", said[0])
	}

	// Сменившаяся причина это новость, её журнал называет.
	quotaCatch(t, fmt.Errorf("ошибка: claude не залогинен, снимать нечего"))
	e.s.quotaRefresh("agentctl")
	if len(said) != 2 {
		t.Fatalf("сменившаяся причина не попала в журнал: %q", said)
	}

	// Как и возвращение к работе: молчание тут неотличимо от «всё ещё стоит».
	quotaCatch(t, nil)
	e.s.quotaRefresh("agentctl")
	e.s.quotaRefresh("agentctl")
	if len(said) != 3 {
		t.Fatalf("возвращение к работе написано %d строками: %q", len(said)-2, said)
	}
}

// Сжатие причины: в плашку идёт первая фраза, а совет с командами остаётся
// журналу. Голое «ошибка» причиной не считается.
func TestQuotaFailWords(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ошибка: claude не залогинен, снимать нечего: пройти вход и повторить",
			"claude не залогинен, снимать нечего"},
		{"agentctl не ответил за 2m0s и снят по сроку", "agentctl не ответил за 2m0s и снят по сроку"},
		{"  ошибка: короткая\nвторая строка  ", "короткая"},
	}
	for _, c := range cases {
		if got := quotaFailWords(c.in); got != c.want {
			t.Fatalf("причина сжалась не так: %q -> %q, ждали %q", c.in, got, c.want)
		}
	}
	long := "ошибка: " + strings.Repeat("длинн", 60)
	if got := quotaFailWords(long); len([]rune(got)) > quotaFailMax+3 {
		t.Fatalf("длинная причина не подрезана: %d знаков", len([]rune(got)))
	}
}
