package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

// preparedBasis закрывает показанное от чужих правок: в него входят материал
// записи без машинного блока, канонический вид prepared-состояния, действующее
// определение и файлы назначения, то есть входы, на которых построен снимок
// подтверждения (LLD DK-1469). Показанные карточки и их редакции сверяются
// отдельно, поэтому ход доски или команды между вопросом и ответом вопрос не
// отменяет. Оба первых слагаемых одинаковы до и после миграции записи, поэтому
// висящий ask переживает перенос блока в prepared-файл. Отсутствующие файлы
// отличимы от пустых.
func preparedBasisInputs(root string, source []byte, state draftproposal.State) ([][]byte, error) {
	h, err := draftmeta.Parse(source)
	if err != nil {
		return nil, err
	}
	set, err := draftTypeSet(root)
	if err != nil {
		return nil, err
	}
	def, ok := set.Types[h.Type]
	if !ok {
		return nil, fmt.Errorf("типа %q нет в словаре", h.Type)
	}
	paths := []string{def.File}
	seen := map[string]bool{}
	for _, p := range state.Proposals {
		if p.Target == "" {
			continue
		}
		if !idRe.MatchString(p.Target) {
			return nil, fmt.Errorf("%s: неверное назначение %q", p.ID, p.Target)
		}
		for _, path := range preparedTargetPaths(root, p) {
			if !seen[path] {
				paths = append(paths, path)
				seen[path] = true
			}
		}
	}
	sort.Strings(paths)
	material, _, err := draftproposal.Material(source)
	if err != nil {
		return nil, err
	}
	parts := [][]byte{material, nil}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			parts = append(parts, []byte(path), []byte("absent"))
			continue
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, []byte(path), []byte("present"), data)
	}
	return parts, nil
}

func preparedBasisFromInputs(parts [][]byte, state draftproposal.State) (string, error) {
	render, err := draftproposal.RenderPrepared(state)
	if err != nil {
		return "", err
	}
	captured := append([][]byte(nil), parts...)
	captured[1] = render
	return draftproposal.Digest(captured...), nil
}

func preparedBasis(root string, source []byte, state draftproposal.State) (string, error) {
	parts, err := preparedBasisInputs(root, source, state)
	if err != nil {
		return "", err
	}
	return preparedBasisFromInputs(parts, state)
}

// preparedSnapshot это снимок исходника для команд save и reject: он меняется
// при правке записи и при правке prepared-файла, потому что решение опирается
// на оба. Digest берёт материал записи без машинного блока: миграция переносит
// блок в prepared-файл, и снимок висящего согласования обязан её пережить.
func preparedSnapshot(state draftproposal.State, record []byte) (string, error) {
	render, err := draftproposal.RenderPrepared(state)
	if err != nil {
		return "", err
	}
	material, _, err := draftproposal.Material(record)
	if err != nil {
		return "", err
	}
	return draftproposal.Digest(material, render), nil
}

func preparedBasisMatches(root, source, actual, shown string) (bool, error) {
	if actual == shown {
		return true, nil
	}
	aliases, err := drafttx.NumberAdoptions(root, source)
	if err != nil {
		return false, err
	}
	reached := map[string]bool{shown: true}
	for changed := true; changed; {
		changed = false
		for _, alias := range aliases {
			if !reached[alias.Before] || reached[alias.After] {
				continue
			}
			if alias.After == actual {
				return true, nil
			}
			reached[alias.After] = true
			changed = true
		}
	}
	return false, nil
}
