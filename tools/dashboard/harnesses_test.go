package main

import (
	"encoding/json"
	"net/http"
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

// Порог ротации исполнителя-субагента приезжает из машинного конфига через
// agentctl harness --json и виден в ответе ручки; без ключа и без agentctl
// стоит умолчание, нуля снаружи не бывает (DK-397).
func TestHarnessesExecRotateTokens(t *testing.T) {
	t.Run("ключ задан", func(t *testing.T) {
		e := newTestEnv(t)
		writeAgentctlFake(t, e.bin, `{"source": "фикстура", "exec_rotate_tokens": 640000,
  "harnesses": [{"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1"}]}`)
		if v := getHarnesses(t, e, e.loggedClient(t)); v.ExecRotateTokens != 640000 {
			t.Fatalf("порог %d, жду 640000 из конфига", v.ExecRotateTokens)
		}
	})
	t.Run("ключа нет", func(t *testing.T) {
		e := newTestEnv(t)
		writeAgentctlFake(t, e.bin, harnessJSONFixture)
		if v := getHarnesses(t, e, e.loggedClient(t)); v.ExecRotateTokens != execRotateDefault {
			t.Fatalf("порог %d, жду умолчание %d", v.ExecRotateTokens, execRotateDefault)
		}
	})
	t.Run("agentctl не нашёлся", func(t *testing.T) {
		e := newTestEnv(t)
		if v := getHarnesses(t, e, e.loggedClient(t)); v.ExecRotateTokens != execRotateDefault {
			t.Fatalf("порог %d, жду умолчание %d", v.ExecRotateTokens, execRotateDefault)
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
