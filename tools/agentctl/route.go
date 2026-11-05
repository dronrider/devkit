package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/team"
)

func routeURL(p team.Participant) (string, error) {
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("неверный endpoint участника")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := ip != nil && ip.IsLoopback()
	if p.Route == "local" {
		if !loopback || u.Scheme != "http" {
			return "", fmt.Errorf("local требует http с числовым loopback IP")
		}
	} else if p.Route != "cloud" || (u.Scheme != "https" && !(loopback && u.Scheme == "http")) {
		return "", fmt.Errorf("cloud требует https (http допустим только на loopback для стенда)")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	return u.String(), nil
}

func routePayload(data []byte, p team.Participant, masks []string) ([]byte, error) {
	h, err := draftmeta.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("не удалось проверить метаданные записи")
	}
	protected, err := draftmeta.RecordProtected(data)
	if err != nil {
		return nil, fmt.Errorf("не удалось проверить защиту записи")
	}
	if (draftmeta.RequiresLocal(h) || protected) && p.Route != "local" {
		return nil, fmt.Errorf("запись ждёт локальную модель; облачный маршрут запрещён")
	}
	if p.Kind != team.Agent || !p.HasRole(team.RoleExec) || p.Model == "" {
		return nil, fmt.Errorf("участнику нужны kind agent, role exec и model")
	}
	text := string(data)
	if p.Route == "cloud" {
		// Longest first prevents partial replacement exposing the tail of a value.
		masks = append([]string(nil), masks...)
		sort.SliceStable(masks, func(i, j int) bool { return len(masks[i]) > len(masks[j]) })
		for _, value := range masks {
			if value == "" {
				return nil, fmt.Errorf("пустая строка маскирования запрещена")
			}
			text = strings.ReplaceAll(text, value, "[REDACTED]")
		}
	}
	return json.Marshal(map[string]any{"model": p.Model, "stream": false, "messages": []map[string]string{{"role": "user", "content": text}}})
}

func cmdRoute(home, handle, path, maskPath string, out, diagnostic io.Writer) error {
	if handle == "" || path == "" {
		return fmt.Errorf("нужны --participant и --record")
	}
	list, err := team.Load(home)
	if err != nil {
		return fmt.Errorf("не удалось прочитать команду машины")
	}
	p, found := team.Find(list, handle)
	if !found {
		return fmt.Errorf("участник не найден: taskctl team list")
	}
	endpoint, err := routeURL(p)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("не удалось прочитать запись")
	}
	var masks []string
	if maskPath != "" {
		raw, err := os.ReadFile(maskPath)
		if err != nil || json.Unmarshal(raw, &masks) != nil {
			return fmt.Errorf("маски ждут JSON-массив строк")
		}
	}
	payload, err := routePayload(data, p, masks)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("не удалось собрать запрос")
	}
	req.Header.Set("Content-Type", "application/json")
	// No environment proxies, redirects, retries or fallback for private material.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	fmt.Fprintf(diagnostic, "маршрут: %s, участник: %s, fallback: нет\n", p.Route, p.Handle)
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("маршрут %s недоступен; облачный fallback не выполнялся", p.Route)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("маршрут вернул HTTP %d; fallback не выполнялся", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 || !json.Valid(raw) {
		return fmt.Errorf("маршрут вернул неверный или слишком большой JSON")
	}
	_, err = out.Write(append(raw, '\n'))
	return err
}
