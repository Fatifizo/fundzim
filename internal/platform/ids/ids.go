// Package ids generates FundZim identifiers (UUIDv7, DATABASE §2).
package ids

import "github.com/google/uuid"

// New returns a new time-ordered UUIDv7 string. It panics only if the system random source fails,
// which is unrecoverable.
func New() string { return uuid.Must(uuid.NewV7()).String() }

// Valid reports whether s is a canonical UUID string.
func Valid(s string) bool {
	if len(s) != 36 {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}
