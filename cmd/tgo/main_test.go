package main

import "testing"

func TestSessionHotkeyAlphabet(t *testing.T) {
	want := "asdfqwertzxcvb"
	if got := SessionHotkeyAlphabet(); got != want {
		t.Fatalf("hotkey alphabet mismatch: got %q want %q", got, want)
	}
}
