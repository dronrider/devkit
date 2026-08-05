package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Ярус в модель разворачивает agentctl: маппинг живёт в профиле активного
// харнеса, и второй читалки профиля стенду заводить незачем. Нет agentctl или
// харнес не настроен, значит модель называется флагом, а не угадывается.
const tierMapPrefix = "маппинг ярусов:"

func parseTierMap(out string) (map[string]string, error) {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, tierMapPrefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l, tierMapPrefix))
		if i := strings.LastIndex(rest, " ("); i >= 0 {
			rest = rest[:i]
		}
		m := map[string]string{}
		for _, part := range strings.Split(rest, ",") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				return nil, fmt.Errorf("маппинг ярусов не разобран: %q", l)
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k == "" || v == "" {
				return nil, fmt.Errorf("маппинг ярусов не разобран: %q", l)
			}
			m[k] = v
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("маппинг ярусов пуст: %q", l)
		}
		return m, nil
	}
	return nil, fmt.Errorf("в выводе agentctl harness нет строки «%s»", tierMapPrefix)
}

func resolveModel(tier string) (string, error) {
	out, err := exec.Command("agentctl", "harness").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("agentctl harness не отработал (%v: %s); назови модель флагом --model",
			err, strings.TrimSpace(string(out)))
	}
	m, err := parseTierMap(string(out))
	if err != nil {
		return "", fmt.Errorf("%v; назови модель флагом --model", err)
	}
	model, ok := m[tier]
	if !ok {
		var have []string
		for k := range m {
			have = append(have, k)
		}
		sort.Strings(have)
		return "", fmt.Errorf("яруса %q у активного харнеса нет, есть: %s", tier, strings.Join(have, ", "))
	}
	return model, nil
}
