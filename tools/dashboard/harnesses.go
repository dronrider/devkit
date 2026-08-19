package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Подписки машины: GET /api/harnesses отдаёт список, из которого человек
// выбирает, на чьей квоте поднять работу (LLD DK-328, решение 3). Ручка
// машинная, а не проектная: харнесы принадлежат машине, как и снимки квоты.
//
// Список берётся подпроцессом `agentctl harness --json`, а не чтением
// ~/.devkit/harness.local: слоёв конфигурации три (профиль, машинный,
// проектный), и проектное сужение при прямом чтении потерялось бы, а дашборд
// начал бы вычислять то, что уже вычисляет agentctl. Человеческий вывод той же
// команды разбирать нельзя, он живой текст и меняется от правки формулировки, и
// ровно для этого у неё заведён машинный вид.
//
// Имён харнесов в этом файле нет ни одного, как и во всём коде дашборда
// (рубеж TestQuotaNoHarnessNamesInCode): что показывать, целиком решает ответ
// утилиты, и третья подписка появится на экране сама.

// agentctlBin подменяется тестами.
var agentctlBin = "agentctl"

// Harness это одна подписка в ответе ручки. Поля повторяют машинный вид
// agentctl: значений окружения тут нет никогда, только имена, и дальше клиента
// они не едут вовсе.
type Harness struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Bin     string `json:"bin,omitempty"`
	// Home это каталог хозяйства подписки. По нему ищутся транскрипты её
	// разговоров: клиент второй подписки поднимается со своим каталогом
	// конфигурации, и журнал он пишет туда, а не в ~/.claude (DK-362).
	Home string   `json:"home,omitempty"`
	Env  []string `json:"env,omitempty"`
	// Models это лестница ярусов подписки моделями: из неё панель разговора
	// собирает выбор модели. Имён тут дашборд не сочиняет, всё приезжает
	// ответом agentctl.
	Models []HarnessModel `json:"models,omitempty"`
}

// HarnessModel это ступень лестницы: ярус и модель, в которую он развёрнут.
type HarnessModel struct {
	Tier  string `json:"tier"`
	Model string `json:"model"`
}

// HarnessView это ответ ручки. Пустой список это не поломка запуска: работа
// поднимется на подписке по умолчанию, как до этой задачи, а причина
// называется полем Note, чтобы отсутствие выбора не выглядело молчанием.
type HarnessView struct {
	Harnesses []Harness `json:"harnesses"`
	Note      string    `json:"note,omitempty"`
	Warns     []string  `json:"warns,omitempty"`
}

// agentctlHarnesses это машинный вид agentctl: разбирается ровно он, поле в
// поле.
type agentctlHarnesses struct {
	Harnesses []struct {
		Name    string   `json:"name"`
		Enabled bool     `json:"enabled"`
		Default bool     `json:"default"`
		Bin     string   `json:"bin"`
		Home    string   `json:"home"`
		Env     []string `json:"env"`
		Models  []struct {
			Tier  string `json:"tier"`
			Model string `json:"model"`
		} `json:"models"`
	} `json:"harnesses"`
	Note  string   `json:"note"`
	Warns []string `json:"warns"`
}

const agentctlMissingNote = "agentctl не нашёлся ни рядом с бинарём дашборда, ни в PATH: " +
	"список подписок читать нечем, запуск идёт на подписке по умолчанию; поставить бинари: devkitctl update"

// readHarnesses спрашивает раскладку подписок у agentctl. Отказ утилиты это
// причина в Note, а не ошибка ручки: выбор подписки штука необязательная, и
// уронить из-за неё кнопку запуска было бы дороже, чем остаться без выбора.
func readHarnesses() HarnessView {
	view := HarnessView{Harnesses: []Harness{}}
	bin := binPath(agentctlBin)
	if bin == "" {
		view.Note = agentctlMissingNote
		return view
	}
	out, err := runProc(bin, "harness", "--json")
	if err != nil {
		view.Note = fmt.Sprintf("agentctl harness --json не ответил (%s): запуск идёт на подписке по умолчанию", procErr(err))
		return view
	}
	var raw agentctlHarnesses
	if err := json.Unmarshal(out, &raw); err != nil {
		view.Note = fmt.Sprintf("ответ agentctl harness --json не разобрался: %v", err)
		return view
	}
	view.Warns = raw.Warns
	for _, h := range raw.Harnesses {
		// Показываются только включённые: невключённому devkit не раскладывает
		// правила, хуки и скиллы, и поднимать на нём работу нечестно. Харнес без
		// клиента пропускается по той же причине: поднимать его нечем, и строка
		// списка обещала бы запуск, которого не будет.
		if !h.Enabled || h.Bin == "" {
			continue
		}
		hh := Harness{Name: h.Name, Default: h.Default, Bin: h.Bin, Home: h.Home, Env: h.Env}
		for _, m := range h.Models {
			if m.Model != "" {
				hh.Models = append(hh.Models, HarnessModel{Tier: m.Tier, Model: m.Model})
			}
		}
		view.Harnesses = append(view.Harnesses, hh)
	}
	if len(view.Harnesses) == 0 {
		view.Note = raw.Note
		if view.Note == "" {
			view.Note = "включённых подписок с клиентом agentctl не назвал: запуск идёт на подписке по умолчанию"
		}
	}
	return view
}

// pick находит подписку по имени. Второе возвращаемое значение это причина
// отказа словами: имя, которого нет в списке, это устаревший экран или опечатка
// в чужом запросе, и молча уехать на подписку по умолчанию тут нельзя.
func (v HarnessView) pick(name string) (*Harness, string) {
	for i := range v.Harnesses {
		if v.Harnesses[i].Name == name {
			return &v.Harnesses[i], ""
		}
	}
	var known []string
	for _, h := range v.Harnesses {
		known = append(known, h.Name)
	}
	if len(known) == 0 {
		return nil, fmt.Sprintf("подписки %s на машине нет: %s", name, v.Note)
	}
	return nil, fmt.Sprintf("подписки %s на машине нет, включены: %s", name, strings.Join(known, ", "))
}

// harnessTTL это срок памяти процесса на раскладку подписок. Спрашивается она
// на каждой сборке экрана и на каждом запуске, а меняется правкой конфига
// руками, поэтому подпроцесс за ней ходит не чаще раза в полминуты.
const harnessTTL = 30 * time.Second

// harnesses отдаёт раскладку, свежую или из памяти. Ответ с причиной вместо
// списка не запоминается вовсе: «agentctl не нашёлся» это причина, а не ответ,
// и поставленный бинарь обязан доезжать до экрана сразу, а не по выходе срока
// (то же правило, что у доски в cache.go).
func (s *server) harnesses() HarnessView {
	now := s.now()
	s.mu.Lock()
	v, born := s.harn, s.harnBorn
	s.mu.Unlock()
	if v != nil && now.Sub(born) < harnessTTL {
		return *v
	}
	got := readHarnesses()
	if len(got.Harnesses) > 0 {
		s.mu.Lock()
		s.harn, s.harnBorn = &got, now
		s.mu.Unlock()
	}
	return got
}

func (s *server) handleHarnesses(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.harnesses())
}
