package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

type verdict struct {
	Tier   string // ступень лестницы моделей: mini, base, pro, max
	Model  string // ярус, развёрнутый маппингом активного харнеса; «-», если разворачивать нечем
	Via    string // харнес назначения ступени, чьей подпиской платится работа; «-» вместе с моделью
	Effort string // reasoning effort субагента: low, medium, high, xhigh
	Reason string
	Groom  bool // вердикт «исполнять рано»: сначала грумминг или разбивка
}

// pickTier выводит ярус из метаданных строки доски. Порядок правил значим:
// грумминг и разбивка перебивают дешевизну, LLD сильнее всего. Считается всё в
// ярусах и ни на какой инструмент не опирается: в модель ярус разворачивается
// последним шагом, маппингом активного харнеса.
func pickTier(r row) verdict {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD") && (r.Cost == "L" || r.Cost == "XL"):
		return verdict{Tier: tierMax, Reason: "LLD ценой L/XL: сложное проектирование, max делает его лучше pro"}
	case strings.EqualFold(r.Type, "LLD"):
		return verdict{Tier: tierPro, Reason: "LLD: дизайн отдаётся сильной модели"}
	case unc >= 4:
		return verdict{Tier: tierPro, Reason: fmt.Sprintf("неопределённость %d: сначала грумминг, исполнять рано", unc), Groom: true}
	case r.Cost == "XL":
		return verdict{Tier: tierPro, Reason: "цена XL: сначала разбить на серию, целиком не отдавать", Groom: true}
	case r.Cost == "S" && unc == 0:
		return verdict{Tier: tierMini, Reason: "совсем атомарная правка с очевидным подходом, дешёвой модели хватает"}
	case r.Cost == "S" && unc == 1:
		return verdict{Tier: tierBase, Reason: "небольшая задача, подход уже выбран, дешёвая модель справится"}
	case strings.EqualFold(r.Type, "task") && r.Cost == "M" && (unc == 1 || unc == 2):
		return verdict{Tier: tierBase, Reason: "полоса задач ценой M с выбранным подходом, дешёвая модель справится, просадку ловят счётчики слияний"}
	case r.Cost == "" || r.Cost == "-":
		return verdict{Tier: tierPro, Reason: "цена не оценена, до оценки модель по умолчанию, не забыть оценить"}
	default:
		return verdict{Tier: tierPro, Reason: "задача для дешёвых моделей не подходит, по умолчанию идёт сильной"}
	}
}

// Роли, под которые считается вердикт. Исполнитель пишет код, ревьювер читает
// готовый дифф, и калибр им нужен разный.
const (
	roleExec   = "exec"
	roleReview = "review"
)

var validRoles = map[string]bool{roleExec: true, roleReview: true}

// reviewShift переводит исполнительский вердикт в ревьюверский. Ревьюверу
// нужен не калибр автора, а внимательность на готовом диффе, поэтому вердикт
// опускается на ярус, но не ниже base: mini дифф читает бегло и замечаний не
// находит, а base это дешёвая рабочая ступень. Два случая спуска не знают.
// Дизайн (тип LLD) читается тем же калибром, каким пишется, спуск тут не
// экономия, а потеря. Грумминговый вердикт значит, что работы ещё не было, и
// ревьюить нечего. Effort роль не трогает: глубина размышления идёт за
// неопределённостью задачи, а она от роли не меняется.
func reviewShift(v *verdict, r row) {
	switch {
	case v.Groom:
		v.Reason += "; роль ревью: вердикт грумминговый, работа по нему не начиналась, ревьюить пока нечего"
		return
	case strings.EqualFold(r.Type, "LLD"):
		v.Reason += "; роль ревью: дизайн читается тем же калибром, каким пишется, спуска нет"
		return
	}
	i := tierIndex(v.Tier)
	switch {
	case i < 0:
		return
	case v.Tier == tierBase:
		v.Reason += "; роль ревью: base это пол ревьювера, ниже не опускаем"
	case i == 0:
		v.Reason += "; роль ревью: mini -> base, дифф надо читать внимательно, ниже base ревью не опускаем"
		v.Tier = tierBase
	default:
		v.Reason += fmt.Sprintf("; роль ревью: %s -> %s, ревьюверу нужен не калибр автора, а внимательность на диффе", v.Tier, tierNames[i-1])
		v.Tier = tierNames[i-1]
	}
}

// costAtLeastM отделяет цены, на которых сдвиг вниз стоит проговаривать
// отдельно от LLD: M, L и XL по DK-015 достойны opus не меньше дизайна, S
// дешёвая модель тянет и без всякого сдвига.
func costAtLeastM(cost string) bool {
	switch cost {
	case "M", "L", "XL":
		return true
	default:
		return false
	}
}

// floorBaseEffort поднимает effort вердикта яруса base минимум до high. Это
// дешёвая рабочая ступень лестницы, а риск потерять качество на ней есть,
// поэтому экономить на глубине размышления смысла нет: low и medium
// подтягиваются, xhigh и выше не трогаются. Ярус mini пол не касается: модель
// вроде Haiku 4.5 про effort в API вовсе не знает, её low остаётся формальной
// меткой. На харнесе, где base развёрнут в дорогую модель, пол теряет исходное
// обоснование, но вреда не наносит: он только поднимает глубину размышления.
func floorBaseEffort(v *verdict) {
	if v.Tier != tierBase {
		return
	}
	if v.Effort == "low" || v.Effort == "medium" {
		v.Reason += ", effort поднят до high: base дёшев, экономить глубину смысла нет"
		v.Effort = "high"
	}
}

// pickEffort считает уровень размышления по неопределённости из разбивки ранга.
// Тип и цена входят только там, где по ним видно, что решение ещё не найдено
// (LLD, вердикты «исполнять рано») или что метаданным верить рано: цена не
// оценена, значит грумминг не доведён до конца, и нулю в третьем слагаемом
// доверия не больше, чем прочерку. Уровень max маппингом не выдаётся, он
// остаётся ручным решением через override-строку файла задачи.
func pickEffort(r row) string {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD"), unc >= 4, r.Cost == "XL":
		return "xhigh"
	case unc < 0, r.Cost == "", r.Cost == "-":
		return "high"
	case unc == 0:
		return "low"
	case unc <= 2:
		return "medium"
	default:
		return "high"
	}
}

// overrideTiers это допустимые значения строки «Модель:» в файле задачи. Кроме
// имён ярусов принимаются старые имена моделей: строки в уже написанных файлах
// задач ломать об жёсткую ошибку нельзя, а псевдоним переводит их в ярус без
// потери смысла. Конкретную модель инструмента писать нельзя сознательно:
// строка в файле задачи переживает смену харнеса, ярус переносим, имя модели
// нет. Опечатка не должна молча провалиться в обычный маппинг, поэтому любое
// другое значение это ошибка pick, а не игнорируемая строка.
var overrideTiers = map[string]string{
	tierMini: tierMini, tierBase: tierBase, tierPro: tierPro, tierMax: tierMax,
	"haiku": tierMini, "sonnet": tierBase, "opus": tierPro, "fable": tierMax,
}

var validEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// overrides это ручные развилки из файла задачи. Оси независимы: домен может
// требовать другого яруса, другого effort или того и другого сразу, а пустая
// ось берётся из обычного маппинга.
type overrides struct {
	Tier   string
	Effort string
}

// readOverrides ищет в файле задачи строки override (форматы «Модель: pro»
// и «- Эффорт: xhigh», поясняющий хвост в скобках допустим и отбрасывается);
// по каждой оси берётся первая встреченная строка. Нет файла или строк,
// пустой результат без ошибки: работает обычный маппинг pick.
func readOverrides(root, id string) (overrides, error) {
	var ov overrides
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ov, nil
		}
		return ov, err
	}
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		t = strings.TrimPrefix(t, "- ")
		switch {
		case strings.HasPrefix(t, "Модель:") && ov.Tier == "":
			name := overrideValue(t, "Модель:")
			tier, ok := overrideTiers[name]
			if !ok {
				return ov, fmt.Errorf("файл задачи %s: override-строка задаёт неизвестный ярус %q, допустимы mini, base, pro, max и старые имена haiku, sonnet, opus, fable; конкретную модель инструмента в override писать нельзя, она не переносима между харнесами", id, name)
			}
			ov.Tier = tier
		case strings.HasPrefix(t, "Эффорт:") && ov.Effort == "":
			effort := overrideValue(t, "Эффорт:")
			if !validEfforts[effort] {
				return ov, fmt.Errorf("файл задачи %s: override-строка задаёт неизвестный effort %q, допустимы low, medium, high, xhigh, max", id, effort)
			}
			ov.Effort = effort
		}
	}
	return ov, nil
}

func overrideValue(line, prefix string) string {
	v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if i := strings.Index(v, "("); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// goalCap это решение потолка цели: с какого яруса срезали и почему. From
// равен итоговому ярусу, когда потолок ничего не тронул.
type goalCap struct {
	From string
	Note string
}

// goalTierCap читает потолок яруса из раздела «Бюджет» файла цели. Строки
// «ярус» может не быть, тогда потолка нет и вердикт работает как обычно.
func goalTierCap(root, goal string) (string, error) {
	path, err := goalPathOf(root, goal)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b, err := parseGoalBudget(goalSection(string(data), goalBudgetSection), nil)
	if err != nil {
		return "", err
	}
	return b.Tier, nil
}

// capTier режет вердикт потолком цели. Потолок только опускает: пользователь
// задал рамку трат, а не назначил ярус, и подтягивать им дешёвый вердикт вверх
// значило бы тратить бюджет там, где его никто не просил. Грумминговый вердикт
// ниже pro не режется: нарезку цели дешёвая модель сделает плохо, и весь бюджет
// уедет на переделку. Возвращается причина для человеческой строки и записи в
// файл задачи, молчаливой подмены нет.
func capTier(v *verdict, cap string) string {
	ci, i := tierIndex(cap), tierIndex(v.Tier)
	if ci < 0 || i < 0 {
		return ""
	}
	note := "потолок цели: " + cap
	if v.Groom && ci < tierIndex(tierPro) {
		ci = tierIndex(tierPro)
		note += ", грумминговый вердикт ниже pro не режется"
	}
	if i <= ci {
		return note + ", вердикт и так не выше"
	}
	from := v.Tier
	v.Tier = tierNames[ci]
	return fmt.Sprintf("%s, %s -> %s", note, from, v.Tier)
}

// timeNow это часы утилиты, отдельной переменной ради тестов: формула
// корректора и дата записи считаются от одного момента.
var timeNow = time.Now

// pickResult это вердикт со всем, что понадобилось его посчитать: run поднимает
// подпроцесс по тому же вердикту, что печатает pick, и второй раз резолвить
// харнес ему нельзя, иначе делегирование ушло бы не туда, куда сказала строка
// via.
type pickResult struct {
	V    verdict
	HC   harnessContext
	Text string
}

func cmdPick(root, id string, record bool, role, goal string) (string, error) {
	p, err := pickVerdict(root, id, record, role, goal)
	if err != nil {
		return "", err
	}
	return p.Text, nil
}

func pickVerdict(root, id string, record bool, role, goal string) (pickResult, error) {
	var res pickResult
	if !validRoles[role] {
		return res, fmt.Errorf("неизвестная роль %q, допустимы exec и review", role)
	}
	rows, err := loadRows(root)
	if err != nil {
		return res, err
	}
	r := rowOf(rows, id)
	if r == nil {
		return res, fmt.Errorf("задачи %s нет на доске", id)
	}
	ov, err := readOverrides(root, id)
	if err != nil {
		return res, err
	}
	now := timeNow()
	v := pickTier(*r)
	v.Effort = pickEffort(*r)
	var qf quotaFacts
	// Контур харнеса резолвится один раз: из него берётся и объявление квоты
	// (какие бакеты бывают, из каких тратит ярус, где лежит снимок), и маппинг
	// ярусов в модели последним шагом.
	hc := resolveHarnessContext(root, "")
	// Маппинг нужен корректору раньше, чем разворачиванию яруса: пока ступени не
	// считаются по своей подписке, назначение решает, работает корректор вообще.
	tm := hc.Models
	var c correction
	var warns []string
	// Про снимок уже сказано предупреждением: в человеческой строке состояние
	// квоты тогда не повторяется, предупреждение говорит то же самое и вдобавок
	// несёт команду починки.
	warned := false
	if ov.Tier != "" {
		// Ручное решение сильнее автоматики: override корректор не двигает,
		// иначе указанный руками ярус пришлось бы отстаивать повторно на
		// каждый снимок квоты.
		v = verdict{Tier: ov.Tier, Effort: v.Effort, Reason: "модель задана override-строкой файла задачи"}
		qf = quotaFacts{Off: "не смотрели, ярус задан override-строкой"}
	} else if tm.away(v.Tier) {
		// Корректор пока однохарнесный: бакеты он берёт из профиля активного
		// харнеса, а снимок из его файла. Мерить этим дефицит уехавшей ступени
		// значило бы врать, поэтому вердикт на ней идёт без корректора, а причина
		// уходит хвостом. Снимется сторож тогда, когда корректор научится считать
		// ступень по её подписке.
		qf = quotaFacts{Off: fmt.Sprintf("ступень %s уезжает в %s, остаток чужой подписки мерить нечем, вердикт без корректора", v.Tier, tm.via(v.Tier))}
	} else if hc.Quota == nil {
		// Снимать остаток нечем: причина уходит хвостом, потому что вердикт без
		// корректора выглядит совершенно штатным.
		if hc.QuotaNote != "" {
			warns = append(warns, hc.QuotaNote)
		}
		qf = quotaFacts{Off: "объявления нет, корректор выключен", Warned: true}
	} else {
		var s snapshot
		s, err = hc.Quota.read()
		if err != nil {
			warns = append(warns, fmt.Sprintf("снимок квоты не прочитан (%v), вердикт без корректора", err))
			qf = quotaFacts{Off: "снимок не прочитан, корректор выключен", Warned: true}
		} else if w := s.ageWarn(hc.Quota.From, now); w != "" {
			warns = append(warns, w)
			warned = true
		}
		if w := hc.Quota.legacyWarn(); w != "" {
			warns = append(warns, w)
		}
		warns = append(warns, s.Warns...)
		c = correctTier(hc.Quota, v.Tier, v.Groom, s, now)
		// Сдвиг через границу подписок сторожится по той же причине: бакеты
		// итоговой ступени корректор напечатал бы по домашнему профилю, то есть
		// соврал бы во второй раз. Сюда же сдвиг на битое назначение: он менял бы
		// вердикт на прочерк вместо модели. Причина остаётся сказанной, сдвига нет.
		if why := tm.shiftBlock(c.Tier); c.shifted() && why != "" {
			c = correction{Tier: c.From, From: c.From, Note: c.Note, Warn: why}
		}
		v.Tier = c.Tier
		if qf.Off == "" {
			qf = quotaFactsOf(hc.Quota, s, c, v.Groom, now, tm.Active)
			qf.Warned = warned
		}
	}
	// Спуск на роль ревью идёт последним по ярусной оси: сдвигается то, что
	// осталось после override и корректора, иначе корректор увёл бы вердикт
	// ревьювера ещё на ярус ниже пола.
	if role == roleReview {
		reviewShift(&v, *r)
	}
	// Потолок цели режет вердикт последним шагом ярусной оси, после override и
	// корректора: бюджетную рамку пользователь задал на всю цель, и ни ручная
	// строка в файле задачи, ни остаток лимитов её не переопределяют.
	cp := goalCap{From: v.Tier}
	if goal != "" {
		cap, gerr := goalTierCap(root, goal)
		if gerr != nil {
			return res, gerr
		}
		if cap != "" {
			cp.Note = capTier(&v, cap)
		}
	}
	// Пол base применяется здесь, а не сразу после pickEffort, потому что от
	// override и от сдвига корректора зависит, к какому ярусу его применять; а
	// явный override effort должен пол перебить целиком, поэтому если он есть,
	// пол не трогаем.
	if ov.Effort != "" {
		v.Effort = ov.Effort
		v.Reason += ", effort задан override-строкой"
	} else {
		floorBaseEffort(&v)
	}
	// Разворачивание яруса в модель идёт последним шагом: до него весь расчёт
	// про ступени лестницы и ни от какого инструмента не зависит.
	v.Model = tm.model(v.Tier)
	v.Via = tm.via(v.Tier)
	if tm.Note != "" {
		warns = append(warns, tm.Note)
	}
	if w := tm.brokenWarn(v.Tier); w != "" {
		warns = append(warns, w)
	}
	// Сложенные в одну модель соседние ярусы это законный маппинг, и сдвиг по
	// такой паре модель не меняет. Молчать про него нельзя: холостой ход
	// неотличим от отсутствия сдвига.
	// Разворачивать нечем, значит про модель сказать нечего вовсе, и хвост про
	// холостой ход был бы обещанием несуществующего.
	c.SameModel = c.shifted() && tm.model(c.Tier) != unmappedModel && tm.model(c.From) == tm.model(c.Tier)
	if tail := c.tail(); tail != "" {
		v.Reason += "; " + tail
	}
	if cp.Note != "" {
		v.Reason += "; " + cp.Note
	}
	// Состояние квоты идёт в вердикт не только на сдвиге: молчание корректора
	// одинаково выглядит и при снимке в норме, и при выключенном корректоре, а
	// решение про модель в этих случаях принимается на разных данных. Там, где
	// про снимок уже сказано предупреждением, второй раз не повторяем.
	if n := qf.note(); n != "" && !qf.Warned {
		v.Reason += "; " + n
	}
	// Совет отложить адресован тому, кто решает, браться ли за работу сейчас,
	// поэтому в вердикте ревьювера его нет: дифф к этому моменту уже написан,
	// откладывать нечего, а исполнительский вердикт по той же задаче совет и
	// так печатает.
	switch {
	case role == roleReview:
	case c.Down && strings.EqualFold(r.Type, "LLD"):
		v.Reason += "; дизайн слабой моделью это долгий ущерб, а сброс близко, так что если не горит, лучше отложить"
	case c.Down && costAtLeastM(r.Cost):
		v.Reason += "; сдвиг вниз на цене M и выше это заметная потеря качества в исполнении, а сброс близко, так что если не горит, лучше отложить"
	}
	for _, w := range warns {
		v.Reason += "; " + w
	}
	unc := "?"
	if n := uncertainty(r.Rank); n >= 0 {
		unc = fmt.Sprint(n)
	}
	if record {
		if err := recordStage(root, id, v, c, cp, qf, tm, now, role); err != nil {
			return res, err
		}
	}
	res.V, res.HC = v, hc
	res.Text = fmt.Sprintf("model: %s\neffort: %s\ntier: %s\nvia: %s\n%s (%s, цена %s, неопределённость %s): %s",
		v.Model, v.Effort, v.Tier, v.Via, r.ID, r.Type, r.Cost, unc, v.Reason)
	return res, nil
}

// recordStage открывает этап работы над задачей: вид деятельности и время
// начала уезжают в запись за пределами репозитория (internal/stage), а в файл
// задачи весь пакет этапов кладёт taskctl на смене статуса. Рабочего дерева
// вердикт при этом не касается вовсе, и правки, которую ревьювер обязан был
// коммитить за собой, больше нет (DK-120). Вид деятельности у ревью «ревью», у
// исполнения и грумминга «разработка»: словарь экранов знает четыре слова, а
// грумминговый вердикт это тот же заход в задачу, только разбирающий. Что
// вердикт был грумминговым, говорит текст записи: исполнение по нему не
// начинается, и запись не должна обещать то, чего не было. Сдвинутый вердикт
// несёт и маппинг, и причину сдвига: иначе по файлу задачи не понять, почему
// модель разошлась с таблицей. Состояние квоты идёт в текст всегда: без него
// запись про несдвинутый вердикт не отличает выключенный корректор от снимка в
// норме, а по закрытой задаче потом не восстановить, на каких данных модель
// выбиралась.
func recordStage(root, id string, v verdict, c correction, cp goalCap, qf quotaFacts, tm tierModels, now time.Time, role string) error {
	if _, err := os.Stat(filepath.Join(root, "docs", "tasks", id+".md")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("файла задачи нет, завести: taskctl file %s", id)
		}
		return err
	}
	kind := stage.Dev
	if role == roleReview {
		kind = stage.Review
	}
	var parts []string
	if c.shifted() {
		parts = append(parts, fmt.Sprintf("маппинг %s, корректор: %s", tm.named(c.From), c.Note))
	}
	switch {
	case cp.Note == "":
	case c.shifted() || cp.From == v.Tier:
		// Исходный маппинг уже назван корректором либо потолок ничего не срезал:
		// хватает самой причины.
		parts = append(parts, cp.Note)
	default:
		parts = append(parts, fmt.Sprintf("маппинг %s, %s", tm.named(cp.From), cp.Note))
	}
	if n := qf.note(); n != "" {
		parts = append(parts, n)
	}
	tail := ""
	if len(parts) > 0 {
		tail = " (" + strings.Join(parts, "; ") + ")"
	}
	// Строка несёт модель, а не ярус: по ней восстанавливают, чем задача
	// делалась. Уехавшая ступень несёт и харнес тем же «харнес:модель», что в
	// машинном конфиге, а домашняя пишется как раньше, без имени харнеса.
	// Развернуть ярус нечем, значит в строку идёт он сам, иначе исполнителя в
	// записи не осталось бы вовсе.
	name := tm.named(v.Tier)
	if name == unmappedModel {
		name = v.Tier
	}
	groom := ""
	if v.Groom {
		groom = "грумминговый вердикт, "
	}
	note := fmt.Sprintf("%s%s %s/%s по вердикту pick%s", groom, tm.word(v.Tier), name, v.Effort, tail)
	// Этап открывается по основному чекауту, а не по дереву задачи: pick зовут
	// с -C <worktree>, а закрывает пакет taskctl из основного чекаута, и без
	// приведения это были бы две разные записи.
	return stage.Open(stage.Home(), stage.MainRoot(root), id, kind, note, now)
}
