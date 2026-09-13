package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

type fileLockOwner struct {
	PID          int       `json:"pid"`
	ProcessStart string    `json:"process_start,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

const unknownLockOwnerAge = 30 * time.Second

func acquireFileLock(path string, timeout time.Duration, label string) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	for {
		lock, err := createFileLock(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock %s: %w", label, err)
		}
		if recoverStaleFileLock(path) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock %s: timed out", label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func createFileLock(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	owner := fileLockOwner{
		PID:          os.Getpid(),
		ProcessStart: processStartToken(os.Getpid()),
		CreatedAt:    time.Now().UTC(),
	}
	data, err := json.Marshal(owner)
	if err == nil {
		_, err = lock.Write(append(data, '\n'))
	}
	if err == nil {
		err = lock.Sync()
	}
	if err != nil {
		_ = lock.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write lock owner: %w", err)
	}
	return lock, nil
}

func recoverStaleFileLock(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	data, readErr := os.ReadFile(path)
	var owner fileLockOwner
	if readErr == nil {
		readErr = json.Unmarshal(data, &owner)
	}
	if readErr == nil && owner.PID > 0 {
		if runtime.GOOS != "linux" {
			return false
		}
		procPath := filepath.Join("/proc", strconv.Itoa(owner.PID))
		if _, procErr := os.Stat(procPath); procErr == nil {
			currentStart := processStartToken(owner.PID)
			if owner.ProcessStart == "" || currentStart == "" || owner.ProcessStart == currentStart {
				return false
			}
		} else if !errors.Is(procErr, os.ErrNotExist) {
			return false
		}
	} else if time.Since(info.ModTime()) < unknownLockOwnerAge {
		return false
	}
	removeErr := os.Remove(path)
	return removeErr == nil || errors.Is(removeErr, os.ErrNotExist)
}
