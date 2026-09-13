package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireFileLockDoesNotStealLiveProcessLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	owner, err := acquireFileLock(path, time.Second, "test")
	if err != nil {
		t.Fatalf("create live lock: %v", err)
	}
	defer func() { _ = owner.Release() }()
	if _, err := acquireFileLock(path, 40*time.Millisecond, "test"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("live lock result = %v, want timeout", err)
	}
}

func TestAcquireFileLockPersistsAcrossRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	first, err := acquireFileLock(path, time.Second, "test")
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent lock path disappeared: %v", err)
	}
	second, err := acquireFileLock(path, time.Second, "test")
	if err != nil {
		t.Fatalf("reacquire persistent lock: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}

func TestAcquireFileLockRejectsSymlinkPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.lock")
	target := filepath.Join(dir, "target.lock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create lock symlink: %v", err)
	}
	if _, err := acquireFileLock(path, 40*time.Millisecond, "test"); err == nil {
		t.Fatal("symlink lock path was accepted")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat rejected lock symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("rejected lock symlink was removed or replaced")
	}
}
