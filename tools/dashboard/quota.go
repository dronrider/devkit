package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/quotaconf"
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
)

// Порог, за которым снимок протух, у дашборда общий с agentctl: пакет
// internal/quotaconf, строка `stale = <минуты>` в ~/.devkit/quota.local,
// умолчание 45 минут. По протухшему снимку agentctl не двигает вердикт вверх,
// и показывать остаток свежим, пока сам выбор моделей ему не верит, значило
// бы врать разными словами в двух местах.

// QuotaBucket это строка снимка: сколько процентов бакета потрачено на момент
// снятия и когда он сбрасывается.
type QuotaBucket struct {
	Name  string `json:"name"`
	Used  int    `json:"used_pct"`
	Reset string `json:"reset,omitempty"`
	// Expired значит, что время сброса бакета уже прошло: окно сбросилось, а
	// цифра в снимке со времени до сброса и живой больше не является. Такой
	// процент экран обязан подписывать, а не рисовать наравне со свежим
	// (замечание 2 приёмки DK-633: занятая у кеша разбивка пережила сброс
	// недели и читалась как живые 37%).
	Expired bool `json:"expired,omitempty"`
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
	// Reason это то, что стоит на экране: несколько слов о том, что снимок не
	// обновился. Причину сюда не кладут. Панель квоты шириной с ладонь, и текст
	// отказа пишет не она, а тот, кто отказал: agentctl объясняет человеку в
	// терминале целым абзацем, и абзац этот в строку панели не лезет (замечание
	// пользователя, второй такой случай за день).
	Reason string `json:"reason"`
	// Detail это причина словами того, кто отказал. Живёт за нажатием «почему»,
	// на экране сразу не разворачивается.
	Detail string `json:"detail,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Age    string `json:"age,omitempty"`
}

// quotaFailSaid это строка отказа для экрана. Она одна на все причины: человеку
// с одного взгляда нужно знать, что цифры не поехали, а чем именно упёрлось
// обновление, он спрашивает нажатием.
const quotaFailSaid = "снимок не обновился"

func quotaDir(home string) string {
	return filepath.Join(home, ".devkit", quotaRel)
}

// readQuota собирает снимки каталога. Битый файл не уносит соседей: своё он
// теряет предупреждениями, а подписка остаётся на экране.
func readQuota(home string, now time.Time) (view QuotaView) {
	dir := quotaDir(home)
	view = QuotaView{Dir: dir, Harnesses: []QuotaHarness{}}
	// Порог свежести читается на каждый запрос: файл настройки меняют рукой, и
	// перезапускать демон ради него незачем. Кривое значение демону валить
	// нечего, запрос к ручке не команда, поэтому отказ здесь это причина на
	// экране при действующем умолчании, а не молчаливый съезд на 45 минут.
	maxAge, confErr := quotaconf.StaleAge(home)
	if confErr != nil {
		maxAge = quotaconf.Default
		note := fmt.Sprintf("%v; действует умолчание %s", confErr, humanAge(quotaconf.Default))
		defer func() {
			if view.Note != "" {
				view.Note = note + "; " + view.Note
				return
			}
			view.Note = note
		}()
	}
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
		view.Harnesses = append(view.Harnesses, parseQuotaSnapshot(name, string(data), now, maxAge))
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
func parseQuotaSnapshot(name, text string, now time.Time, maxAge time.Duration) QuotaHarness {
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
		if b.Reset != "" {
			if reset, err := parseQuotaTime(b.Reset); err == nil && reset.Before(now) {
				b.Expired = true
			}
		}
		h.Buckets = append(h.Buckets, b)
	}
	if !taken.IsZero() {
		h.Taken = taken.Format(quotaTimeLayout)
	}
	h.Age, h.Stale, h.Note = quotaAge(taken, now, maxAge)
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
func quotaAge(taken, now time.Time, maxAge time.Duration) (age string, stale bool, note string) {
	switch d := now.Sub(taken); {
	case taken.IsZero():
		return "", true, "момента снятия в снимке нет, возраст неизвестен"
	case d < 0:
		return "", true, "снимок из будущего, часы разошлись"
	case d > maxAge:
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

// Разницы в возрасте снимков дашборд словами не называет: давность подписана
// у каждой подписки цифрой, и сравнение человек делает сам. Приписка «раньше
// остальных» у старшего снимка стояла лишним словом и путала (решение
// пользователя).

// Свежесть снимка квоты держит сам демон (замечание 21 двенадцатого круга
// POC). Прежде снимок обновлял только хук старта сессии и рука, а переписка в
// уже живых чатах его не трогала, отчего на экране почти всегда стояло «снимок
// час назад». Тик редкий и служебный: agentctl сам умеет и замок, и атомарную
// запись, поверх них тут ничего не строится.

// quotaTick это шаг пробуждения, а не съёма: когда снимать, решает порог
// свежести внутри agentctl (--if-stale), тику остаётся будить его достаточно
// часто, чтобы протухший снимок не стоял до следующего часа.
const quotaTick = 10 * time.Minute

// Доверие каталогу клиент харнеса помнит у себя в конфиге, и спрашивается оно
// оттуда, а не угадывается. Дом пользователя на роль доверенного каталога не
// годится: он у клиента в списке стоит, а подтверждения доверия у него нет
// (живой случай пользователя, где refresh упирался в вопрос именно из дома).

// clientTrustFile это конфиг клиента с подтверждениями доверия каталогам.
func clientTrustFile(home string) string {
	return filepath.Join(home, ".claude.json")
}

// clientTrusted читает каталоги, доверие которым человек клиенту уже
// подтвердил. Файла нет или формат разошёлся, значит доверенных каталогов
// стенду неизвестно: вызов пойдёт откатом, а причина отказа доедет до плашки.
func clientTrusted(home string) map[string]bool {
	out := map[string]bool{}
	if home == "" {
		return out
	}
	data, err := os.ReadFile(clientTrustFile(home))
	if err != nil {
		return out
	}
	var conf struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		return out
	}
	for dir, p := range conf.Projects {
		if p.Trusted {
			out[dir] = true
		}
	}
	return out
}

// quotaTrust это шов чтения доверия: тест подставляет свой список и не зависит
// от конфига машины, на которой его гоняют.
var quotaTrust = clientTrusted

// quotaRefreshDir это каталог, из которого зовётся обновление снимка. Пустой
// каталог означал бы рабочий каталог самого демона, а под launchd он к дереву
// пользователя отношения не имеет: клиент такому каталогу не доверяет и вместо
// панели остатка показывает вопрос про доверие, за которым refresh стоит до
// срока. Снимок при этом стареет часами, а в журнал каждые десять минут капает
// одна и та же ошибка (живой случай пользователя).
//
// Берётся дерево проекта, доверие которому клиент уже помнит: в дереве человек
// клиента и запускает, поэтому вопроса про доверие там не будет, а от того,
// откуда поднят сам демон, выбор не зависит вовсе. Служебным вызовом транскрипт
// в проекте не заводится, разговоры проекта такой refresh не засоряет
// (проверено живым прогоном). Не нашлось ни одного доверенного дерева, значит
// вызов идёт из дома: он хотя бы принадлежит человеку, а отказ теперь виден
// причиной в плашке квоты.
func (s *server) quotaRefreshDir() string {
	home := realHome()
	trusted := quotaTrust(home)
	projects, _ := s.projects()
	for _, p := range projects {
		if trusted[p.Path] {
			return p.Path
		}
	}
	return home
}

// quotaRefreshRun это шов вызова обновления: в работе подпроцесс agentctl, а
// тест смотрит, из какого каталога и с какими флагами зовут, не поднимая
// клиента вовсе. Флаг --all обходит обе подписки (DK-633): без него тик
// освежал только активный харнес, и снимок второй подписки стоял часами при
// живом демоне. Флаг --if-stale отдаёт порог свежести agentctl: панель /usage
// и кабинет z.ai дёргаются по протуханию снимка, а не каждые десять минут.
var quotaRefreshRun = func(dir, bin string) error {
	_, err := runProcQuiet(dir, true, bin, "quota", "refresh", "--all", "--if-stale")
	return err
}

// quotaFailMin это длина фразы, которой хватает на причину: разрез идёт по
// двоеточию, а первым куском у подпроцесса стоит слово вроде «ошибка», и одним
// им причина не названа.
const quotaFailMin = 24

// quotaFailWhy это длина, на которой разбор причины по фразам останавливается.
// Одной фразы отказу мало: первая называет, обо что упёрлись, а вторая часто
// говорит, чем это кончится и что снимок цел.
const quotaFailWhy = 120

// quotaFailMax это потолок причины: она лежит за нажатием и переносится по
// строкам, поэтому места тут больше, чем было у строки плашки, но абзац с
// путями и советами всё равно остаётся журналу.
const quotaFailMax = 220

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
	out = quotaFailSentence(out)
	if r := []rune(out); len(r) > quotaFailMax {
		cut := strings.TrimSpace(string(r[:quotaFailMax]))
		// Обрыв идёт по концу фразы, когда он есть во второй половине куска:
		// оборванное на полуслове предложение читается хуже, чем целое.
		if i := strings.LastIndex(cut, ". "); i > quotaFailMax/2 {
			return strings.TrimSpace(cut[:i+1])
		}
		out = cut + "..."
	}
	return out
}

// quotaFailSentence обрезает причину по концу фразы. Хвост за первой фразой у
// отказов devkit это путь к кадру, совет и напоминание, что снимок не тронут:
// человеку у панели они не нужны, а место занимают все.
func quotaFailSentence(text string) string {
	out := ""
	for _, part := range strings.Split(text, ". ") {
		if out == "" {
			out = part
		} else {
			out += ". " + part
		}
		if len([]rune(out)) >= quotaFailWhy {
			break
		}
	}
	return strings.TrimSpace(out)
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
	fail := &QuotaFail{Reason: quotaFailSaid, Detail: reason, Dir: s.quotaRefreshDir()}
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
	dir := s.quotaRefreshDir()
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
