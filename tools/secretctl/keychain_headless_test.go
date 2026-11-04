package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestHeadlessKeychainUnavailable(t *testing.T) {
	const diagnostic = "security: SecKeychainItemCreateFromContent (<default>): User interaction is not allowed.\nadd-generic-password: returned -25308"
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"success", nil, false},
		{"timeout", errSecurityTimeout, true},
		{"wrapped timeout", fmt.Errorf("security: %w", errSecurityTimeout), true},
		{"headless authorization", errors.New(diagnostic + ": exit status 36"), true},
		{"wrong exit status", errors.New(diagnostic + ": exit status 1"), false},
		{"missing exit status", errors.New(diagnostic), false},
		{"generic exit 36", errors.New("exit status 36"), false},
		{"diagnostic code only", errors.New("add-generic-password: returned -25308: exit status 36"), false},
		{"other keychain error", errors.New("security: SecKeychainItemCreateFromContent (<default>): Unknown error.\nadd-generic-password: returned -25308: exit status 36"), false},
		{"item missing", errors.New("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.: exit status 44"), false},
		{"bad timeout setting", errBadSecurityTimeout, false},
		{"other error", errors.New("permission denied"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := headlessKeychainUnavailable(tc.err); got != tc.want {
				t.Fatalf("headlessKeychainUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
