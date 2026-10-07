package main

// Журнал запусков. Пишет строку на каждый запуск с ID, задачей и расходом,
// снятым до сноса временного дома. Строка обновляется по мере набора чисел и
// переживает обрыв: каталог прогона сносится как раньше, транскрипты не
// хранятся, тела токенов в след не попадают (DK-1309).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

// startRunJournal пишет первую строку запуска. ID короткий и свой у каждого
// запуска: повтор ключа в файле задачи отметку заменяет, а строки журнала
// живут рядом.
func startRunJournal(home, task string, repeats int) *runJournal {
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

// openRunJournal заводит журнал до разбора флагов прогона. Разведка
// (--task с повторами ниже MinRepeats) до сессий не доходит и чисел не
// пишет, но строку в журнале оставляет: свод обязан её видеть (DK-1309).
func openRunJournal(home, task string, repeats int) (*runJournal, error) {
	j := startRunJournal(home, task, repeats)
	if task != "" && repeats < spend.MinRepeats {
		j.abort()
		return j, fmt.Errorf("--task %s при -k %d это разведка, а не замер: следу в файле задачи нужно хотя бы %d повтора на раскладку", task, repeats, spend.MinRepeats)
	}
	return j, nil
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
	if j.lastErr != nil {
		fmt.Fprintf(os.Stderr, "журнал запусков: финальный статус %q не сохранён: %v\n", status, j.lastErr)
	}
	j.finalized = true
}

// write дописывает строку в журнал. Провал записи не роняет прогон: числа
// уже собраны, и остановка стенда из-за полного дома стоила бы всего
// прогона. Молчать при этом нельзя: без строки расход в свод не дойдёт, и
// отказ обязан быть виден наружу (DK-1309).
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "журнал запусков: %v\n", err)
	}
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
