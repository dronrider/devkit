package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/dronrider/devkit/internal/stage"
)

// execCeilingDefault это лимит жизненного цикла агента в минутах по
// умолчанию (LLD DK-503, решение 1). Число откалибровано по трём обычным
// заходам разбора DK-181 (100, около 60, около 90 минут) и одному
// аномальному (639 минут): не задевает обычный заход и останавливает
// аномальный почти впятеро раньше автоматического сжатия контекста.
const execCeilingDefault = 120

// execCeilingEnv переопределяет лимит для стендов, тем же приёмом, что и
// потолки планировщика слота (slotTreesEnv, slotQuestionsEnv в slot.go).
const execCeilingEnv = "DEVKIT_EXEC_CEILING_MINUTES"

// execCeiling отдаёт лимит жизненного цикла агента в минутах. Окружение
// перебивает число для стендов.
func execCeiling() int {
	if v := os.Getenv(execCeilingEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return execCeilingDefault
}

// pluralMinutes склоняет «минута» по числу.
func pluralMinutes(n int) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return "минут"
	}
	switch n % 10 {
	case 1:
		return "минута"
	case 2, 3, 4:
		return "минуты"
	default:
		return "минут"
	}
}

// cmdElapsed печатает минуты с открытия этапа исполнителя записи задачи
// против планового лимита жизненного цикла агента (LLD DK-503, решение 1):
// этап открывает хук спавна субагента на каждом подъёме исполнителя (DK-911),
// команда только читает запись и сравнивает. Меряется работа исполнителя над
// кодом, разработка либо доработка после ревью: сравнение идёт предикатом
// словаря, а не словом. Без открытого этапа (задача ещё не бралась в работу)
// команда честно говорит об этом и не падает: отсутствие данных не повод рвать
// заход.
func cmdElapsed(root, id string) (string, error) {
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		return "", err
	}
	now := timeNow()
	s, ok := stage.LastOf(rec, stage.IsExec)
	if !ok {
		return "этап не открыт, лимит не проверить", nil
	}
	d := now.Sub(s.Start)
	start := s.Start
	minutes := int(d.Minutes())
	ceiling := execCeiling()
	verdict := fmt.Sprintf("лимит %d %s: в пределах", ceiling, pluralMinutes(ceiling))
	if minutes > ceiling {
		verdict = fmt.Sprintf("лимит %d %s пройден: сдавай хвост", ceiling, pluralMinutes(ceiling))
	}
	return fmt.Sprintf("%s открыта %d %s назад (с %s), %s",
		s.Kind, minutes, pluralMinutes(minutes), start.Format(stage.Stamp), verdict), nil
}
