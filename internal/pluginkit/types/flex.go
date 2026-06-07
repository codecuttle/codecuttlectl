// Package types provides shared types for Cuttlebone plugin input handling.
// These types handle quirks in LLM-generated JSON (e.g., numbers emitted as
// strings, booleans as strings) and expose JSONSchema methods so that
// auto-derived schemas accurately reflect what the types accept.
package types

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FlexInt handles both integer and string-encoded integer values from LLM JSON.
// LLMs frequently emit numbers as strings ("5" instead of 5). This type
// transparently accepts either form and defaults to 0 on any parse failure.
type FlexInt int

// UnmarshalJSON accepts a JSON integer or a string-encoded integer.
// On any failure (non-numeric string, null, etc.) it defaults to 0 silently.
func (f *FlexInt) UnmarshalJSON(data []byte) error {
	// Fast path: try as a JSON number
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		*f = FlexInt(i)
		return nil
	}

	// Slow path: try as a string-encoded integer (e.g., "42")
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		s = strings.TrimSpace(s)
		if s != "" {
			if parsed, err := strconv.Atoi(s); err == nil {
				*f = FlexInt(parsed)
				return nil
			}
		}
	}

	// Default: 0 (null, empty string, non-numeric, etc.)
	*f = 0
	return nil
}

// MarshalJSON emits FlexInt as a standard JSON number.
func (f FlexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(f))
}

// Int returns the underlying int value.
func (f FlexInt) Int() int {
	return int(f)
}

// FlexBool handles boolean, integer (0/1), and string-encoded boolean values.
// LLMs sometimes emit "true"/"false" as strings, or 0/1 as booleans.
type FlexBool bool

// UnmarshalJSON accepts a JSON boolean, 0/1 integer, or string like "true"/"false"/"1"/"0".
func (f *FlexBool) UnmarshalJSON(data []byte) error {
	// Fast path: try as a JSON boolean
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*f = FlexBool(b)
		return nil
	}

	// Try as an integer (0 = false, non-zero = true)
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		*f = FlexBool(i != 0)
		return nil
	}

	// Try as a string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		s = strings.TrimSpace(strings.ToLower(s))
		switch s {
		case "true", "1", "yes":
			*f = true
			return nil
		case "false", "0", "no", "":
			*f = false
			return nil
		}
	}

	// Default: false
	*f = false
	return nil
}

// MarshalJSON emits FlexBool as a standard JSON boolean.
func (f FlexBool) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(f))
}

// Bool returns the underlying bool value.
func (f FlexBool) Bool() bool {
	return bool(f)
}
