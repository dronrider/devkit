package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
)

// preparedBasis закрывает показанное от чужих правок: в него входят материал
// записи без машинного блока, канонический вид prepared-состояния, действующее
// определение и файлы назначения, то есть входы, на которых построен снимок
// подтверждения (LLD DK-1469). Показанные карточки и их редакции сверяются
// отдельно, поэтому ход доски или команды между вопросом и ответом вопрос не
// отменяет. Оба первых слагаемых одинаковы до и после миграции записи, поэтому
// висящий ask переживает перенос блока в prepared-файл. Отсутствующие файлы
// отличимы от пустых.
func preparedBasis(root string, source []byte, state draftproposal.State) (string, error) {
	h, err := draftmeta.Parse(source)
	if err != nil {
		return "", err
	}
	set, err := draftTypeSet(root)
	if err != nil {
		return "", err
	}
	def, ok := set.Types[h.Type]
	if !ok {
		return "", fmt.Errorf("типа %q нет в словаре", h.Type)
	}
	paths := []string{def.File}
	seen := map[string]bool{}
	for _, p := range state.Proposals {
		if p.Target == "" {
			continue
		}
		if !idRe.MatchString(p.Target) {
			return "", fmt.Errorf("%s: неверное назначение %q", p.ID, p.Target)
		}
		path := taskFileAbs(root, p.Target)
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	sort.Strings(paths)
	material, _, err := draftproposal.Material(source)
	if err != nil {
		return "", err
	}
	render, err := draftproposal.RenderPrepared(state)
	if err != nil {
		return "", err
	}
	parts := [][]byte{material, render}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			parts = append(parts, []byte(path), []byte("absent"))
			continue
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, []byte(path), []byte("present"), data)
	}
	return draftproposal.Digest(parts...), nil
}

// preparedSnapshot это снимок исходника для команд save и reject: он меняется
// при правке записи и при правке prepared-файла, потому что решение опирается
// на оба.
func preparedSnapshot(state draftproposal.State, record []byte) (string, error) {
	render, err := draftproposal.RenderPrepared(state)
	if err != nil {
		return "", err
	}
	return draftproposal.Digest(record, render), nil
}
