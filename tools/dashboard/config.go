package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Конфиг живёт в ~/.devkit/dashboard.local до машинного конфига DK-042,
// формат «key = value», решётка комментарий, тот же, что у .devkit/deploy.local
// и ~/.devkit/watch.local. Незнакомый ключ это ошибка старта, а не молчаливый
// пропуск. Пустой список корней старт не роняет: доску нечего показывать, но
// вход и /healthz обязаны работать, и причина оседает в Errs, а не в тишине.

const (
	confRel = ".devkit/dashboard.local"
	// Порт по умолчанию по номеру цели DK-112.
	defaultPort = 7112
	logRel      = ".devkit/dashboard.log"
)

type Config struct {
	Path  string
	Home  string
	Roots []string
	Addr  string
	Port  int
	Token string
	// Ошибки наполнения, с которыми сервер живёт: их называют старт в журнале
	// и /healthz, потому что молчание тут неотличимо от «проектов нет».
	Errs []string
}

func confPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(confRel))
}

// expandHome разворачивает тильду в корнях поиска относительно дома.
func expandHome(home, p string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// LoadConfig читает конфиг. Отсутствие файла это штатный первый старт:
// секрет родится сам, а пустой список корней уже назван в Errs. Файл с
// правами шире 600 это ошибка: в нём секрет входа.
func LoadConfig(home string) (*Config, error) {
	cfg := &Config{Path: confPath(home), Home: home, Port: defaultPort}
	data, err := os.ReadFile(cfg.Path)
	if os.IsNotExist(err) {
		cfg.noRoots()
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(cfg.Path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("права %s должны быть 600, в нём секрет входа: chmod 600 %s",
			cfg.Path, cfg.Path)
	}
	for i, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		key, val, ok := strings.Cut(ln, "=")
		if !ok {
			// Содержимое строки в ошибку не идёт: ошибка старта попадает в
			// журнал с правами 644, а в строке без «=» может лежать секрет
			// (например, «token abc» с потерянным знаком).
			return nil, fmt.Errorf("%s:%d: строка без «=», жду «key = value»", cfg.Path, i+1)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "root":
			cfg.Roots = append(cfg.Roots, expandHome(home, val))
		case "addr":
			cfg.Addr = val
		case "port":
			p, err := strconv.Atoi(val)
			if err != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("%s:%d: порт %q не число 1..65535", cfg.Path, i+1, val)
			}
			cfg.Port = p
		case "token":
			cfg.Token = val
		default:
			return nil, fmt.Errorf("%s:%d: незнакомый ключ %q, жду root / addr / port / token", cfg.Path, i+1, key)
		}
	}
	if len(cfg.Roots) == 0 {
		cfg.noRoots()
	}
	return cfg, nil
}

func (c *Config) noRoots() {
	c.Errs = append(c.Errs, fmt.Sprintf(
		"в %s нет ни одной строки root: искать проекты негде, дописать «root = ~/projects»", c.Path))
}

// ListenAddr собирает адрес прослушивания; пустой addr слушает все
// интерфейсы, доступ решает канал, вход решает секрет.
func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Addr, c.Port)
}

// saveToken пишет секрет в конфиг, не трогая остальные строки; файл получает
// права 600. Отдельный файл под секрет отвергнут в LLD как второй файл с теми
// же правами и тем же временем жизни.
func saveToken(path, token string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		for _, ln := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if k, _, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == "token" {
				continue
			}
			if ln != "" || len(lines) > 0 {
				lines = append(lines, ln)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	lines = append(lines, "token = "+token)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	// WriteFile не меняет права существующего файла, а секрет требует 600.
	return os.Chmod(path, 0o600)
}
