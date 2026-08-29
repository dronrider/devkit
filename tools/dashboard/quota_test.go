package main

import (
	"encoding/json"
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

// Пометка неполноты, которую кладёт agentctl, до экрана остатка не доходит и
// разбор не ломает. Дашборд читает снимок сам (LLD DK-112), и всё, что не
// taken, он держит за бакет: приди пометка ключом, человек увидел бы «бакет
// partial не разобран» вместо цифры. Строка снимка тут дословная, из
// agentctl/quota.go.
func TestQuotaPartialNoteIgnored(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return quotaNow }
	writeQuota(t, e.home, "harness-one", "taken = 2026-08-11T13:23\n"+
		"week_all = 12% сброс 2026-08-17T15:00\n"+
		"# partial week_max: свежей разбивки клиент не получил, на панели она из его кеша\n")

	h := getQuota(t, e).Harnesses[0]
	if len(h.Warns) != 0 {
		t.Fatalf("пометка прочиталась как битая строка: %+v", h.Warns)
	}
	if len(h.Buckets) != 1 || h.Buckets[0].Name != "week_all" {
		t.Fatalf("пометка попала на экран бакетом: %+v", h.Buckets)
	}
	if strings.Contains(h.Note, "partial") {
		t.Fatalf("пометка вылезла подписью подписки: %q", h.Note)
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
