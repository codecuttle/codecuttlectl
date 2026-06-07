package types

import "github.com/invopop/jsonschema"

// JSONSchema implements the jsonschema.JSONSchemaProperty interface for FlexInt.
// This tells invopop/jsonschema to emit an accurate schema: the type accepts
// both a JSON integer and a string-encoded integer (to handle LLM quirks).
func (FlexInt) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			{Type: "integer"},
			{Type: "string", Pattern: "^-?[0-9]+$"},
		},
		Description: "Integer value (accepts both number and string-encoded number)",
	}
}

// JSONSchema implements the jsonschema.JSONSchemaProperty interface for FlexBool.
// This tells invopop/jsonschema to emit an accurate schema: the type accepts
// a JSON boolean, an integer (0/1), or a string like "true"/"false".
// The string variant uses a case-insensitive pattern rather than an enum to
// avoid rejecting common LLM outputs like "True" or "FALSE" during validation.
func (FlexBool) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			{Type: "boolean"},
			{Type: "integer", Enum: []any{0, 1}},
			{Type: "string", Pattern: "^(?i)(true|false|1|0|yes|no)$"},
		},
		Description: "Boolean value (accepts boolean, 0/1 integer, or string)",
	}
}
