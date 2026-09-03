package main

import (
	"strings"
	"time"
)

// Ретрай кончившейся подписки (DK-647). Разбор 480 сессий машины за 10 дней
// нашёл 11 полностью немых смертей. Харнес упирается в лимит, отвечает
// сокетом как живой, ничего не пишет в транскрипт и молча повторяет запрос
// сам, до трёхсот раз с часовым шагом. Единственный след на всей машине это
// собственная строка ретрая клиента в пейне tmux-сессии, вида «Weekly limit
// reached, Retrying in 1h (3pm), attempt 1/300». Готовые детекторы (клин,
// разлогин) её не ловят. Клину нужно молчание транскрипта плюс живой признак
// сломанного канала, а тут канал цел, и харнес сам не делает ни хода.
//
// Часу и часовому поясу в самой строке верить нельзя. Подсказка «(3pm)» не
// называет ни даты, ни зоны, а строка ретрая это диагностика клиента, не
// контракт. Срок сброса берётся из снимка ~/.devkit/quota (DK-633), который
// уже умеет считать квоту, второй источник тут не заводится (замечание
// разбора DK-647). Точно снимок называет только недельное окно. Пятичасовая
// сессия панелью узнаётся, но в снимок не пишется (agentctl usage.go,
// panelSection), и для неё срок остаётся не назван, а не угадан.

// quotaWaitWord это слова состояния «кончилась подписка». Харнес не отвечает
// и не пишет в транскрипт, но и не умирает. От клина (Stuck) это отличается
// тем, что лечится временем, а не перезапуском. Снятый и поднятый заново
// процесс встанет на тот же ретрай.
const quotaWaitWord = "лимит подписки исчерпан"

// quotaWaitNote собирает причину и срок одной фразой. Списку разговоров и
// ответу на реплику нужна одна и та же строка, а разойдись они словами,
// молчащий чат читался бы двумя разными состояниями.
func quotaWaitNote(reset string) string {
	if reset == "" {
		return quotaWaitWord + ", срок сброса неизвестен: в снимке квоты его нет"
	}
	return quotaWaitWord + ", сброс " + reset
}

// quotaRetryStuck узнаёт строку автоматического ретрая харнеса в тексте
// пейна. Пара слов, а не фраза целиком. Перенос строки в узком терминале мог
// разбить фразу пополам, а других версий у клиента разработчик не собирал.
func quotaRetryStuck(text string) bool {
	low := strings.ToLower(text)
	return strings.Contains(low, "limit reached") && strings.Contains(low, "retrying")
}

// quotaRetryWeekly отличает недельное окно от прочих. Срок сброса известен
// только для него (quotaResetOf), а для прочих строк ретрая (пятичасовая
// сессия, к примеру) бакет не угадывается.
func quotaRetryWeekly(text string) bool {
	return strings.Contains(strings.ToLower(text), "weekly")
}

// quotaWaitTTL держит память снимка пейна. Снимок берётся не на каждую
// сборку списка и не на каждую реплику, тем же порядком, что вопрос клиента
// (askSeenTTL, tmuxAsking).
const quotaWaitTTL = 10 * time.Second

// quotaWaitEntry это память снимка. Она хранит, узнан ли ретрай, был ли он
// недельным, и когда снят.
type quotaWaitEntry struct {
	retry, weekly bool
	born          time.Time
}

// quotaPaneOfFn это шов для тестов. Боевой сервер снимает панель, тест
// подставляет свой текст и tmux машины не трогает.
var quotaPaneOfFn = func(name string) string {
	out, err := runProc("tmux", "capture-pane", "-p", "-t", "="+name+":")
	if err != nil {
		return ""
	}
	return string(out)
}

// quotaWaitOf узнаёт ретрай кончившейся подписки в пейне живой сессии. Ответ
// помнится quotaWaitTTL. Спрашивают его и список чатов, и каждая реплика, а
// стоит он подпроцесса на каждую живую сессию машины.
func (s *server) quotaWaitOf(name string) (retry, weekly bool) {
	if name == "" || tmuxMissingCheck() != "" {
		return false, false
	}
	now := s.now()
	s.mu.Lock()
	e, hit := s.quotaSeen[name]
	s.mu.Unlock()
	if hit && now.Sub(e.born) < quotaWaitTTL {
		return e.retry, e.weekly
	}
	text := quotaPaneOfFn(name)
	retry = quotaRetryStuck(text)
	weekly = retry && quotaRetryWeekly(text)
	s.mu.Lock()
	if s.quotaSeen == nil {
		s.quotaSeen = map[string]quotaWaitEntry{}
	}
	s.quotaSeen[name] = quotaWaitEntry{retry: retry, weekly: weekly, born: now}
	s.mu.Unlock()
	return retry, weekly
}

// quotaResetOf находит срок сброса недельного окна для харнеса разговора.
// Снимок ~/.devkit/quota уже читает readQuota, второй разбор тут не
// заводится. Харнес неузнанный или ретрай не недельный, значит срок
// неизвестен, и вместо выдуманной даты остаётся молчание.
func (s *server) quotaResetOf(harness string, weekly bool) string {
	if harness == "" || !weekly {
		return ""
	}
	view := readQuota(s.cfg.Home, s.now())
	for _, h := range view.Harnesses {
		if h.Name != harness {
			continue
		}
		reset := quotaBucketReset(h.Buckets, "week_all")
		if reset == "" {
			// week_all это бакет базового яруса; снимок без него, но с другим
			// недельным бакетом (week_max и прочие ярусы) тоже сбрасывается
			// тем же окном (agentctl usage.go: разбивка по моделям делит одно
			// недельное время, не порождает второго).
			for _, b := range h.Buckets {
				if strings.HasPrefix(b.Name, "week") {
					reset = quotaBucketReset(h.Buckets, b.Name)
					if reset != "" {
						break
					}
				}
			}
		}
		if reset == "" {
			return ""
		}
		return quotaResetHuman(reset, s.now())
	}
	return ""
}

// quotaBucketReset достаёт срок сброса живого (не просроченного) бакета по
// имени. Просроченный сброс это протухшая цифра до пересъёма (DK-633), а не
// срок ожидания.
func quotaBucketReset(buckets []QuotaBucket, name string) string {
	for _, b := range buckets {
		if b.Name == name && !b.Expired && b.Reset != "" {
			return b.Reset
		}
	}
	return ""
}

// quotaResetHuman округляет момент сброса до понятного вида. Сегодняшний
// сброс это только часы, а сброс другим днём несёт дату, тем же порядком, что
// панель остатка подписки (quotaWhen в static/app.js).
func quotaResetHuman(reset string, now time.Time) string {
	t, err := parseQuotaTime(reset)
	if err != nil {
		return ""
	}
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("02.01 15:04")
}
