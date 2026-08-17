package main

import (
	"fmt"
	"os"
)

// Пять рубежей F из замера цели DK-395: доли активного времени задачи по
// фазам. Рубеж нигде не хранится, он считается по требованию из машинных
// меток (LLD DK-400, решение 1): доски, git и файла задачи, поэтому числа
// здесь константы, а не результат пересчёта.
const (
	progressBoard   = 0.00 // строка заведена
	progressCommit  = 0.35 // первый коммит кода
	progressReady   = 0.56 // код и тесты готовы
	progressRelease = 0.89 // ревью пройдено, слито и выкачено
	progressDone    = 1.00 // приёмка пройдена
)

// taskProgress это рубеж одной задачи: число F, название рубежа из таблицы
// замера и признак, по которому число получено. Признак едет и в печать, и в
// JSON, чтобы читатель рубежа видел не голое число, а чем оно заработано.
type taskProgress struct {
	ID   string  `json:"id"`
	F    float64 `json:"f"`
	Mark string  `json:"mark"`
	Sign string  `json:"sign"`
}

// progressOf считает рубеж задачи по признакам от старшего к младшему.
// Порядок проверок и есть монотонность: возврат из Check в работу оставляет
// разделы файла на месте, и 0.89 у задачи на втором круге остаётся честной
// правдой о сделанном. Признак «коммит сверх main» взят у ворот Check
// (unmergedTaskBranch): живая ветка с неслитыми коммитами значит, что код
// задачи уже написан; после слияния раздел «Выкат» закрывает рубеж старше.
func progressOf(root, id string) (*taskProgress, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return nil, err
	}
	rel := "docs/tasks/" + id + ".md"
	if b.find(id) == nil {
		arch, err := LoadArchive(archivePath(root))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && arch.has(id) {
			return &taskProgress{ID: id, F: progressDone,
				Mark: "приёмка пройдена", Sign: "строка в архиве"}, nil
		}
		drafts, err := loadDrafts(root)
		if err != nil {
			return nil, err
		}
		if findDraft(drafts, id) != nil {
			return nil, fmt.Errorf("%s черновик, рубеж считается у строки доски", id)
		}
		return nil, fmt.Errorf("%s нет ни на доске, ни в архиве", id)
	}
	// Разделы файла задачи читает taskDocSections: заголовки внутри
	// ограждённых блоков это чужой вывод прогона, а не разделы этой задачи.
	found, ok := taskDocSections(root, id, mergedSection, reviewHeading)
	switch {
	case ok && found[0]:
		return &taskProgress{ID: id, F: progressRelease,
			Mark: "ревью пройдено, слито и выкачено", Sign: "раздел «Выкат» в " + rel}, nil
	case ok && found[1]:
		return &taskProgress{ID: id, F: progressReady,
			Mark: "код и тесты готовы", Sign: "раздел «Ревью» в " + rel}, nil
	}
	if br, ahead := unmergedTaskBranch(root, id); br != "" {
		return &taskProgress{ID: id, F: progressCommit,
			Mark: "первый коммит кода",
			Sign: fmt.Sprintf("ветка %s не слита, коммитов впереди main %d", br, ahead)}, nil
	}
	return &taskProgress{ID: id, F: progressBoard,
		Mark: "строка заведена", Sign: "строка на доске, разделов файла и ветки нет"}, nil
}

// cmdProgress печатает рубеж одной строкой тем же стилем, каким list печатает
// возраст строки: число и короткое объяснение, откуда оно.
func cmdProgress(root, id string) (string, error) {
	p, err := progressOf(root, id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: рубеж %.2f (%s: %s)", p.ID, p.F, p.Mark, p.Sign), nil
}

// cmdProgressJSON отдаёт тот же рубеж машинным видом: планировщик слота и
// дашборд (DK-404) читают одно состояние с командой, второй копии рубежа нет.
func cmdProgressJSON(root, id string) (string, error) {
	p, err := progressOf(root, id)
	if err != nil {
		return "", err
	}
	return marshal(p)
}
