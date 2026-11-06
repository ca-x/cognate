package oidclogin

import (
	"strings"
	"testing"
)

func TestDisplayNameIsBoundedPresentationOnly(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		values []string
		want   string
	}{
		{"trim", []string{"  林同学  "}, "林同学"},
		{"fallback", []string{"", "czyt", "nickname"}, "czyt"},
		{"control", []string{"bad\nname", "safe"}, "safe"},
		{"direction", []string{"bad\u202ename", "safe"}, "safe"},
		{"length", []string{strings.Repeat("界", 43), "safe"}, "safe"},
		{"invalid UTF8", []string{"\xff", "safe"}, "safe"},
		{"empty", []string{"", " "}, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := displayName(scenario.values...); got != scenario.want {
				t.Fatal("unexpected display name")
			}
		})
	}
}
