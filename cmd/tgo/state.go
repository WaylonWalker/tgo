package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type state struct {
	Favorites []string `json:"favorites"`
	Order     []string `json:"order"`
}

type stateStore struct {
	path string
}

func openStateStore() (*stateStore, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	return &stateStore{path: filepath.Join(configDir, "tgo", "state.json")}, nil
}

func (s *stateStore) Load() (state, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state{}, nil
		}
		return state{}, fmt.Errorf("read state: %w", err)
	}

	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		bak := s.path + ".bak"
		_ = os.Rename(s.path, bak)
		return state{}, nil
	}
	return st, nil
}

func (s *stateStore) Save(st state) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func normalizeState(st state, sessions []session) state {
	exists := make(map[string]struct{}, len(sessions))
	for _, s := range sessions {
		exists[s.Name] = struct{}{}
	}

	st.Favorites = dedupeAndFilter(st.Favorites, exists)

	favSet := make(map[string]struct{}, len(st.Favorites))
	for _, name := range st.Favorites {
		favSet[name] = struct{}{}
	}

	allowedOthers := make(map[string]struct{}, len(sessions)-len(st.Favorites))
	for _, s := range sessions {
		if _, fav := favSet[s.Name]; fav {
			continue
		}
		allowedOthers[s.Name] = struct{}{}
	}
	st.Order = dedupeAndFilter(st.Order, allowedOthers)
	return st
}

func dedupeAndFilter(items []string, allowed map[string]struct{}) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := allowed[item]; !ok {
			continue
		}
		if _, dup := seen[item]; dup {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
