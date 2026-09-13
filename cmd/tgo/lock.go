package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type fileLock struct {
	file *os.File
}

type fileLockOwner struct {
	PID          int       `json:"pid"`
	ProcessStart string    `json:"process_start,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func acquireFileLock(path string, timeout time.Duration, label string) (*fileLock, error) {
	deadline := time.Now().Add(timeout)
	for {
		lock, err := openFileLock(path)
		if err != nil {
			return nil, fmt.Errorf("lock %s: %w", label, err)
		}
		err = unix.Flock(int(lock.file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			if err := writeFileLockOwner(lock.file); err != nil {
				_ = lock.Release()
				return nil, fmt.Errorf("lock %s: %w", label, err)
			}
			return lock, nil
		}
		_ = lock.file.Close()
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("lock %s: %w", label, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock %s: timed out", label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func openFileLock(path string) (*fileLock, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create lock file handle")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect lock file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("lock path is not a regular file")
	}
	return &fileLock{file: file}, nil
}

func writeFileLockOwner(file *os.File) error {
	owner := fileLockOwner{
		PID:          os.Getpid(),
		ProcessStart: processStartToken(os.Getpid()),
		CreatedAt:    time.Now().UTC(),
	}
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("marshal lock owner: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek lock owner: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate lock owner: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write lock owner: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync lock owner: %w", err)
	}
	return nil
}

func (lock *fileLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}
