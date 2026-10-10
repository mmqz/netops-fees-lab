package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// formatTime renders t as the storage representation: UTC RFC3339
// (PROJECT_BRIEF §5).
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// parseTime parses a stored UTC RFC3339 timestamp.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("repo: parse timestamp %q: %w", s, err)
	}
	return t, nil
}

// nullString maps an empty string to SQL NULL (nil) for nullable TEXT
// columns, and any non-empty string to itself.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullablePtr maps a *string to a driver argument, preserving nil.
func nullablePtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// nullFloatPtr maps a *float64 to a driver argument, preserving nil (SQL
// NULL = "no value" — used by the virtual_devices geo columns).
func nullFloatPtr(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

// floatPtrFromNull converts a scanned nullable REAL into *float64 (nil when
// NULL) — the read side of nullFloatPtr.
func floatPtrFromNull(nf sql.NullFloat64) *float64 {
	if !nf.Valid {
		return nil
	}
	v := nf.Float64
	return &v
}

// nullableTimePtr maps a *time.Time to a driver argument (UTC RFC3339),
// preserving nil.
func nullableTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// stringFromNull converts a scanned nullable TEXT into a plain string.
func stringFromNull(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}

// stringPtrFromNull converts a scanned nullable TEXT into *string.
func stringPtrFromNull(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

// timePtrFromNull converts a scanned nullable timestamp into *time.Time.
func timePtrFromNull(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// mapJSON serializes m into the JSON storage representation. A nil or empty
// map is stored as "{}".
func mapJSON(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("repo: marshal map: %w", err)
	}
	return string(b), nil
}

// mapFromJSON deserializes a stored JSON object into map[string]string; an
// empty or "null" value yields an empty (non-nil) map.
func mapFromJSON(s string) (map[string]string, error) {
	if s == "" || s == "null" {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("repo: unmarshal map %q: %w", s, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint
// violation, so callers can map it to domain.ErrConflict.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// escapeLike escapes SQL LIKE wildcards (\, %, _) so a user-supplied search
// term matches literally. The corresponding LIKE clauses must use
// ESCAPE '\'.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// likePattern builds a case-insensitive contains pattern for q.
func likePattern(q string) string {
	return "%" + escapeLike(strings.ToLower(q)) + "%"
}

// paging normalizes limit/offset: limit <= 0 means "no limit" (SQL LIMIT -1),
// offset < 0 becomes 0.
func paging(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = -1
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// notFound returns an error wrapping domain.ErrNotFound when err indicates
// a missing row; otherwise it returns a contextual error wrapping err.
func notFound(kind, key string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("repo: %s %s: %w", kind, key, domain.ErrNotFound)
	}
	return fmt.Errorf("repo: %s %s: %w", kind, key, err)
}
