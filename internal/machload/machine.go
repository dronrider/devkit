package machload

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Память машины и средняя нагрузка читаются системным вызовом sysctl, а не
// подпроцессом: ps и vm_stat дают большую часть картины, но объём всей памяти
// и средняя нагрузка у системы спрашиваются дешевле и без ребёнка. Шов
// sysctlRead держит стенд: боевая машина зовёт системный вызов, тест кладёт
// сырой ответ.
var sysctlRead = sysctlRaw

// Mem это снимок памяти машины: вся, занятая и доля занятой в процентах.
type Mem struct {
	Total   int64
	Used    int64
	UsedPct float64
}

// MemPressure снимает память машины: vm_stat даёт страницы по состояниям,
// объём всей памяти берётся системным вызовом.
func MemPressure(run Run) (Mem, error) {
	total, err := MemTotal()
	if err != nil {
		return Mem{}, err
	}
	out, err := run("vm_stat")
	if err != nil {
		return Mem{}, fmt.Errorf("vm_stat не поднят: %w", err)
	}
	return ParseVMStat(string(out), total)
}

// Заявление vm_stat: размер страницы в заголовке и строки «Pages имя: N.» с
// точкой на конце. Строка компрессора стоит в кавычках, и кавычки входят в
// разбор, а не режутся до него.
var (
	vmPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)
	vmPageRe     = regexp.MustCompile(`^"?Pages ([^":]+?)"?:\s+(\d+)\.\s*$`)
)

// ParseVMStat разбирает вывод vm_stat. Занятой считается память активных,
// прибитых и сжатых страниц без освобождаемых: свободные и неактивные страницы
// это кеш файлов, который машина отдаёт без боли, и давлением они не являются.
// Чужие строки пропускаются: формат стабилен, а лишняя строка не должна ронять
// снимок. Пустой разбор это отказ, а не ноль занятости.
func ParseVMStat(out string, total int64) (Mem, error) {
	var page int64
	pages := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		if m := vmPageSizeRe.FindStringSubmatch(line); m != nil {
			page, _ = strconv.ParseInt(m[1], 10, 64)
			continue
		}
		m := vmPageRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.ParseInt(m[2], 10, 64)
		pages[strings.ToLower(m[1])] = n
	}
	if page <= 0 || total <= 0 || len(pages) == 0 {
		return Mem{}, fmt.Errorf("память по vm_stat не собралась: размер страницы %d, вся память %d, строк страниц %d",
			page, total, len(pages))
	}
	used := (pages["active"] + pages["wired down"] + pages["occupied by compressor"] +
		pages["throttled"] - pages["purgeable"]) * page
	if used < 0 {
		used = 0
	}
	if used > total {
		used = total
	}
	return Mem{Total: total, Used: used, UsedPct: float64(used) / float64(total) * 100}, nil
}

// MemTotal отдаёт объём памяти машины байтами.
func MemTotal() (int64, error) {
	raw, err := sysctlRead("hw.memsize")
	if err != nil {
		return 0, fmt.Errorf("объём памяти не прочитался: %w", err)
	}
	return ParseBytes(raw)
}

// ParseBytes разбирает целое из системного вызова: sysctl отдаёт его сырыми
// байтами в порядке машины. Замыкающий ноль числа бывает срезан библиотекой, и
// недостающий байт читается нулём.
func ParseBytes(raw []byte) (int64, error) {
	if len(raw) != 7 && len(raw) != 8 {
		return 0, fmt.Errorf("жду 8 байт числа, пришло %d", len(raw))
	}
	var buf [8]byte
	copy(buf[:], raw)
	return int64(binary.LittleEndian.Uint64(buf[:])), nil
}

// LoadAvg отдаёт среднюю нагрузку машины за минуту, пять и пятнадцать.
func LoadAvg() ([3]float64, error) {
	raw, err := sysctlRead("vm.loadavg")
	if err != nil {
		return [3]float64{}, fmt.Errorf("средняя нагрузка не прочиталась: %w", err)
	}
	return ParseLoadAvg(raw)
}

// loadavgCeiling это потолок осмысленного значения средней нагрузки: выше
// число получается только из сломанного масштаба, и такой разбор отказывает,
// а не несёт чушь на экран.
const loadavgCeiling = 1 << 20

// ParseLoadAvg разбирает структуру loadavg системного вызова: три числа с
// фиксированной точкой и масштаб. Длина структуры расходится по системам, и
// масштаб читается из неё самой, а не зашивается числом. Байты идут в порядке
// машины, у больших макосных машин это little-endian. Замыкающий ноль структуры
// бывает срезан библиотекой, и недостающий байт масштаба читается нулём.
func ParseLoadAvg(raw []byte) ([3]float64, error) {
	if len(raw) < 16 {
		return [3]float64{}, fmt.Errorf("жду структуру средней нагрузки, пришло %d байт", len(raw))
	}
	scale := uint64(1024)
	if len(raw) >= 23 {
		var tail [8]byte
		copy(tail[:], raw[16:])
		if v := binary.LittleEndian.Uint64(tail[:]); v > 0 {
			scale = v
		}
	} else if v := uint64(binary.LittleEndian.Uint32(raw[12:16])); v > 0 {
		scale = v
	}
	var out [3]float64
	for i := range out {
		v := float64(binary.LittleEndian.Uint32(raw[i*4:i*4+4])) / float64(scale)
		if v < 0 || v > loadavgCeiling {
			return [3]float64{}, fmt.Errorf("средняя нагрузка %g не лезет в масштаб %d", v, scale)
		}
		out[i] = v
	}
	return out, nil
}
