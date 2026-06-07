// Package schema provides JSON Schema generation from Go structs and protobuf
// message descriptors. It produces schemas suitable for LLM tool_use interfaces:
// compact JSON, no $id/$schema headers, with descriptions from struct tags.
//
// The primary workflow:
//  1. Plugin author defines a Go struct with json + jsonschema tags
//  2. Calls MustSchema(&myInput{}) in Describe() to get the JSON Schema string
//  3. The orchestrator passes this schema to the LLM's tool config
//  4. When the LLM produces JSON input, Validate() checks it against the schema
package schema

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

// reflectorDefaults configures invopop/jsonschema for LLM-friendly output.
func newReflector() *jsonschema.Reflector {
	r := &jsonschema.Reflector{
		// Don't add additionalProperties: false — LLMs sometimes add extra fields
		DoNotReference: true, // Inline all definitions (no $ref/$defs)
	}
	return r
}

// FromStruct generates a JSON Schema string from a Go struct.
// The struct should use json tags for field names and jsonschema/jsonschema_description
// tags for schema metadata. Returns compact JSON suitable for LLM tool configs.
//
// Supported struct tags:
//   - `json:"name,omitempty"` — field name, omitempty makes it non-required
//   - `jsonschema:"required"` — mark field as required
//   - `jsonschema:"enum=a,enum=b"` — enumerated values
//   - `jsonschema_description:"..."` — field description
//
// Types implementing JSONSchema() *jsonschema.Schema get custom schema output
// (e.g., FlexInt emits oneOf[integer, string]).
func FromStruct(v any) (string, error) {
	r := newReflector()
	s := r.Reflect(v)

	// Strip top-level properties that LLM tool APIs don't want
	s.ID = ""
	s.Version = ""

	// Marshal to compact JSON
	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("marshaling schema: %w", err)
	}

	// Post-process: remove empty fields that add noise
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return string(data), nil // Fall back to un-cleaned version
	}

	cleaned := cleanSchema(raw)
	result, err := json.Marshal(cleaned)
	if err != nil {
		return string(data), nil
	}

	return string(result), nil
}

// MustSchema is FromStruct that panics on error. Safe to use in Describe()
// implementations since it's called once at plugin startup.
func MustSchema(v any) string {
	s, err := FromStruct(v)
	if err != nil {
		panic(fmt.Sprintf("pluginkit/schema: failed to generate schema: %v", err))
	}
	return s
}

// Validate checks a JSON input against a JSON Schema string.
// Returns nil if valid, or a human-readable error suitable for returning
// to the LLM as a tool error message (so it can fix its input).
func Validate(schemaStr string, input []byte) error {
	c := validator.NewCompiler()
	c.DefaultDraft(validator.Draft2020)

	sch, err := validator.UnmarshalJSON(strings.NewReader(schemaStr))
	if err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}

	if err := c.AddResource("schema.json", sch); err != nil {
		return fmt.Errorf("compiling schema: %w", err)
	}

	compiled, err := c.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compiling schema: %w", err)
	}

	var v any
	if err := json.Unmarshal(input, &v); err != nil {
		return fmt.Errorf("invalid JSON input: %w", err)
	}

	if err := compiled.Validate(v); err != nil {
		// Extract a clean error message for the LLM
		return fmt.Errorf("input validation failed: %w", err)
	}

	return nil
}

// cleanSchema removes empty/null fields from a schema map to keep output compact.
// Also strips fields that are inappropriate for LLM tool schemas.
func cleanSchema(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		// Skip empty/null values
		if v == nil {
			continue
		}

		// Skip $schema and $id (LLM tool APIs don't use these)
		if k == "$schema" || k == "$id" {
			continue
		}

		// Strip additionalProperties: false — LLMs sometimes add extra fields
		// and we don't want validation to reject them
		if k == "additionalProperties" {
			if bv, ok := v.(bool); ok && !bv {
				continue
			}
		}

		switch val := v.(type) {
		case map[string]interface{}:
			if len(val) == 0 {
				continue
			}
			result[k] = cleanSchema(val)
		case []interface{}:
			if len(val) == 0 {
				continue
			}
			cleaned := make([]interface{}, 0, len(val))
			for _, item := range val {
				if sub, ok := item.(map[string]interface{}); ok {
					cleaned = append(cleaned, cleanSchema(sub))
				} else {
					cleaned = append(cleaned, item)
				}
			}
			result[k] = cleaned
		case string:
			if val == "" {
				continue
			}
			result[k] = val
		case float64:
			if val == 0 {
				// Keep explicit zeros for things like minItems
				// but skip for version/id fields (already handled above)
				result[k] = val
			} else {
				result[k] = val
			}
		case bool:
			// Keep all bools (including false — they're meaningful in schemas)
			result[k] = val
		default:
			result[k] = val
		}
	}
	return result
}
