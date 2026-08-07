package main

import (
	"bufio"
	"os"
	"path/filepath"
	"fmt"
	"strconv"
	"strings"
	"time"
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
	Timeout    time.Duration
}

// defaultDeployTimeout это предел времени на шаг выката, когда ключа
// deploy_timeout в конфиге нет. Запас взят к самому долгому штатному выкату,
// какой встречался: кросс-сборка релизных бинарей с нуля идёт единицы минут,
// получасовой предел её не режет, зато вставшая команда (сборка ждёт
// неподнятый демон Docker) кончается провалом, а не вечным молчанием (DK-154).
const defaultDeployTimeout = 30 * time.Minute

// loadDeployConfig читает .devkit/deploy.local, если он есть. Формат простой:
// строки вида key = value, # это комментарий, пустые строки пропускаются.
// Отсутствие файла не ошибка: выкат тогда остаётся за пользователем, как и до
// появления конфига.
func loadDeployConfig(root string) (deployConfig, error) {
	c := deployConfig{Timeout: defaultDeployTimeout}
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
		case "deploy_timeout":
			// Молча вернуться к умолчанию нельзя: опечатка в пределе оставила бы
			// выкат с чужим временем ожидания, а заметить это можно только на
			// вставшей команде.
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return c, fmt.Errorf("%s: deploy_timeout = %q не читается как предел времени, ждал длительность вида 90s, 30m, 2h", deployConfigPath, val)
			}
			c.Timeout = d
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
