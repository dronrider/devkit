package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const deployConfigPath = ".devkit/deploy.local"

// deployConfig это проектная обвязка выката из .devkit/deploy.local. Файл
// гитигнорнут: в команде выката обычно адрес или роль машины, а её место в
// локальном, а не коммитимом (RULES.board.md, «Трекинг задач» п. 8). shipctl
// читает отсюда команду, чтобы не передавать --deploy на каждый merge, и флаг
// автономии: разрешает ли проект агенту катить на прод сам, без отдельного
// слова пользователя. Команда тестов лежит там же и по той же причине: она
// такая же принадлежность проекта, как команда выката, и без неё процедуре
// пачки пришлось бы сочинять --test под каждый репозиторий.
type deployConfig struct {
	Deploy     string
	Test       string
	Autonomous bool
}

// loadDeployConfig читает .devkit/deploy.local, если он есть. Формат простой:
// строки вида key = value, # это комментарий, пустые строки пропускаются.
// Отсутствие файла не ошибка: выкат тогда остаётся за пользователем, как и до
// появления конфига.
func loadDeployConfig(root string) (deployConfig, error) {
	var c deployConfig
	f, err := os.Open(filepath.Join(root, deployConfigPath))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		switch key {
		case "deploy":
			c.Deploy = val
		case "test":
			c.Test = val
		case "autonomous":
			c.Autonomous, _ = strconv.ParseBool(val)
		}
	}
	return c, sc.Err()
}

// deployPlan разводит два исхода: run это команда, которую shipctl выполнит
// сам, manual это пояснение, почему выкат остаётся за пользователем (команда
// в конфиге есть, но автономия выключена). Оба пустые значат «плейбук проекта».
// autonomous поднят, когда агенту доверен весь конвейер: тогда merge и revert
// пушат результат сами, чтобы origin, доска и прод не разошлись.
// warn это предупреждение про проблемное конфигурирование (например, autonomous
// поднят, но команда выката не задана).
type deployPlan struct {
	run        string
	manual     string
	autonomous bool
	warn       string
}

// resolveDeploy решает, что делать с выкатом. Явный --deploy это указание
// пользователя прямо сейчас, выполняется всегда (пуш тогда за пользователем,
// по флагу --push). Без флага смотрим конфиг: команду катим сами только при
// autonomous=true, иначе оставляем её пользователю, показав, что именно
// запускать.
func resolveDeploy(root, flag string) (deployPlan, error) {
	if flag != "" {
		return deployPlan{run: flag}, nil
	}
	cfg, err := loadDeployConfig(root)
	if err != nil {
		return deployPlan{}, err
	}
	plan := deployPlan{autonomous: cfg.Autonomous}
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

// unquote снимает одну окружающую пару кавычек, если значение целиком в них
// завёрнуто. Кавычки внутри команды (ssh host 'systemctl restart foo') не
// трогаются: снимается ровно внешняя пара, не все подряд.
func unquote(s string) string {
	if len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}
	return s
}
