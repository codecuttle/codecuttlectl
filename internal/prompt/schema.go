package prompt

import "encoding/json"

// SchemaToToolParams parses a JSON Schema (as produced by pluginkit/schema) into
// a flat list of ToolParam entries suitable for system prompt rendering. It extracts
// the top-level "properties" object and the "required" array to determine each
// parameter's name, type, required status, and description.
//
// This allows the system prompt to dynamically render full parameter signatures
// directly from the plugin's InputSchema — no hardcoded parameter lists needed.
func SchemaToToolParams(schemaJSON json.RawMessage) []ToolParam {
	if len(schemaJSON) == 0 {
		return nil
	}

	var s schemaDoc
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return nil
	}

	if len(s.Properties) == 0 {
		return nil
	}

	// Build a set of required field names for O(1) lookup.
	reqSet := make(map[string]bool, len(s.Required))
	for _, r := range s.Required {
		reqSet[r] = true
	}

	params := make([]ToolParam, 0, len(s.Properties))
	for name, prop := range s.Properties {
		tp := ToolParam{
			Name:        name,
			Type:        resolveType(prop),
			Required:    reqSet[name],
			Description: prop.Description,
		}
		params = append(params, tp)
	}

	// Sort by: required first, then alphabetical within each group.
	sortToolParams(params)
	return params
}

// schemaDoc is a minimal representation of a JSON Schema object for parsing.
type schemaDoc struct {
	Properties map[string]schemaProp `json:"properties"`
	Required   []string              `json:"required"`
}

// schemaProp is a minimal representation of a single property in a JSON Schema.
type schemaProp struct {
	Type        interface{} `json:"type"`        // string or []string
	Description string      `json:"description"`
	Enum        []string    `json:"enum"`
	OneOf       []oneOfItem `json:"oneOf"`
	Items       *schemaItem `json:"items"`
}

// oneOfItem is a single entry in a oneOf array (used for FlexInt-style types).
type oneOfItem struct {
	Type    string `json:"type"`
	Pattern string `json:"pattern"`
}

// schemaItem describes the items field for array-type properties.
type schemaItem struct {
	Type string `json:"type"`
}

// resolveType determines the display type string for a schema property.
// Handles simple types, oneOf (e.g., FlexInt → "integer|string"), enums, and arrays.
func resolveType(prop schemaProp) string {
	// If oneOf is present, combine the types (e.g., FlexInt emits oneOf[integer, string]).
	if len(prop.OneOf) > 0 {
		types := make([]string, 0, len(prop.OneOf))
		for _, item := range prop.OneOf {
			if item.Type != "" {
				types = append(types, item.Type)
			}
		}
		if len(types) > 0 {
			return joinTypes(types)
		}
	}

	// Handle enum types — show as the base type with enum values.
	if len(prop.Enum) > 0 {
		base := typeString(prop.Type)
		if base == "" {
			base = "string"
		}
		return base + ", enum: " + joinEnum(prop.Enum)
	}

	// Handle array types.
	if typeString(prop.Type) == "array" && prop.Items != nil {
		return "[]" + prop.Items.Type
	}

	return typeString(prop.Type)
}

// typeString extracts a string from the Type field, which may be a string or []interface{}.
func typeString(t interface{}) string {
	switch v := t.(type) {
	case string:
		return v
	case []interface{}:
		// JSON Schema allows type: ["string", "null"] — take the first non-null.
		for _, item := range v {
			if s, ok := item.(string); ok && s != "null" {
				return s
			}
		}
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return "object"
}

// joinTypes joins type names with "|".
func joinTypes(types []string) string {
	result := types[0]
	for _, t := range types[1:] {
		result += "|" + t
	}
	return result
}

// joinEnum formats enum values for display.
func joinEnum(values []string) string {
	result := "["
	for i, v := range values {
		if i > 0 {
			result += ", "
		}
		result += "'" + v + "'"
	}
	result += "]"
	return result
}

// sortToolParams sorts parameters: required first, then alphabetical within each group.
func sortToolParams(params []ToolParam) {
	n := len(params)
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			if shouldSwap(params[i], params[j]) {
				params[i], params[j] = params[j], params[i]
			}
		}
	}
}

// shouldSwap returns true if b should come before a in sorted order.
func shouldSwap(a, b ToolParam) bool {
	// Required params come first.
	if a.Required != b.Required {
		return !a.Required && b.Required
	}
	// Within the same required group, sort alphabetically.
	return a.Name > b.Name
}
