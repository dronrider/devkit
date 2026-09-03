package main

import (
	"fmt"
	"sort"
	"strings"
)

// ticket это тикет глазами адаптера: то немногое, что нужно циклу задачи.
// Постановка и обсуждение остаются в трекере, в devkit они не копируются
// (docs/lld/DK-074-corp-contour.md, «Граница доски и тикета»), кроме одного
// исключения: `trackctl review` тянет Description в файл задачи строки
// ревью, у неё постановка своя (LLD DK-756, решение 1). URL нужен той же
// команде для ссылки зеркальной строки на тикет.
type ticket struct {
	Key         string
	Status      string
	Type        string
	Title       string
	Estimate    string
	Description string
	URL         string
}

// adapter это обязательная часть контракта трекера. Трекер меняется от
// компании к компании, поэтому операции прибиты здесь, а реализация выбирается
// по имени из таблицы adapters, как разборщики протоколов в hookio.py.
// transition получает целевой статус одним шагом: путь через промежуточные
// статусы автоматика не ищет, на чужих переходах висят обязательные поля и
// уведомления.
type adapter interface {
	fetch(key string) (ticket, error)
	transition(key, status string, fields map[string]string) error
	assign(key, user string) error
	estimate(key, value string) error
	worklog(key, date, spent string) error
}

// Необязательные операции вынесены отдельными интерфейсами: адаптер, который
// их не умеет, просто не реализует, и отсутствие видно проверкой типа, а не
// заглушкой, молча отвечающей «готово». Такое отсутствие это честное
// отсутствие оси: trackctl status называет её вслух, а команды шагов по ней не
// делают.
type ranker interface {
	rank(key, field string, value int) error
}

type commenter interface {
	comment(key, text string) error
}

// updater это правка полей тикета: заголовок, тип и произвольные поля по
// имени. Значения приезжают строками, а в объекты их сворачивает адаптер:
// форма значения это знание трекера, а не команды.
type updater interface {
	update(key string, fields map[string]string) error
}

// mrLister это поиск MR по префиксу ветки: у Jira такой операции нет вовсе, у
// GitLab она была бы, но живого клиента GitLab в сборке сегодня нет, поэтому
// реализация лежит только в тестовой заглушке. В optionalOps эта ось не
// попадает: список там про правку тикета, а поиск MR тикет не трогает и
// свою честность про отсутствие называет сама команда `trackctl review`, а
// не общий вывод `status`.
type mrLister interface {
	mergeRequests(branchPrefix string) ([]string, error)
}

// optionalOps перечисляет необязательные операции контракта в порядке вывода.
var optionalOps = []struct {
	name string
	has  func(adapter) bool
	what string
}{
	{"rank", func(a adapter) bool { _, ok := a.(ranker); return ok }, "числовое поле приоритета не проставляется"},
	{"comment", func(a adapter) bool { _, ok := a.(commenter); return ok }, "комментарии в тикет не пишутся"},
	{"update", func(a adapter) bool { _, ok := a.(updater); return ok }, "поля тикета (заголовок, тип, произвольные) не правятся"},
}

// missingOptional отдаёт операции, которых у адаптера нет, каждую с тем, чего
// из-за неё не происходит.
func missingOptional(a adapter) []string {
	var out []string
	for _, op := range optionalOps {
		if !op.has(a) {
			out = append(out, fmt.Sprintf("%s: %s", op.name, op.what))
		}
	}
	return out
}

type adapterFactory func(c *contour) (adapter, error)

// adapters это таблица адаптеров по имени. Новый трекер добавляется строкой
// таблицы и своим кодом, контракт при этом не меняется.
var adapters = map[string]adapterFactory{}

func registerAdapter(name string, f adapterFactory) {
	adapters[name] = f
}

func adapterNames() []string {
	names := make([]string, 0, len(adapters))
	for n := range adapters {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func newAdapter(c *contour) (adapter, error) {
	f, ok := adapters[c.Adapter]
	if ok {
		return f(c)
	}
	if len(adapters) == 0 {
		return nil, fmt.Errorf("адаптер %q не знаком: в сборке нет ни одного адаптера трекера", c.Adapter)
	}
	return nil, fmt.Errorf("адаптер %q не знаком, в таблице есть: %s", c.Adapter, strings.Join(adapterNames(), ", "))
}

// transitionRefused это отказ воркфлоу трекера: прямого перехода в целевой
// статус нет. Разруливает такое человек один раз, поэтому отказ перечисляет
// доступные переходы и называет лекарство: недостающий промежуточный статус
// вписывается в таблицу контура синонимом и становится целевым.
type transitionRefused struct {
	Key       string
	Target    string
	Available []string
}

func (e *transitionRefused) Error() string {
	avail := "их нет вовсе"
	if len(e.Available) > 0 {
		avail = strings.Join(e.Available, ", ")
	}
	return fmt.Sprintf("тикет %s: прямого перехода в «%s» нет, доступны: %s; недостающий статус вписывается синонимом в [status] контура и становится целевым",
		e.Key, e.Target, avail)
}
