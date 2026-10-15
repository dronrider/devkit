// Package machload снимает загрузку машины: таблицу процессов ps, память по
// vm_stat и среднюю нагрузку по системному вызову sysctl. Зовущих двое:
// дашборд рисует кольца у логотипа и раздел ресурсов (DK-1388), а будущий
// сторож сессий DK-1141 по тому же съёму узнаёт, что душит машину, и своего
// ps не заводит. Съём живёт общим модулем, чтобы оба читали машину одними
// числами.
//
// Внешних зависимостей нет: релизная сборка идёт с CGO_ENABLED=0, и всё
// считывается подпроцессами ps и vm_stat да системным вызовом. Разбор отделён
// от съёма: парсеры едят строки и байты, и стенд проверяет их фикстурами,
// не поднимая машины.
package machload

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// shotTimeout это потолок одного подпроцесса съёма: ps и vm_stat отвечают
// десятками миллисекунд, и зависший ребёнок дольше этого срока съёму не нужен.
const shotTimeout = 5 * time.Second

// Run это запуск служебного подпроцесса съёма. Дашборд подставляет свой тихий
// запуск с настоящим домом, сторож DK-1141 подставит свой, а съём и разбор
// остаются одни.
type Run func(name string, args ...string) ([]byte, error)

// Quiet это запуск без обвязки: подпроцесс со сроком, без окружения дашборда.
// Стражу его хватает, дашборд строит свой запуск поверх тех же функций.
func Quiet(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shotTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s не ответил за %s и снят по сроку", name, shotTimeout)
	}
	return out, err
}

// Proc это строка таблицы процессов: процесс, его родитель, доля процессора
// из ps (сто это одно ядро) и резидентная память килобайтами.
type Proc struct {
	PID   int
	PPID  int
	PCPU  float64
	RSSKB int64
}

// psArgs это колонки съёма: процесс, родитель, доля процессора и память.
// Заголовки выключены, и порядок колонок это контракт разбора.
var psArgs = []string{"-axo", "pid=,ppid=,pcpu=,rss="}

// Procs снимает таблицу процессов подпроцессом ps.
func Procs(run Run) ([]Proc, error) {
	out, err := run("ps", psArgs...)
	if err != nil {
		return nil, fmt.Errorf("ps не поднят: %w", err)
	}
	return ParseProcs(string(out))
}

// ParseProcs разбирает вывод ps: по строке на процесс, колонки через пробел.
// Битые строки пропускаются: ps печатает таблицу на живой машине, и одна
// недоезжающая строка не должна ронять весь снимок. Пустой разбор это отказ:
// молчаливый ноль читался бы машиной без процессов.
func ParseProcs(out string) ([]Proc, error) {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			continue
		}
		rss, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			continue
		}
		procs = append(procs, Proc{PID: pid, PPID: ppid, PCPU: cpu, RSSKB: rss})
	}
	if len(procs) == 0 {
		return nil, fmt.Errorf("таблица процессов не разобралась: ps ответил %d байтами", len(out))
	}
	return procs, nil
}

// Spread это доля машины по корню: доля процессора в ядрах (сто это одно
// ядро) и резидентная память килобайтами. Память процессов складывается с
// двойным счётом общих страниц, для поиска виновника это принято: занятость
// всей машины считает vm_stat, а не эта сумма.
type Spread struct {
	CPU   float64
	RSSKB int64
}

// Other это имя доли процессов без корня: tmux-сервер, дети прогонов и всё,
// что в реестр сессий не входит.
const Other = "прочее"

// walkCeiling это потолок подъёма по родителям: цепочка длиннее него читается
// циклом, и процесс уходит в прочее, а не кружит по таблице.
const walkCeiling = 128

// Attribute раскладывает снимок по корням. Корень это названный PID: сессия
// реестра, сам дашборд, у сторожа своё. Процесс считается за ближайшим корнем:
// дашборд, поднятый из сессии, остаётся долей дашборда, а не сессии, и каждый
// процесс попадает в одну долю, а не во все, чьим потомком он приходится.
// Процессу без корня достаётся прочее.
func Attribute(procs []Proc, roots map[int]string) map[string]Spread {
	out := map[string]Spread{}
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}
	var walk func(pid, depth int) string
	walk = func(pid, depth int) string {
		if name, ok := roots[pid]; ok && name != "" {
			return name
		}
		if depth >= walkCeiling {
			return ""
		}
		up, ok := parent[pid]
		if !ok || up == pid {
			return ""
		}
		return walk(up, depth+1)
	}
	for _, p := range procs {
		name := walk(p.PID, 0)
		if name == "" {
			name = Other
		}
		s := out[name]
		s.CPU += p.PCPU
		s.RSSKB += p.RSSKB
		out[name] = s
	}
	return out
}

// Total складывает снимок целиком: доля процессора в ядрах и резидентная
// память всех процессов машины, без деления по корням.
func Total(procs []Proc) Spread {
	var out Spread
	for _, p := range procs {
		out.CPU += p.PCPU
		out.RSSKB += p.RSSKB
	}
	return out
}
