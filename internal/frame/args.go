// Волну разбора аргументов из DK-236 сюда кладёт LLD DK-237: три утилиты
// (shipctl, agentctl, trackctl) разбирали позиционные до fs.Parse, а хвост
// fs.Args() не смотрели, поэтому «agentctl pick DK-146 junk --role review»
// молча съедал --role review и выдавал исполнительский вердикт вместо
// ревьюверского. taskctl то же самое чинил локально в DK-099, теперь общий
// код живёт тут.

package frame

import (
	"flag"
	"fmt"
)

// ParseArgs разбирает аргументы подкоманды и отдаёт позиционные в порядке
// набора. Стандартный flag.Parse останавливается на первом не-флаге, а хвост
// из fs.Args() команды не смотрели вовсе, поэтому флаг перед позиционным
// выбрасывал и сам позиционный, и флаг молча (DK-236). Позиционный тут
// снимается по одному, а разбор продолжается с остатка, так что флаг стоит
// где угодно; всё, что за «--», позиционное как есть, даже если начинается с
// дефиса.
func ParseArgs(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		head, tail := args, []string(nil)
		for i, a := range args {
			if a == "--" {
				head, tail = args[:i+1], args[i+1:]
				break
			}
		}
		fs.Parse(head)
		rest := fs.Args()
		if len(rest) == 0 {
			return append(pos, tail...)
		}
		pos = append(pos, rest[0])
		args = append(append([]string{}, rest[1:]...), tail...)
	}
}

// NeedArgs проверяет число позиционных аргументов: и нехватку, и лишнее.
// Лишний аргумент это потерянные кавычки или промах мимо флага, и выбросить
// его молча значит потерять данные ровно так, как их теряла форма «флаг перед
// текстом». Возвращает ошибку, а не зовёт os.Exit: fail у каждой утилиты свой,
// в общий модуль он не въехал (LLD DK-237), поэтому вызывающий код делает свой
// «if err != nil { fail(err) }».
func NeedArgs(pos []string, min, max int, usage string) error {
	if len(pos) < min {
		return fmt.Errorf("жду: %s", usage)
	}
	if max >= 0 && len(pos) > max {
		return fmt.Errorf("лишний аргумент %q, жду: %s", pos[max], usage)
	}
	return nil
}
