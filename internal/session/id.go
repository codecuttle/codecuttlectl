package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateID creates a new unique session ID with the format "ses_" + 8 hex chars.
// Uses crypto/rand for uniqueness guarantees.
func GenerateID() (string, error) {
	b := make([]byte, 4) // 4 bytes = 8 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}
	return "ses_" + hex.EncodeToString(b), nil
}
