package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/dronrider/devkit/internal/stage"
)

// execCeilingDefault это потолок захода в минутах по умолчанию (LLD DK-503,
// решение 1). Число откалибровано по трём обычным заходам разбора DK-181
// (100, около 60, около 90 минут) и одному аномальному (639 минут): не
// задевает обычный заход и останавливает аномальный почти впятеро раньше
// автоматического сжатия контекста.
const execCeilingDefault = 120

// execCeilingEnv переопределяет потолок для стендов, тем же приёмом, что и
// потолки планировщика слота (slotTreesEnv, slotQuestionsEnv в slot.go).
const execCeilingEnv = "DEVKIT_EXEC_CEILING_MINUTES"

// execCeiling отдаёт потолок захода в минутах. Окружение перебивает число для
// стендов.
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

// cmdElapsed печатает минуты с открытия этапа «разработка» записи задачи
// против планового потолка захода (LLD DK-503, решение 1): диспетчер
// открывает этап вызовом agentctl pick --record перед каждым спавном
// исполнителя (DK-511), команда только читает запись и сравнивает. Без
// открытого этапа (диспетчер пропустил --record, либо задача ещё не бралась
// в работу) команда честно говорит об этом и не падает: отсутствие данных не
// повод рвать заход.
func cmdElapsed(root, id string) (string, error) {
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		return "", err
	}
	now := timeNow()
	d, ok := stage.Elapsed(rec, stage.Dev, now)
	if !ok {
		return "этап не открыт, потолок не проверить", nil
	}
	start := now.Add(-d)
	minutes := int(d.Minutes())
	ceiling := execCeiling()
	verdict := fmt.Sprintf("потолок %d %s: в пределах", ceiling, pluralMinutes(ceiling))
	if minutes > ceiling {
		verdict = fmt.Sprintf("потолок %d %s пройден: сдавай хвост", ceiling, pluralMinutes(ceiling))
	}
	return fmt.Sprintf("%s открыта %d %s назад (с %s), %s",
		stage.Dev, minutes, pluralMinutes(minutes), start.Format(stage.Stamp), verdict), nil
}
