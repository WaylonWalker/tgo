package main

import "strings"

// fuzzyMatch returns true if all characters in pattern appear in text in order
// (case-insensitive). An empty pattern matches everything.
func fuzzyMatch(pattern, text string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.ToLower(pattern)
	text = strings.ToLower(text)
	pi := 0
	for ti := 0; ti < len(text) && pi < len(pattern); ti++ {
		if text[ti] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}

// filterSessions returns only sessions whose Name fuzzy-matches pattern.
func filterSessions(sessions []session, pattern string) []session {
	if pattern == "" {
		return sessions
	}
	var result []session
	for _, s := range sessions {
		if fuzzyMatch(pattern, s.Name) {
			result = append(result, s)
		}
	}
	return result
}

// filterWindowUsage returns only rows whose SessionName fuzzy-matches pattern.
func filterWindowUsage(rows []windowUsage, pattern string) []windowUsage {
	if pattern == "" {
		return rows
	}
	var result []windowUsage
	for _, r := range rows {
		if fuzzyMatch(pattern, r.SessionName) {
			result = append(result, r)
		}
	}
	return result
}
