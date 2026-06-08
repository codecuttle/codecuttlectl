package backlog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateID creates a new unique work item ID with the format "wi_" + 8 hex chars.
// Uses crypto/rand for uniqueness guarantees.
func GenerateID() (string, error) {
	b := make([]byte, 4) // 4 bytes = 8 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating work item ID: %w", err)
	}
	return "wi_" + hex.EncodeToString(b), nil
}
