package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPageNavigation(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		height, count, start, want int
		key                        tea.KeyType
		warnings, filtered         bool
	}{
		{name: "down", height: 18, count: 20, start: 0, want: 4, key: tea.KeyPgDown},
		{name: "up", height: 18, count: 20, start: 10, want: 6, key: tea.KeyPgUp},
		{name: "resized", height: 28, count: 30, start: 0, want: 14, key: tea.KeyPgDown},
		{name: "end", height: 18, count: 6, start: 4, want: 5, key: tea.KeyPgDown},
		{name: "start", height: 18, count: 6, start: 2, want: 0, key: tea.KeyPgUp},
		{name: "empty", height: 18, key: tea.KeyPgDown},
		{name: "warnings down", height: 18, count: 10, want: 4, key: tea.KeyPgDown, warnings: true},
		{name: "warnings up", height: 18, count: 10, start: 7, want: 3, key: tea.KeyPgUp, warnings: true},
		{name: "filtered end", height: 18, count: 3, want: 2, key: tea.KeyPgDown, filtered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model{height: tc.height, cursor: tc.start, showWarnings: tc.warnings}
			if tc.warnings {
				m.result.Warnings = make([]string, tc.count)
			} else {
				for i := 0; i < tc.count; i++ {
					m.result.Items = append(m.result.Items, finding{Path: "match"})
				}
			}
			if tc.filtered {
				m.query = "match"
				for i := 0; i < 10; i++ {
					m.result.Items = append(m.result.Items, finding{Path: "excluded"})
				}
			}
			updated, _ := m.Update(tea.KeyMsg{Type: tc.key})
			if got := updated.(model).cursor; got != tc.want {
				t.Fatalf("cursor = %d, want %d", got, tc.want)
			}
		})
	}
}
