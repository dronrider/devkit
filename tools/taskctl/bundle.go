package main

import (
	"fmt"
	"github.com/dronrider/devkit/internal/draftideas"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func preparedMaterialDigest(record []byte) (string, error) {
	material, _, err := draftproposal.Material(record)
	if err != nil {
		return "", err
	}
	return draftproposal.Digest(material), nil
}

func preparedNotReady(record []byte, state draftproposal.State) string {
	if state.State == "history" {
		return "Разбор подтверждён, исходная запись сохранена в истории"
	}
	if _, pending := draftideas.Count(record); pending > 0 {
		return "Сначала разберите новые мысли"
	}
	if state.PreparedSource == "" || state.PackRevision < 1 {
		return "Проверьте и сохраните подготовленные описания, чтобы завершить разбор"
	}
	digest, err := preparedMaterialDigest(record)
	if err != nil || digest != state.PreparedSource {
		return "Исходная запись изменилась, завершите её разбор"
	}
	for _, p := range state.Proposals {
		if p.Status == "" {
			return ""
		}
	}
	return "Нет подготовленных результатов для подтверждения"
}

func validatePreparedBundle(root, id string, record []byte, state draftproposal.State) error {
	if _, pending := draftideas.Count(record); pending > 0 {
		return fmt.Errorf("%s: остались неразобранные мысли", id)
	}
	h, err := draftmeta.Parse(record)
	if err != nil {
		return err
	}
	set, err := draftTypeSet(root)
	if err != nil {
		return err
	}
	def, ok := set.Types[h.Type]
	if !ok {
		return fmt.Errorf("тип %s не найден", h.Type)
	}
	var selected []string
	for _, p := range state.Proposals {
		if p.Status == "" {
			selected = append(selected, p.ID)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("нет подготовленных результатов для подтверждения")
	}
	staged, err := newStagingBoard(root)
	if err != nil {
		return err
	}
	defer staged.Close()
	_, err = staged.planProposals(id, h, def, state, selected)
	return err
}
