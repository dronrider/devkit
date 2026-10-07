package main

// Журнал запусков. Пишет строку на каждый запуск с ID, задачей и расходом,
// снятым до сноса временного дома. Строка обновляется по мере набора чисел и
// переживает обрыв: каталог прогона сносится как раньше, транскрипты не
// хранятся, тела токенов в след не попадают (DK-1309).

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/spend"
)

// runJournal держит строку текущего запуска и дописывает её в журнал дома
// пользователя. Дом этот не временный HOME прогона: тот сносится вместе с
// транскриптами, а журнал обязан пережить прогон.
type runJournal struct {
	mu        sync.Mutex
	home      string
	id        string
	task      string
	repeats   int
	status    string
	usage     spend.Usage
	when      time.Time
	lastErr   error
	finalized bool
}

// activeJournal это журнал идущего процесса. Его зовут fail и обрыв по
// сигналу: без строки расход пропадал бы вместе с процессом.
var activeJournal *runJournal

// newRunJournal заводит журнал запуска. ID короткий и свой у каждого
// запуска: повтор ключа в файле задачи отметку заменяет, а строки журнала
// живут рядом. Дом берётся обычный, пользователя: временный HOME прогона
// сносится вместе с транскриптами.
func newRunJournal(task string, repeats int) *runJournal {
	home, _ := os.UserHomeDir()
	j := &runJournal{
		home:    home,
		id:      runID(),
		task:    task,
		repeats: repeats,
		status:  spend.StatusRun,
		when:    time.Now(),
	}
	activeJournal = j
	j.write()
	j.watchSignals()
	return j
}

// runID режет случайный ID запуска. Времени тут хватило бы на машине с
// одним стендом, а два одновременных прогона столкнулись бы на одной
// миллисекунде.
func runID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "ob-" + time.Now().Format("150405.000")
	}
	return "ob-" + hex.EncodeToString(b[:])
}

// update кладёт свежий расход в строку запуска. Зовётся после каждой сессии,
// пока дом цел: обрыв между сессиями сохраняет уже собранное.
func (j *runJournal) update(usage spend.Usage) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.finalized {
		return
	}
	j.usage = usage
	j.write()
}

// ok заканчивает прогоном, который дошёл до таблицы: такой идёт в свод,
// когда это замер с --task.
func (j *runJournal) ok(usage spend.Usage) {
	j.finish(spend.StatusOK, usage)
}

// abort заканчивает прогоном, который остановили. Расход в журнале
// остаётся, в свод не идёт.
func (j *runJournal) abort() {
	j.finish(spend.StatusAbort, j.usage)
}

// finish пишет финальный статус строки.
func (j *runJournal) finish(status string, usage spend.Usage) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.finalized {
		return
	}
	j.status = status
	j.usage = usage
	j.write()
	j.finalized = true
}

// write дописывает строку в журнал. Провал записи не роняет прогон: числа
// уже собраны, и журнала без них хватит на глаз, а остановка стенда из-за
// полного дома стоила бы всего прогона.
func (j *runJournal) write() {
	err := spend.WriteRun(j.home, spend.RunRow{
		ID:      j.id,
		Task:    j.task,
		Repeats: j.repeats,
		Status:  j.status,
		When:    j.when,
		Usage:   j.usage,
	})
	j.lastErr = err
}

// watchSignals ловит остановку с терминала: строка успевает уехать в журнал
// с собранным расходом, каким бы он ни был к моменту сигнала.
func (j *runJournal) watchSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		j.abort()
		logRun(".", 2)
		os.Exit(2)
	}()
}
