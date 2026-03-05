package main

import (
	"reflect"
	"testing"
)

func TestNormalizeState(t *testing.T) {
	sessions := []session{{Name: "api"}, {Name: "web"}, {Name: "db"}}
	st := state{
		Favorites: []string{"web", "web", "missing", "api"},
		Order:     []string{"db", "api", "missing", "db"},
	}

	got := normalizeState(st, sessions)

	if !reflect.DeepEqual(got.Favorites, []string{"web", "missing", "api"}) {
		t.Fatalf("favorites mismatch: got %v", got.Favorites)
	}
	if !reflect.DeepEqual(got.Order, []string{"db", "api", "web"}) {
		t.Fatalf("order mismatch: got %v", got.Order)
	}
}

func TestOrderSessions(t *testing.T) {
	sessions := []session{{Name: "api"}, {Name: "web"}, {Name: "db"}, {Name: "docs"}}
	st := state{
		Favorites: []string{"web", "api"},
		Order:     []string{"docs", "db"},
	}

	favorites, others := orderSessions(sessions, st)

	if got := names(favorites); !reflect.DeepEqual(got, []string{"web", "api"}) {
		t.Fatalf("favorites order mismatch: got %v", got)
	}
	if got := names(others); !reflect.DeepEqual(got, []string{"docs", "db", "api", "web"}) {
		t.Fatalf("all order mismatch: got %v", got)
	}
}

func TestAssignHotkeys(t *testing.T) {
	rows := []session{{Name: "web"}, {Name: "api"}, {Name: "db"}, {Name: "docs"}}

	got := assignHotkeys(rows, SessionHotkeyAlphabet())

	assertHotkey(t, got, "web", 'a')
	assertHotkey(t, got, "api", 's')
	assertHotkey(t, got, "db", 'd')
	assertHotkey(t, got, "docs", 'f')
}

func TestAssignHotkeysLimit(t *testing.T) {
	alpha := "as"
	rows := []session{{Name: "one"}, {Name: "two"}, {Name: "three"}}

	got := assignHotkeys(rows, alpha)

	assertHotkey(t, got, "one", 'a')
	assertHotkey(t, got, "two", 's')
	if _, ok := got["three"]; ok {
		t.Fatalf("expected no hotkey for third session")
	}
}

func names(sessions []session) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.Name)
	}
	return out
}

func assertHotkey(t *testing.T, got map[string]rune, name string, expected rune) {
	t.Helper()
	if got[name] != expected {
		t.Fatalf("hotkey mismatch for %s: got %q want %q", name, got[name], expected)
	}
}
