package taskhead

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

// groomRunGuard restricts a groomer's task launches to saved add receipts.
// It uses source state, never the client's displayed payload.
func groomRunGuard(root, source, mode, target string) error {
	if source == "" {
		return nil
	}
	if filepath.Base(source) != source || source == "." || source == ".." {
		return fmt.Errorf("неверный источник разбора")
	}
	if mode != "run" {
		return fmt.Errorf("разбор %s не заказывал выполнение", source)
	}
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "drafts", source+".md"))
	if err != nil {
		return fmt.Errorf("источник разбора %s не прочитан: %w", source, err)
	}
	state, err := draftproposal.Parse(data)
	if err != nil {
		return err
	}
	for _, p := range state.Proposals {
		if p.Status != "accepted" || p.Outcome != "add" || p.Result == nil || p.Result.Source != source || p.Result.Outcome != "add" || p.Result.Key == "" {
			continue
		}
		for _, receipt := range state.Receipts {
			if !reflect.DeepEqual(receipt, *p.Result) || receipt.Result == "отклонено" {
				continue
			}
			for _, id := range receipt.IDs {
				if id == target {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("%s: выполнение разрешено только принятым результатам add разбора %s", target, source)
}
