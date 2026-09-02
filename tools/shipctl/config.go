package main

import (
	"time"

	"github.com/dronrider/devkit/internal/deployconf"
)

// deployConfigPath, deployConfig и loadDeployConfig это обвязка выката проекта.
// Разбор файла живёт в internal/deployconf: тот же флаг автономии читает
// дашборд, решая, поднимать ли прогон сценария после выката (DK-718).
const deployConfigPath = deployconf.Rel

type deployConfig = deployconf.Config

const defaultDeployTimeout = deployconf.DefaultTimeout

func loadDeployConfig(root string) (deployConfig, error) { return deployconf.Load(root) }

// deployPlan разводит два исхода: run это команда, которую shipctl выполнит
// сам, manual это пояснение, почему выкат остаётся за пользователем (команда
// в конфиге есть, но автономия выключена). Оба пустые значат «плейбук проекта».
// autonomous поднят, когда агенту доверен весь конвейер: тогда merge и revert
// пушат результат сами, чтобы origin, доска и прод не разошлись.
// warn это предупреждение про проблемное конфигурирование (например, autonomous
// поднят, но команда выката не задана).
// timeout это предел времени на команду выката: за ним она убивается вместе с
// потомками, а выкат кончается провалом с внятным текстом.
type deployPlan struct {
	run        string
	manual     string
	autonomous bool
	warn       string
	timeout    time.Duration
}

// resolveDeploy решает, что делать с выкатом. Явный --deploy это указание
// пользователя прямо сейчас, выполняется всегда (пуш тогда за пользователем,
// по флагу --push). Без флага смотрим конфиг: команду катим сами только при
// autonomous=true, иначе оставляем её пользователю, показав, что именно
// запускать.
func resolveDeploy(root, flag string) (deployPlan, error) {
	cfg, err := loadDeployConfig(root)
	if err != nil {
		return deployPlan{}, err
	}
	// Предел времени берётся из конфига и при явном --deploy: команда с флага
	// виснет так же, как команда из конфига, а второго места, где предел
	// настраивают, заводить незачем.
	if flag != "" {
		return deployPlan{run: flag, timeout: cfg.Timeout}, nil
	}
	plan := deployPlan{autonomous: cfg.Autonomous, timeout: cfg.Timeout}
	switch {
	case cfg.Deploy == "":
		if cfg.Autonomous {
			plan.warn = "autonomous = true, но deploy пустой в " + deployConfigPath + ": выкат не запустится; вписать команду выката либо снять autonomous"
		}
	case cfg.Autonomous:
		plan.run = cfg.Deploy
	default:
		plan.manual = "команда в " + deployConfigPath + ", autonomous=false"
	}
	return plan, nil
}

// resolveTest решает, чем гонять тесты при слиянии. Явный --test это указание
// пользователя прямо сейчас и сильнее конфига; без флага команда берётся из
// ключа test, как берётся оттуда команда выката. Нет ни флага, ни ключа значит
// прежний отказ: ветка сливается только зелёной. Второе значение говорит, что
// команда пришла из конфига, и уходит в отчёт: прогон незнакомой командой
// иначе был бы неотличим от прогона своей.
// Ключ читает только merge. ship тестов не гоняет и так (код проверен при
// слиянии), а у revert пустой --test значит «без прогона»: откат чинит прод и
// должен быть быстрым, прогон на нём остаётся решением того, кто откатывает.
func resolveTest(root, flag string) (string, bool, error) {
	if flag != "" {
		return flag, false, nil
	}
	cfg, err := loadDeployConfig(root)
	if err != nil {
		return "", false, err
	}
	return cfg.Test, cfg.Test != "", nil
}

// unquote снимает окружающую пару кавычек значения. Разбор корп-конфига
// (corp.go) читает свои ключи тем же приёмом, что обвязка выката.
func unquote(s string) string { return deployconf.Unquote(s) }
