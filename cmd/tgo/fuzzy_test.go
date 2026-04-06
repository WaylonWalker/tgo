package main

import (
	"reflect"
	"testing"
)

func TestFuzzyMatchEmpty(t *testing.T) {
	if !fuzzyMatch("", "anything") {
		t.Fatal("empty pattern should match everything")
	}
}

func TestFuzzyMatchExact(t *testing.T) {
	if !fuzzyMatch("web", "web") {
		t.Fatal("exact match should succeed")
	}
}

func TestFuzzyMatchSubstring(t *testing.T) {
	if !fuzzyMatch("api", "my-api-server") {
		t.Fatal("substring match should succeed")
	}
}

func TestFuzzyMatchScattered(t *testing.T) {
	if !fuzzyMatch("wb", "web") {
		t.Fatal("scattered chars w..b should match 'web'")
	}
	if !fuzzyMatch("dc", "docs") {
		t.Fatal("scattered chars d..c should match 'docs'")
	}
}

func TestFuzzyMatchCaseInsensitive(t *testing.T) {
	if !fuzzyMatch("WEB", "web") {
		t.Fatal("case-insensitive match should succeed")
	}
	if !fuzzyMatch("web", "WEB") {
		t.Fatal("case-insensitive match should succeed")
	}
}

func TestFuzzyMatchNoMatch(t *testing.T) {
	if fuzzyMatch("xyz", "web") {
		t.Fatal("non-matching pattern should fail")
	}
}

func TestFuzzyMatchOrderMatters(t *testing.T) {
	if fuzzyMatch("ba", "abc") {
		t.Fatal("out-of-order chars should not match")
	}
}

func TestFilterSessions(t *testing.T) {
	sessions := []session{
		{Name: "web"},
		{Name: "api"},
		{Name: "db"},
		{Name: "docs"},
	}

	got := filterSessions(sessions, "")
	if len(got) != 4 {
		t.Fatalf("empty filter should return all: got %d", len(got))
	}

	got = filterSessions(sessions, "d")
	want := []string{"db", "docs"}
	gotNames := make([]string, len(got))
	for i, s := range got {
		gotNames[i] = s.Name
	}
	if !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("filter 'd': got %v want %v", gotNames, want)
	}

	got = filterSessions(sessions, "wb")
	if len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("filter 'wb': got %v want [web]", got)
	}

	got = filterSessions(sessions, "xyz")
	if len(got) != 0 {
		t.Fatalf("filter 'xyz': got %v want []", got)
	}
}

func TestFilterWindowUsage(t *testing.T) {
	rows := []windowUsage{
		{Target: "%1", SessionName: "web"},
		{Target: "%2", SessionName: "api"},
		{Target: "%3", SessionName: "web"},
		{Target: "%4", SessionName: "db"},
	}

	got := filterWindowUsage(rows, "web")
	if len(got) != 2 {
		t.Fatalf("filter 'web': got %d want 2", len(got))
	}
	if got[0].Target != "%1" || got[1].Target != "%3" {
		t.Fatalf("filter 'web': wrong targets: %v", got)
	}

	got = filterWindowUsage(rows, "")
	if len(got) != 4 {
		t.Fatalf("empty filter should return all: got %d", len(got))
	}
}
