package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireFileLockRecoversDeadProcessGeneration(t *testing.T) {
	start := processStartToken(os.Getpid())
	if start == "" {
		t.Skip("process start tokens are not available")
	}
	path := filepath.Join(t.TempDir(), "state.lock")
	data, err := json.Marshal(fileLockOwner{
		PID:          os.Getpid(),
		ProcessStart: "different-generation",
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal stale lock: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
	lock, err := acquireFileLock(path, time.Second, "test")
	if err != nil {
		t.Fatalf("acquire stale lock: %v", err)
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(path)
	}()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovered lock: %v", err)
	}
	if !strings.Contains(string(contents), `"pid"`) {
		t.Fatalf("recovered lock has no owner metadata: %s", contents)
	}
}

func TestAcquireFileLockDoesNotStealLiveProcessLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	owner, err := createFileLock(path)
	if err != nil {
		t.Fatalf("create live lock: %v", err)
	}
	defer func() {
		_ = owner.Close()
		_ = os.Remove(path)
	}()
	if _, err := acquireFileLock(path, 40*time.Millisecond, "test"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("live lock result = %v, want timeout", err)
	}
}

func TestAcquireFileLockRemovesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.lock")
	if err := os.Symlink(filepath.Join(dir, "missing"), path); err != nil {
		t.Fatalf("create dangling lock symlink: %v", err)
	}
	lock, err := acquireFileLock(path, time.Second, "test")
	if err != nil {
		t.Fatalf("acquire through dangling lock symlink: %v", err)
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(path)
	}()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat recovered lock: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("dangling lock symlink was not replaced")
	}
}
