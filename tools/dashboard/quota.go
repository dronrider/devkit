package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Остаток подписок: GET /api/quota отдаёт снимки квоты из каталога
// ~/.devkit/quota. Подписка тут это файл каталога, имя харнеса берётся из
// имени файла, а разбор один на всех: ни одного имени харнеса в коде нет, и
// подключённая завтра подписка появляется на экране сама, стоит её снимку лечь
// в каталог.
//
// Читается каталог с диска, а не подпроцессом agentctl, и граница LLD DK-112
// проведена ровно тут: через подпроцесс дашборд ходит за состоянием, у
// которого есть утилита с инвариантами записи (доска и taskctl), а журналы и
// снимки чужих процессов читает сам. Снимок пишет agentctl quota refresh,
// дашборд его только показывает, и ломать тут нечего. Своей команды чтения,
// пригодной дашборду, у agentctl всё равно нет: `agentctl quota` печатает
// человеческий текст, берёт один активный харнес из своего окружения и
// отсеивает бакеты по профилю этого харнеса, так что каталог целиком через
// него не прочитать, а разбирать печатный вывод хрупко. Формат «key = value»
// дашборд уже разбирает в своём конфиге, и второго разбора тут не заводится.

const (
	// quotaRel это каталог снимков от ~/.devkit. Снимки машинные, а не
	// проектные: лимиты общие на подписку.
	quotaRel = "quota"
	// Снимок это файл <харнес>.local, как его кладёт agentctl.
	quotaSuffix = ".local"
	// Момент снятия и даты сброса пишутся местным временем без секунд.
	quotaTimeLayout = "2006-01-02T15:04"
	// quotaMaxAge это порог, за которым снимок протух. Величина взята из
	// agentctl (snapshotMaxAge): по такому снимку он уже не двигает вердикт
	// вверх, и показывать остаток свежим, пока сам выбор моделей ему не верит,
	// значило бы врать разными словами в двух местах.
	quotaMaxAge = 45 * time.Minute
)

// QuotaBucket это строка снимка: сколько процентов бакета потрачено на момент
// снятия и когда он сбрасывается.
type QuotaBucket struct {
	Name  string `json:"name"`
	Used  int    `json:"used_pct"`
	Reset string `json:"reset,omitempty"`
}

// QuotaHarness это одна подписка. Age идёт словами, а не секундами: возраст
// показывается и в боковой колонке, и карточкой на телефоне, и складывать его
// в двух местах клиента незачем. Stale отделено от Note, чтобы протухший
// снимок был подписан честно и без разбора текста.
type QuotaHarness struct {
	Name  string `json:"name"`
	Taken string `json:"taken,omitempty"`
	Age   string `json:"age,omitempty"`
	Stale bool   `json:"stale"`
	// AgeSec это возраст снимка секундами: по нему экран красит время снимка
	// градацией, а слова «протух» человеку ничего не говорили (замечание 21).
	AgeSec  int64         `json:"age_sec,omitempty"`
	Note    string        `json:"note,omitempty"`
	Buckets []QuotaBucket `json:"buckets"`
	Warns   []string      `json:"warns,omitempty"`
}

// QuotaView это ответ ручки. Пустота называется словами полем Note: каталога
// нет и каталог без снимков это разные случаи, и молчащий блок не отличался бы
// от отработавшего.
type QuotaView struct {
	Dir       string         `json:"dir"`
	Note      string         `json:"note,omitempty"`
	Harnesses []QuotaHarness `json:"harnesses"`
	// Fail это отказ последнего обновления. Пусто, пока обновление проходит:
	// молчание тут и означает, что снимок стар не потому, что его перестали
	// снимать.
	Fail *QuotaFail `json:"fail,omitempty"`
}

// QuotaFail это причина, по которой снимок перестал обновляться. Прежде она
// оседала только в журнале демона и повторялась там каждые десять минут, а
// человек оставался с трёхчасовым снимком на экране и без единого слова о том,
// почему он такой (живой случай пользователя).
type QuotaFail struct {
	Reason string `json:"reason"`
	Dir    string `json:"dir,omitempty"`
	Age    string `json:"age,omitempty"`
}

func quotaDir(home string) string {
	return filepath.Join(home, ".devkit", quotaRel)
}

// readQuota собирает снимки каталога. Битый файл не уносит соседей: своё он
// теряет предупреждениями, а подписка остаётся на экране.
func readQuota(home string, now time.Time) QuotaView {
	dir := quotaDir(home)
	view := QuotaView{Dir: dir, Harnesses: []QuotaHarness{}}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		view.Note = "каталога " + dir + " нет: снимки кладёт agentctl quota refresh"
		return view
	}
	if err != nil {
		view.Note = fmt.Sprintf("каталог %s не читается: %v", dir, err)
		return view
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), quotaSuffix) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), quotaSuffix)
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			view.Harnesses = append(view.Harnesses, QuotaHarness{Name: name,
				Buckets: []QuotaBucket{}, Note: "снимок не прочитался: " + err.Error()})
			continue
		}
		view.Harnesses = append(view.Harnesses, parseQuotaSnapshot(name, string(data), now))
	}
	sort.Slice(view.Harnesses, func(i, j int) bool { return view.Harnesses[i].Name < view.Harnesses[j].Name })
	if len(view.Harnesses) == 0 {
		view.Note = "в " + dir + " нет ни одного снимка: снять командой agentctl quota refresh"
	}
	return view
}

// parseQuotaSnapshot разбирает текст снимка: строки «key = value», «#» это
// комментарий. Ключ taken это момент снятия, любой другой ключ это бакет, и
// список бакетов держит снимок, а не код: у каждой подписки лимиты свои.
func parseQuotaSnapshot(name, text string, now time.Time) QuotaHarness {
	h := QuotaHarness{Name: name, Buckets: []QuotaBucket{}}
	var taken time.Time
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		key, val, ok := strings.Cut(ln, "=")
		if !ok {
			h.Warns = append(h.Warns, fmt.Sprintf("строка %q не разобрана", ln))
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if key == "taken" {
			t, err := parseQuotaTime(val)
			if err != nil {
				h.Warns = append(h.Warns, fmt.Sprintf("момент снятия %q не разобран", val))
				continue
			}
			taken = t
			continue
		}
		b, err := parseQuotaBucket(key, val)
		if err != nil {
			h.Warns = append(h.Warns, fmt.Sprintf("бакет %s не разобран: %v", key, err))
			continue
		}
		h.Buckets = append(h.Buckets, b)
	}
	if !taken.IsZero() {
		h.Taken = taken.Format(quotaTimeLayout)
	}
	h.Age, h.Stale, h.Note = quotaAge(taken, now)
	if !taken.IsZero() && now.After(taken) {
		h.AgeSec = int64(now.Sub(taken).Seconds())
	}
	if len(h.Buckets) == 0 && h.Note == "" {
		h.Note = "бакетов в снимке нет"
	}
	return h
}

// quotaAge говорит про возраст снимка. Случаи, где возрасту верить нельзя,
// называются отдельно: без них «снимок свежий» читалось бы одинаково и там,
// где момент снятия не пришёл вовсе, и там, где часы машины разошлись.
func quotaAge(taken, now time.Time) (age string, stale bool, note string) {
	switch d := now.Sub(taken); {
	case taken.IsZero():
		return "", true, "момента снятия в снимке нет, возраст неизвестен"
	case d < 0:
		return "", true, "снимок из будущего, часы разошлись"
	case d > quotaMaxAge:
		return humanAge(d), true, ""
	default:
		return humanAge(d), false, ""
	}
}

// parseQuotaBucket разбирает значение вида «34% сброс 2026-08-04T10:00».
func parseQuotaBucket(name, val string) (QuotaBucket, error) {
	pct, rest, ok := strings.Cut(val, "%")
	if !ok {
		return QuotaBucket{}, fmt.Errorf("жду процент потраченного, вижу %q", val)
	}
	n, err := strconv.Atoi(strings.TrimSpace(pct))
	if err != nil || n < 0 || n > 100 {
		return QuotaBucket{}, fmt.Errorf("процент %q не в диапазоне 0-100", strings.TrimSpace(pct))
	}
	b := QuotaBucket{Name: name, Used: n}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "сброс"))
	if rest == "" {
		return b, nil
	}
	reset, err := parseQuotaTime(rest)
	if err != nil {
		return QuotaBucket{}, fmt.Errorf("дата сброса %q не разобрана", rest)
	}
	b.Reset = reset.Format(quotaTimeLayout)
	return b, nil
}

func parseQuotaTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{quotaTimeLayout, "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("не время: %q", s)
}

// humanAge это возраст словами, теми же, какими его печатает agentctl.
func humanAge(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h >= 24:
		return fmt.Sprintf("%dд %dч", h/24, h%24)
	case h > 0:
		return fmt.Sprintf("%dч %dм", h, m)
	default:
		return fmt.Sprintf("%dм", m)
	}
}

// handleQuota отдаёт снимки. Ручка своя, а не поле ответа /api/projects:
// подписка машинная, к проектам отношения не имеет, и блок квоты стоит над
// любым экраном, а не только над списком проектов. Памяти процесса тут нет:
// это чтение каталога с парой файлов по сотне байт, оно на три порядка дешевле
// подпроцессов, ради которых заведён кэш DK-242, а лишний слой стоил бы
// показанного остатка, который на минуту отстаёт от снятого.
func (s *server) handleQuota(w http.ResponseWriter, r *http.Request) {
	view := readQuota(s.cfg.Home, s.now())
	view.Fail = s.quotaFail()
	writeJSON(w, http.StatusOK, view)
}

// Свежесть снимка квоты держит сам демон (замечание 21 двенадцатого круга
// POC). Прежде снимок обновлял только хук старта сессии и рука, а переписка в
// уже живых чатах его не трогала, отчего на экране почти всегда стояло «снимок
// час назад». Тик редкий и служебный: agentctl сам умеет и замок, и атомарную
// запись, поверх них тут ничего не строится.

// quotaTick это шаг обновления снимка. Реже порога свежести, но не настолько,
// чтобы экран успел покраснеть между тиками.
const quotaTick = 10 * time.Minute

// quotaRefreshDir это каталог, из которого зовётся обновление снимка. Пустой
// каталог означал бы рабочий каталог самого демона, а под launchd он к дереву
// пользователя отношения не имеет: клиент харнеса такому каталогу не доверяет и
// вместо панели остатка показывает вопрос про доверие, за которым refresh стоит
// до срока. Снимок при этом стареет часами, а в журнал каждые десять минут
// капает одна и та же ошибка (живой случай пользователя).
//
// Берётся настоящий дом, тот же, что у прочих служебных вызовов клиента
// (homeEnv, chatVars): из него человек клиента и запускает, поэтому доверие
// каталогу у клиента уже есть, а от того, откуда поднят демон, дом не зависит
// вовсе. Дерево проекта тут не годится, хоть и доверено: транскрипт служебного
// вызова лёг бы в чаты этого проекта и всплыл бы в списке разговоров.
func quotaRefreshDir() string {
	return realHome()
}

// quotaRefreshRun это шов вызова обновления: в работе подпроцесс agentctl, а
// тест смотрит, из какого каталога зовут, не поднимая клиента вовсе.
var quotaRefreshRun = func(dir, bin string) error {
	_, err := runProcQuiet(dir, true, bin, "quota", "refresh")
	return err
}

// quotaFailMin это длина фразы, которой хватает на причину: разрез идёт по
// двоеточию, а первым куском у подпроцесса стоит слово вроде «ошибка», и одним
// им причина не названа.
const quotaFailMin = 24

// quotaFailMax это потолок причины в плашке: блок стоит в колонке шириной с
// ладонь, и совет с командами человек читает в журнале, а не там.
const quotaFailMax = 140

// quotaFailWords сжимает причину отказа до первой фразы. Разрез по двоеточию
// нарочно: так причину пишут и agentctl, и подпроцессы девкита, а хвост за ним
// это совет, что делать руками.
func quotaFailWords(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	out := ""
	for _, part := range strings.Split(text, ": ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Голая подпись подпроцесса это не причина: с неё начинается всякий его
		// отказ, и в плашке она заняла бы место того, что стоит прочитать.
		if out == "" && quotaFailLabel(part) {
			continue
		}
		if out == "" {
			out = part
		} else {
			out += ": " + part
		}
		if len([]rune(out)) >= quotaFailMin {
			break
		}
	}
	if r := []rune(out); len(r) > quotaFailMax {
		out = strings.TrimSpace(string(r[:quotaFailMax])) + "..."
	}
	return out
}

// quotaFailLabel узнаёт подпись отказа, стоящую перед самой причиной.
func quotaFailLabel(part string) bool {
	switch strings.ToLower(part) {
	case "ошибка", "ошибки", "error", "err":
		return true
	}
	return false
}

// quotaFail собирает отказ для ответа ручки. Каталог вызова тут же: у отказа
// про доверие он и есть половина ответа на вопрос «почему».
func (s *server) quotaFail() *QuotaFail {
	s.mu.Lock()
	reason, at := s.quotaErr, s.quotaErrAt
	s.mu.Unlock()
	if reason == "" {
		return nil
	}
	fail := &QuotaFail{Reason: reason, Dir: quotaRefreshDir()}
	if !at.IsZero() {
		fail.Age = humanAge(s.now().Sub(at))
	}
	return fail
}

// quotaOutcome помнит исход обновления и отвечает, сменился ли он. По этому
// ответу пишется журнал: одинаковая строка раз в десять минут топит его, ничего
// не добавляя, а смена исхода это как раз то, что стоит прочитать.
func (s *server) quotaOutcome(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := !s.quotaSaid || s.quotaErr != reason
	s.quotaSaid = true
	s.quotaErr = reason
	if reason == "" {
		s.quotaErrAt = time.Time{}
	} else {
		s.quotaErrAt = s.now()
	}
	return fresh
}

// quotaRefresh снимает снимок и помнит исход: причина отказа уезжает в плашку
// квоты, а в журнал идёт сменой исхода.
func (s *server) quotaRefresh(bin string) {
	dir := quotaRefreshDir()
	if err := quotaRefreshRun(dir, bin); err != nil {
		full := procErr(err)
		if s.quotaOutcome(quotaFailWords(full)) {
			s.logf("снимок квоты не обновился (каталог %q): %s", dir, full)
		}
		return
	}
	if s.quotaOutcome("") {
		s.logf("снимок квоты обновляется тиком демона (каталог %q)", dir)
	}
}

// quotaKeeper обновляет снимок квоты по кругу, пока жив демон. Вызов служебный:
// хуки devkit на нём молчат, и лента уведомлений от него не наполняется.
func (s *server) quotaKeeper(stop <-chan struct{}) {
	bin := binPath(agentctlBin)
	if bin == "" {
		s.logf("снимок квоты обновлять нечем: agentctl не нашёлся")
		return
	}
	// Первый снимок снимается сразу: демон перезапускается каждой пересборкой,
	// и ждать десять минут значило бы держать на экране снимок, который к этому
	// времени уже протух.
	s.quotaRefresh(bin)
	t := time.NewTicker(quotaTick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.quotaRefresh(bin)
		}
	}
}
