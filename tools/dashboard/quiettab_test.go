package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Вкладка, забытая открытой, грела машину, с которой в дашборд только смотрели
// (DK-986). Причин было две, и сторожат их тут две проверки.
//
// Первая про опросы. Их десяток, ходили они по таймеру и на видимость вкладки
// не смотрели вовсе: скрытая вкладка спрашивала сервер столько же раз, сколько
// открытая. Выключатель теперь один на все (pollEvery и pollOnce в app.js), и
// заведённый мимо него опрос это возврат поломки.
//
// Вторая про вечные анимации. Моргание кнопки чата, точки состояния и кольца
// задачи гоняли box-shadow, border-color и stroke, а это перерисовка кадра:
// одна живая строка доски давала один вечный цикл, десять строк десять. Вечная
// анимация теперь трогает только прозрачность и превращение, то есть то, что
// браузер отдаёт композитору.

// pollNames это опросы дашборда, у которых частота названа своей постоянной.
// Каждый из них обязан ходить через выключатель видимости.
var pollNames = []string{
	"OUTBOX_POLL", "PULSE_POLL", "ASK_POLL", "WAIT_POLL", "SESS_POLL", "DRAFT_GROOM_POLL",
}

// TestStaticPollsGoThroughVisibilityGate: голого таймера у периодического
// опроса нет ни одного.
func TestStaticPollsGoThroughVisibilityGate(t *testing.T) {
	app := readStatic(t, "app.js")
	if strings.Contains(app, "setInterval(") {
		t.Errorf("в app.js остался setInterval: опрос мимо выключателя видимости не гаснет на скрытой вкладке")
	}
	if !strings.Contains(app, `document.visibilityState === "hidden"`) ||
		!strings.Contains(app, "function pollEvery(") || !strings.Contains(app, "function pollOnce(") {
		t.Fatal("выключателя видимости в app.js нет: опросы гасить нечем")
	}
	// Догон идёт лесенкой: первый ряд сразу, остальные по ступеньке (замечание
	// ревью). Поведение замеряет TestDashboardSmokeHiddenTabQuiet настоящим
	// браузером, а тут сторожатся сами опоры, чтобы правка не сняла их на
	// машине без chrome.
	if !strings.Contains(app, "const WAKE_STEP") || !strings.Contains(app, "poll.eager") {
		t.Errorf("возврат к вкладке будит опросы разом: ни ступеньки (WAKE_STEP), ни первого ряда (eager) в app.js нет")
	}
	// Повторный уход в фон посреди лесенки снимает недобуженные ступеньки, и
	// держат это две опоры разом (замечание ревью). Заведённые ступеньки
	// снимаются по своим id, а разбуженный круг сам смотрит на видимость перед
	// запросом: отложенный вызов успевает прийти между двумя строками.
	if !strings.Contains(app, "stairsDrop") {
		t.Errorf("ступеньки лесенки нигде не хранятся: повторный уход в фон их не снимет")
	}
	if !strings.Contains(app, "if (poll.dead || hiddenTab()) return;") {
		t.Errorf("круг pollEvery не смотрит на видимость перед запросом: ступенька уйдёт на сервер со скрытой вкладки")
	}
	for _, name := range pollNames {
		gate := regexp.MustCompile(`poll(Every|Once)\(\s*` + name + `\b`)
		if !gate.MatchString(app) {
			t.Errorf("опрос %s заведён мимо pollEvery/pollOnce: скрытая вкладка его не погасит", name)
		}
		bare := regexp.MustCompile(`set(Timeout|Interval)\([^;]*?,\s*` + name + `\b`)
		if bare.MatchString(app) {
			t.Errorf("опрос %s висит на голом таймере: скрытая вкладка спрашивает сервер вхолостую", name)
		}
	}
}

// TestStaticEndlessAnimationsAreComposite: вечная анимация меняет только
// прозрачность и превращение.
func TestStaticEndlessAnimationsAreComposite(t *testing.T) {
	css := readStatic(t, "style.css")
	frames := keyframeBodies(css)
	endless := regexp.MustCompile(`animation:([^;}]*infinite[^;}]*)`)
	seen := 0
	for _, hit := range endless.FindAllStringSubmatch(css, -1) {
		name := ""
		for _, word := range strings.Fields(hit[1]) {
			if _, ok := frames[word]; ok {
				name = word
				break
			}
		}
		if name == "" {
			t.Errorf("дорожка вечной анимации не нашлась по имени: %q", strings.TrimSpace(hit[1]))
			continue
		}
		seen++
		for _, prop := range frameProps(frames[name]) {
			if prop == "opacity" || prop == "transform" {
				continue
			}
			t.Errorf("вечная анимация %s меняет %s: браузер перерисовывает кадр, пока строка жива", name, prop)
		}
	}
	if seen < 4 {
		t.Fatalf("вечных анимаций нашлось %d: проверка смотрит не туда", seen)
	}
}

// keyframeBodies достаёт тела дорожек из style.css по именам.
func keyframeBodies(css string) map[string]string {
	out := map[string]string{}
	head := regexp.MustCompile(`@keyframes\s+([\w-]+)\s*\{`)
	for _, at := range head.FindAllStringSubmatchIndex(css, -1) {
		name := css[at[2]:at[3]]
		depth := 1
		i := at[1]
		start := i
		for i < len(css) && depth > 0 {
			switch css[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			i++
		}
		out[name] = css[start : i-1]
	}
	return out
}

// frameProps называет свойства, которые дорожка меняет от кадра к кадру.
func frameProps(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, decl := range regexp.MustCompile(`[{;]\s*([\w-]+)\s*:`).FindAllStringSubmatch(body, -1) {
		name := decl[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func readStatic(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("static", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Молчание скрытой вкладки проверяется и поведением, а не одним текстом
// исходника: стенд гоняет настоящий app.js поверх мока дерева, уводит вкладку
// в фон и смотрит, заведётся ли следующий круг опроса. Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticHiddenTabStopsPolling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд молчания скрытой вкладки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_hiddentab.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("молчание скрытой вкладки: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
