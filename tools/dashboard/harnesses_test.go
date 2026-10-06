package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Подписки машины и запуск на выбранной (DK-326). Живого agentctl в тестах нет,
// его место занимает фикстура в PATH: проверяется дорога от машинного вида
// утилиты до команды tmux-сессии, а не сама утилита, у неё свои тесты.

// harnessJSONFixture это ответ agentctl harness --json с тремя харнесами:
// включённый по умолчанию, включённая вторая подписка и невключённый сосед.
const harnessJSONFixture = `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1"},
    {"name": "втораяtest", "enabled": true, "default": false, "bin": "клиент-2", "env": ["CONFIG_DIR"]},
    {"name": "третьяtest", "enabled": false, "default": false, "bin": "клиент-3"}
  ]
}`

// harnessTiersFixture это та же раскладка с лестницей ярусов: по ней ярус
// разворачивается в модель. Без неё стенды разбора зависели бы от машины
// разработчика, где на вопрос о ярусах отвечает живой agentctl.
const harnessTiersFixture = `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
     "models": [{"tier": "base", "model": "модель-base"}, {"tier": "pro", "model": "модель-pro"}]},
    {"name": "втораяtest", "enabled": true, "default": false, "bin": "клиент-2", "env": ["CONFIG_DIR"],
     "models": [{"tier": "base", "model": "вторая-base"}, {"tier": "pro", "model": "вторая-pro"}]}
  ]
}`

func writeAgentctlFake(t *testing.T, bin, out string) {
	t.Helper()
	writeScript(t, bin, "agentctl", "cat <<'JSON'\n"+out+"\nJSON")
}

// writeAgentctlPick кладёт фикстуру agentctl, которая отвечает на оба вопроса
// запуска: раскладкой на harness --json и машинными строками вердикта на
// pick <ID>. Ярус вердикта называет стенд, пустой ярус это молчащий вердикт
// (утилита есть, а строки tier в ответе нет).
func writeAgentctlPick(t *testing.T, bin, layout, tier string) {
	t.Helper()
	said := "model: модель-pro\neffort: high\n"
	if tier != "" {
		said += "tier: " + tier + "\n"
	}
	writeScript(t, bin, "agentctl", "case \"$1\" in\nharness)\ncat <<'JSON'\n"+layout+
		"\nJSON\n;;\npick)\nprintf '"+said+"'\n;;\n*)\nexit 1\n;;\nesac")
}

// writeAgentctlVerdict это та же фикстура с подпиской в вердикте (строка via) и
// ответом на exec: так обёртка подписки отвечает на пробу входа. Пустой execSaid
// значит живой вход, непустой печатается и уходит кодом 1, как отказ клиента.
func writeAgentctlVerdict(t *testing.T, bin, layout, tier, via, execSaid string) {
	t.Helper()
	said := "model: модель-pro\neffort: high\ntier: " + tier + "\nvia: " + via + "\n"
	probe := "exit 0"
	if execSaid != "" {
		probe = "echo '" + execSaid + "'\nexit 1"
	}
	writeScript(t, bin, "agentctl", "case \"$1\" in\nharness)\ncat <<'JSON'\n"+layout+
		"\nJSON\n;;\npick)\nprintf '"+said+"'\n;;\nexec)\n"+probe+"\n;;\n*)\nexit 1\n;;\nesac")
}

func getHarnesses(t *testing.T, e *testEnv, c *http.Client) HarnessView {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/harnesses", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список подписок: %d %s", resp.StatusCode, text)
	}
	var v HarnessView
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ не разобрался (%v): %s", err, text)
	}
	return v
}

// Ручка отдаёт включённые подписки с их клиентами и признаком «по умолчанию»:
// из этого и собран выбор в кнопке запуска. Невключённый харнес в список не
// идёт: devkit не раскладывает ему правила, хуки и скиллы, и поднимать на нём
// работу нечестно.
func TestHarnessesFromAgentctl(t *testing.T) {
	e := newTestEnv(t)
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	v := getHarnesses(t, e, e.loggedClient(t))
	if len(v.Harnesses) != 2 {
		t.Fatalf("подписок %d, жду две включённые: %+v", len(v.Harnesses), v)
	}
	if v.Harnesses[0].Name != "перваяtest" || !v.Harnesses[0].Default || v.Harnesses[0].Bin != "клиент-1" {
		t.Errorf("подписка по умолчанию пришла как %+v", v.Harnesses[0])
	}
	if v.Harnesses[1].Default || v.Harnesses[1].Bin != "клиент-2" {
		t.Errorf("вторая подписка пришла как %+v", v.Harnesses[1])
	}
	if v.Note != "" {
		t.Errorf("при живом списке приписка лишняя: %s", v.Note)
	}
}

// Порог ротации исполнителя-субагента целиком считает agentctl и отдаёт полем
// harness --json (DK-615): дашборд берёт число как есть. Своё запасное число
// он подставляет только там, где ответа нет вовсе, и нуля снаружи не бывает.
func TestHarnessesExecRotateTokens(t *testing.T) {
	t.Run("число от agentctl", func(t *testing.T) {
		e := newTestEnv(t)
		writeAgentctlFake(t, e.bin, `{"source": "фикстура", "exec_rotate_tokens": 640000,
  "harnesses": [{"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1"}]}`)
		if v := getHarnesses(t, e, e.loggedClient(t)); v.ExecRotateTokens != 640000 {
			t.Fatalf("порог %d, жду 640000 из конфига", v.ExecRotateTokens)
		}
	})
	t.Run("agentctl числа не прислал", func(t *testing.T) {
		e := newTestEnv(t)
		writeAgentctlFake(t, e.bin, harnessJSONFixture)
		if v := getHarnesses(t, e, e.loggedClient(t)); v.ExecRotateTokens != execRotateFallback {
			t.Fatalf("порог %d, жду запасное число %d", v.ExecRotateTokens, execRotateFallback)
		}
	})
	t.Run("agentctl не нашёлся", func(t *testing.T) {
		e := newTestEnv(t)
		if v := getHarnesses(t, e, e.loggedClient(t)); v.ExecRotateTokens != execRotateFallback {
			t.Fatalf("порог %d, жду запасное число %d", v.ExecRotateTokens, execRotateFallback)
		}
	})
	// Запасное число не расходится с умолчанием agentctl: две константы в
	// разных бинарях разъезжаются молча, и заказ чата поехал бы с чужим порогом.
	t.Run("запас сходится с умолчанием agentctl", func(t *testing.T) {
		src, err := os.ReadFile(filepath.Join("..", "agentctl", "rotate.go"))
		if err != nil {
			t.Fatalf("исходник agentctl не прочитался: %v", err)
		}
		want := fmt.Sprintf("execRotateDefault = %d", execRotateFallback)
		if !strings.Contains(string(src), want) {
			t.Fatalf("в agentctl нет %q: умолчание разъехалось с запасом дашборда", want)
		}
	})
}

// Отсутствие agentctl и его отказ это причина словами, а не пустой список
// молча: без причины экран показывал бы кнопку без выбора и не говорил, почему.
func TestHarnessesNoteInsteadOfSilence(t *testing.T) {
	cases := []struct {
		name, script, want string
	}{
		{"утилиты нет", "", "agentctl не нашёлся"},
		{"утилита отказала", "echo 'ошибка: слои не прочитаны' >&2\nexit 1", "не ответил"},
		{"ответ не разобрался", "echo не-json", "не разобрался"},
		{"включённых нет", `cat <<'JSON'
{"harnesses": [], "note": "включённых харнесов нет"}
JSON`, "включённых харнесов нет"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv(t)
			if c.script != "" {
				writeScript(t, e.bin, "agentctl", c.script)
			} else {
				// PATH стенда впереди системного, но живой agentctl лежит в нём же:
				// имя бинаря подменяется, чтобы не найти ни фикстуры, ни машинного.
				agentctlBin = "agentctl-которого-нет"
				t.Cleanup(func() { agentctlBin = "agentctl" })
			}
			v := getHarnesses(t, e, e.loggedClient(t))
			if len(v.Harnesses) != 0 {
				t.Fatalf("подписки взялись из ниоткуда: %+v", v.Harnesses)
			}
			if !strings.Contains(v.Note, c.want) {
				t.Fatalf("причина %q, жду про %q", v.Note, c.want)
			}
		})
	}
}

// Харнес без клиента в списке не стоит: поднимать его нечем, и строка обещала
// бы запуск, которого не будет.
func TestHarnessesSkipsWithoutClient(t *testing.T) {
	e := newTestEnv(t)
	writeAgentctlFake(t, e.bin, `{"harnesses": [
	  {"name": "безклиента", "enabled": true, "default": true},
	  {"name": "склиентом", "enabled": true, "bin": "клиент-2"}]}`)
	v := getHarnesses(t, e, e.loggedClient(t))
	if len(v.Harnesses) != 1 || v.Harnesses[0].Name != "склиентом" {
		t.Fatalf("подписка без клиента попала в список: %+v", v.Harnesses)
	}
}

// Список подписок это данные, а не строка данных: за вход он спрятан, как и
// доска.
func TestHarnessesNeedsLogin(t *testing.T) {
	e := newTestEnv(t)
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	resp := doReq(t, plainClient(), "GET", e.srv.URL+"/api/harnesses", "")
	if got := resp.StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("список подписок без входа: %d, жду 401 (%s)", got, body(t, resp))
	}
}

// Живой agentctl до стенда не дотягивается. У разработчика он лежит в том же
// PATH, что и фикстуры, и на каждый запуск сам зовёт git полдюжины раз, а
// тесты, которые считают подпроцессы, засчитывали эти вызовы своим. Краснело
// это там, где машинный agentctl отказывал: раскладку с причиной вместо списка
// дашборд не запоминает, и каждый запрос цепочки шёл в утилиту заново, унося с
// собой её git (DK-512).
func TestStandKeepsLiveAgentctlOut(t *testing.T) {
	e := newTestEnv(t)
	if got := binPath(agentctlBin); filepath.Dir(got) != e.bin {
		t.Fatalf("agentctl стенда нашёлся по %q вместо %s: подпроцессы машинного бинаря уедут в счёт теста", got, e.bin)
	}
	v := e.s.harnesses()
	if len(v.Harnesses) == 0 || v.Harnesses[0].Name != "перваяtest" {
		t.Fatalf("раскладка подписок пришла мимо фикстуры стенда: %+v", v)
	}
}

// harnessViaFixture это лестница с уехавшей ступенью: «втораяtest» листится
// первой, а её ярус pro ссылкой стоит на «перваяtest» ту же модель, что там
// домашняя. «третьяtest» держит ту же модель домашней у себя, и это честный
// повтор, который схлопывать нельзя. Порядок харнесов взят нарочно не
// совпадающим с домашним: старый chatHarnessOf брал первую подписку по
// порядку, и без via она попадала бы не туда (DK-1177).
const harnessViaFixture = `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "втораяtest", "enabled": true, "default": false, "bin": "клиент-2",
     "models": [{"tier": "base", "model": "вторая-base"}, {"tier": "pro", "model": "модель-pro", "via": "перваяtest"}]},
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
     "models": [{"tier": "base", "model": "модель-base"}, {"tier": "pro", "model": "модель-pro"}]},
    {"name": "третьяtest", "enabled": true, "default": false, "bin": "клиент-3",
     "models": [{"tier": "pro", "model": "модель-pro"}]}
  ]
}`

// TestChatModelOptsCollapsesViaPair: в выборе моделей чата уехавшая ссылкой
// ступень своей строки не даёт, когда домашняя строка той же модели есть в
// списке, а честный повтор модели у двух подписок с домашней ступенью
// остаётся (DK-1177).
func TestChatModelOptsCollapsesViaPair(t *testing.T) {
	e := newTestEnv(t)
	writeAgentctlFake(t, e.bin, harnessViaFixture)

	opts := e.s.chatModelOpts()
	byHarness := map[string]string{}
	count := map[string]int{}
	for _, o := range opts {
		if o.Model == "модель-pro" {
			byHarness[o.Harness] = o.Model
			count["модель-pro"]++
		}
	}
	if count["модель-pro"] != 2 {
		t.Fatalf("«модель-pro» в списке %d раз, жду две строки (перваяtest и третьяtest): %+v", count["модель-pro"], opts)
	}
	if _, ok := byHarness["втораяtest"]; ok {
		t.Fatalf("уехавшая ступень втораяtest дала свою строку модели-владельца: %+v", opts)
	}
	if _, ok := byHarness["перваяtest"]; !ok {
		t.Fatalf("домашняя строка перваяtest потерялась: %+v", opts)
	}
	if _, ok := byHarness["третьяtest"]; !ok {
		t.Fatalf("честный повтор третьяtest потерялся: %+v", opts)
	}

	// chatHarnessOf ведёт на подписку-владельца, а не на первую по порядку
	// (втораяtest стоит в фикстуре раньше перваяtest).
	if h := e.s.chatHarnessOf("модель-pro", ""); h == nil || h.Name != "перваяtest" {
		t.Fatalf("chatHarnessOf(модель-pro) = %+v, жду перваяtest", h)
	}
}

// harnessChatFixture это раскладка со списком чата сверх лестницы (DK-1298):
// у «перваяtest» одна строка сверх лестницы, у «втораяtest» список повторяет её
// же ступень base и держит ещё одну свою строку.
const harnessChatFixture = `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
     "models": [{"tier": "pro", "model": "модель-pro"}],
     "chat": ["модель-чата"]},
    {"name": "втораяtest", "enabled": true, "default": false, "bin": "клиент-2",
     "models": [{"tier": "base", "model": "вторая-base"}],
     "chat": ["вторая-base", "вторая-чата"]}
  ]
}`

// TestChatModelOptsBeyondLadder: модели чата сверх лестницы показываются
// строками с подпиской-владельцем, яруса у строки нет и дефолтом она не
// становится (DK-1298). Повтор модели лестнице внутри одной подписки второй
// строки не даёт, а владелец подъёма и сверка пары ведут по списку чата так
// же, как по лестнице.
func TestChatModelOptsBeyondLadder(t *testing.T) {
	e := newTestEnv(t)
	writeAgentctlFake(t, e.bin, harnessChatFixture)

	opts := e.s.chatModelOpts()
	rows := map[string]chatModelOpt{}
	for _, o := range opts {
		rows[o.Model+"/"+o.Harness] = o
	}
	extra := rows["модель-чата/перваяtest"]
	if extra.Model == "" || extra.Tier != "" || extra.Harness != "перваяtest" || extra.Default {
		t.Fatalf("строка сверх лестницы пришла как %+v, жду строку без яруса и без дефолта", extra)
	}
	if _, ok := rows["вторая-чата/втораяtest"]; !ok {
		t.Fatalf("строка чата второйtest потерялась: %+v", opts)
	}
	count := 0
	for _, o := range opts {
		if o.Model == "вторая-base" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("«вторая-base» в списке %d раз, жду одну строку (лестница и список чата одной подписки): %+v", count, opts)
	}
	if o := rows["модель-pro/перваяtest"]; !o.Default {
		t.Fatalf("дефолт остался на ярусе pro, а строка пришла как %+v", o)
	}

	// Владелец строки чата это её подписка: и с именем в ключе, и без него.
	if h := e.s.chatHarnessOf("модель-чата", ""); h == nil || h.Name != "перваяtest" {
		t.Fatalf("chatHarnessOf(модель-чата) = %+v, жду перваяtest", h)
	}
	if h := e.s.chatHarnessOf("модель-чата", "перваяtest"); h == nil || h.Name != "перваяtest" {
		t.Fatalf("chatHarnessOf(модель-чата, перваяtest) = %+v, жду перваяtest", h)
	}
	// Сверка пары пускает строку своего списка и отказывает чужой подписке.
	if !e.s.chatPairKnown("модель-чата", "перваяtest") {
		t.Fatalf("пара модель-чата/перваяtest отвергнута, жду пропуск")
	}
	if e.s.chatPairKnown("модель-чата", "втораяtest") {
		t.Fatalf("модель-чата прошла сверку с чужой подпиской втораяtest")
	}
}

// Список чата доезжает и до ручки подписок: транспорт сквозной, панель
// разговора собирает выбор из ответа /api/chats, а экран запуска читает
// /api/harnesses, и поля у обоих повторяют машинный вид. Ответ разбирается
// собственным типом теста, а не Harness пакета: на старом коде поля Chat там
// ещё нет, и краснота regcheck была бы ошибкой сборки, а не честным прогоном.
func TestHarnessesCarriesChat(t *testing.T) {
	e := newTestEnv(t)
	writeAgentctlFake(t, e.bin, harnessChatFixture)
	resp := doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/harnesses", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список подписок: %d %s", resp.StatusCode, text)
	}
	var v struct {
		Harnesses []struct {
			Name string   `json:"name"`
			Chat []string `json:"chat"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ не разобрался (%v): %s", err, text)
	}
	if len(v.Harnesses) != 2 {
		t.Fatalf("подписок %d, жду две: %s", len(v.Harnesses), text)
	}
	if got := strings.Join(v.Harnesses[0].Chat, ","); got != "модель-чата" {
		t.Fatalf("список чата первой подписки %q", got)
	}
	if got := strings.Join(v.Harnesses[1].Chat, ","); got != "вторая-base,вторая-чата" {
		t.Fatalf("список чата второй подписки %q", got)
	}
}
