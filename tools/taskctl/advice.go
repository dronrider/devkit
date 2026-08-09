package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// deployConfigPath держит обвязку выката проекта, и taskctl читает оттуда один
// ключ: кому доверена кнопка слияния. Файл гитигнорнут, разбор формата и
// остальных ключей у shipctl (config.go), сюда переезжает только чтение флага.
const deployConfigPath = ".devkit/deploy.local"

// autonomousFlag отвечает, поднят ли autonomous в обвязке выката. Нет файла
// или нет ключа значит false: ровно то же умолчание, что у shipctl, и
// подсказка обеих утилит на одном проекте называет одну ветку.
func autonomousFlag(root string) bool {
	f, err := os.Open(filepath.Join(root, deployConfigPath))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "autonomous" {
			continue
		}
		on, err := strconv.ParseBool(strings.Trim(strings.TrimSpace(val), `"'`))
		return err == nil && on
	}
	return false
}

// nextAfterMove называет следующий шаг конвейера после смены статуса. Знание
// «что дальше» живёт в выводе команды перехода, а не в прозе скилла: подсказка
// приходит свежей строкой ровно в тот момент, когда решение принимается, и её
// читает любая модель, чем бы она ни была занята до этого (DK-180, DK-205).
func nextAfterMove(root, id, target string) string {
	switch target {
	case SectInProgress:
		return "следующий шаг: код в дереве задачи (shipctl start " + id +
			"), правка вместе с тестами, дальше ревью и слияние (shipctl merge " + id +
			"); сдача это Check, а не «код написан»" + mergeFork(root, id)
	case SectCheck:
		return "следующий шаг: прогнать сценарий проверки из docs/tasks/" + id +
			".md; агентский сценарий агент прогоняет сам и закрывает задачу (taskctl close " + id +
			"), пользовательский ждёт слова пользователя"
	case SectBlocked:
		return "следующий шаг: снять блокер снаружи доски, после чего вернуть задачу в работу (taskctl move " +
			id + " in-progress); пока строка на блокере, работа по ней не идёт"
	}
	return "следующий шаг: строка ждёт очереди в Backlog, ранг и метаданные правит taskctl set " + id
}

// mergeFork договаривает развилку «кто нажимает кнопку»: ветка autonomous
// называется явно и вместе со значением флага. В ядре правил автономный режим
// сказан исключением в хвосте, и на нём агент сходит с конвейера ровно на
// готовой задаче (DK-205, диспетчер остановился со словами «пуш за тобой» при
// autonomous = true).
func mergeFork(root, id string) string {
	if autonomousFlag(root) {
		return ". Слияние зовёт агент сам: в " + deployConfigPath +
			" стоит autonomous = true, merge пушит и катит выкат, отдельного слова пользователя тут не ждут"
	}
	return ". Слияние за пользователем: в " + deployConfigPath +
		" стоит autonomous = false, агент останавливается на локальном коммите и ждёт команды"
}

// nextAfterClose называет следующий шаг после закрытия задачи: очередь выката
// освободилась, и конвейер идёт на второй круг.
func nextAfterClose() string {
	return "следующий шаг: очередь выката свободна, следующая задача берётся с доски (taskctl list, дальше taskctl move <ID> in-progress)"
}
