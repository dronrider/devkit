package modelcatalog

import "testing"

func TestCurrent(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"gpt-6-sol", "gpt-6.2-sol"},
		{"gpt-6.1-sol", "gpt-6.2-sol"},
		{"gpt-6-astra", "gpt-6.2-astra"},
		{"gpt-6.1-astra", "gpt-6.2-astra"},
		{"gpt-6.2-sol", "gpt-6.2-sol"},
		{"gpt-6.2-astra", "gpt-6.2-astra"},
		{"gpt-6.1-terra", "gpt-6.1-terra"},
		{"gpt-6-luna", "gpt-6-luna"},
		{"custom-model", "custom-model"},
		{"", ""},
	} {
		if got := Current(tc.model); got != tc.want {
			t.Errorf("Current(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}
