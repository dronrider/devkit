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

// cmdElapsed печатает минуты с открытия живого этапа записи задачи против
// планового лимита жизненного цикла агента (LLD DK-503, решение 1): этап
// открывает хук спавна субагента на каждом подъёме исполнителя (DK-911),
// команда только читает запись и сравнивает. Живой этап это последний, которого
// не закрыл писатель. Считать последнюю разработку в пакете нельзя: у записи
// без конца её закрывает следующий этап, и открытые следом ревью и слияние шли
// в счёт разработки часами, а команда без повода звала сдавать хвост (DK-874).
// Этап, которому по словарю не положена живая сессия (stage.NeedsSession),
// лимитом не меряется: там ждут человека или события, и сдавать хвост некому.
// Без открытого этапа (задача ещё не бралась в работу либо последний этап
// закрыт его писателем) команда честно говорит об этом и не падает: отсутствие
// данных не повод рвать заход, а минуты закрытого этапа лимитом не считаются.
func cmdElapsed(root, id string) (string, error) {
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		return "", err
	}
	now := timeNow()
	live, ok := rec.Live()
	if !ok {
		if n := len(rec.Stages); n > 0 {
			last := rec.Stages[n-1]
			return fmt.Sprintf("этап не открыт, лимит не проверить: этап %s закрыт писателем %s", last.Kind, last.End.Format(stage.Stamp)), nil
		}
		return "этап не открыт, лимит не проверить", nil
	}
	minutes := int(now.Sub(live.Start).Minutes())
	if minutes < 0 {
		minutes = 0
	}
	ceiling := execCeiling()
	verdict := fmt.Sprintf("лимит %d %s: в пределах", ceiling, pluralMinutes(ceiling))
	switch {
	case !stage.NeedsSession(live.Kind):
		// Этап без живой сессии по словарю это ожидание, и лимит захода к
		// нему не относится.
		verdict = "ожидание, лимит не считается"
	case minutes > ceiling:
		verdict = fmt.Sprintf("лимит %d %s пройден: сдавай хвост", ceiling, pluralMinutes(ceiling))
	}
	return fmt.Sprintf("этап %s открыт %d %s назад (с %s), %s",
		live.Kind, minutes, pluralMinutes(minutes), live.Start.Format(stage.Stamp), verdict), nil
}
