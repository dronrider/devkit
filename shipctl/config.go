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
// слова пользователя.
type deployConfig struct {
	Deploy     string
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
		case "autonomous":
			c.Autonomous, _ = strconv.ParseBool(val)
		}
	}
	return c, sc.Err()
}

// deployPlan разводит два исхода: run это команда, которую shipctl выполнит
// сам, manual это пояснение, почему выкат остаётся за пользователем (команда
// в конфиге есть, но автономия выключена). Оба пустые значат «плейбук проекта».
type deployPlan struct {
	run    string
	manual string
}

// resolveDeploy решает, что делать с выкатом. Явный --deploy это указание
// пользователя прямо сейчас, выполняется всегда. Без флага смотрим конфиг:
// команду катим сами только при autonomous=true, иначе оставляем её
// пользователю, показав, что именно запускать.
func resolveDeploy(root, flag string) (deployPlan, error) {
	if flag != "" {
		return deployPlan{run: flag}, nil
	}
	cfg, err := loadDeployConfig(root)
	if err != nil {
		return deployPlan{}, err
	}
	switch {
	case cfg.Deploy == "":
		return deployPlan{}, nil
	case cfg.Autonomous:
		return deployPlan{run: cfg.Deploy}, nil
	default:
		return deployPlan{manual: "команда в " + deployConfigPath + ", autonomous=false"}, nil
	}
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
