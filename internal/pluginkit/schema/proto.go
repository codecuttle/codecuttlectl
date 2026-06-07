package schema

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// FromProtoDescriptor generates a JSON Schema string from a protobuf message
// descriptor. This enables cross-language plugins that share a .proto file to
// have their JSON Schema auto-derived from the proto definition.
//
// The generated schema uses protobuf's JSON mapping (camelCase field names by
// default, with original_name available via proto field name).
//
// Usage:
//
//	import "google.golang.org/protobuf/reflect/protoreflect"
//
//	msg := &MyProtoInput{}
//	schema, err := schema.FromProtoDescriptor(msg.ProtoReflect().Descriptor())
//
// Field descriptions are derived from proto field names (humanized). For richer
// descriptions, use ProtoSchemaOptions.Descriptions to supply them per field.
func FromProtoDescriptor(md protoreflect.MessageDescriptor) (string, error) {
	return FromProtoDescriptorWithOptions(md, ProtoSchemaOptions{})
}

// ProtoSchemaOptions configures proto-to-schema generation.
type ProtoSchemaOptions struct {
	// Descriptions maps field JSON names to description strings.
	// If a field is not in this map, its description is derived from the field name.
	Descriptions map[string]string

	// Required lists field JSON names that should be marked as required.
	// If nil, no fields are required (proto3 has no required concept).
	Required []string

	// UseProtoNames uses the original proto field names (snake_case) instead of
	// camelCase JSON names. Default: false (use camelCase, matching protojson).
	UseProtoNames bool
}

// FromProtoDescriptorWithOptions generates a JSON Schema with custom options.
func FromProtoDescriptorWithOptions(md protoreflect.MessageDescriptor, opts ProtoSchemaOptions) (string, error) {
	schema := protoMessageToSchema(md, opts, 0)

	data, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("marshaling proto schema: %w", err)
	}

	return string(data), nil
}

// MustProtoSchema is FromProtoDescriptor that panics on error.
// Safe for use in Describe() implementations.
func MustProtoSchema(md protoreflect.MessageDescriptor) string {
	s, err := FromProtoDescriptor(md)
	if err != nil {
		panic(fmt.Sprintf("pluginkit/schema: failed to generate proto schema: %v", err))
	}
	return s
}

// MustProtoSchemaWithOptions is FromProtoDescriptorWithOptions that panics on error.
func MustProtoSchemaWithOptions(md protoreflect.MessageDescriptor, opts ProtoSchemaOptions) string {
	s, err := FromProtoDescriptorWithOptions(md, opts)
	if err != nil {
		panic(fmt.Sprintf("pluginkit/schema: failed to generate proto schema: %v", err))
	}
	return s
}

const maxProtoDepth = 10 // Prevent infinite recursion on cyclic messages

// protoMessageToSchema converts a message descriptor to a JSON Schema map.
func protoMessageToSchema(md protoreflect.MessageDescriptor, opts ProtoSchemaOptions, depth int) map[string]interface{} {
	if depth > maxProtoDepth {
		return map[string]interface{}{"type": "object"}
	}

	properties := make(map[string]interface{})
	fields := md.Fields()

	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)

		// Determine JSON field name
		jsonName := string(fd.JSONName())
		if opts.UseProtoNames {
			jsonName = string(fd.Name())
		}

		// Skip map entry fields (handled via the map field itself)
		if fd.IsMap() {
			properties[jsonName] = protoMapFieldSchema(fd, opts, depth)
			continue
		}

		// Handle repeated fields (arrays)
		if fd.IsList() {
			itemSchema := protoFieldTypeSchema(fd, opts, depth)
			prop := map[string]interface{}{
				"type":  "array",
				"items": itemSchema,
			}
			if desc := fieldDescription(jsonName, fd, opts); desc != "" {
				prop["description"] = desc
			}
			properties[jsonName] = prop
			continue
		}

		// Singular field
		prop := protoFieldTypeSchema(fd, opts, depth)
		if desc := fieldDescription(jsonName, fd, opts); desc != "" {
			prop["description"] = desc
		}
		properties[jsonName] = prop
	}

	// Handle oneofs — convert to JSON Schema oneOf
	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}

	if len(opts.Required) > 0 {
		schema["required"] = opts.Required
	}

	return schema
}

// protoFieldTypeSchema returns the JSON Schema for a single proto field's type.
func protoFieldTypeSchema(fd protoreflect.FieldDescriptor, opts ProtoSchemaOptions, depth int) map[string]interface{} {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return map[string]interface{}{"type": "boolean"}

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return map[string]interface{}{"type": "integer"}

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		// In proto JSON, int64/uint64 are encoded as strings to avoid precision loss.
		// We accept both forms (matching protojson behavior on unmarshal).
		return map[string]interface{}{
			"oneOf": []interface{}{
				map[string]interface{}{"type": "integer"},
				map[string]interface{}{"type": "string", "pattern": "^-?[0-9]+$"},
			},
		}

	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]interface{}{"type": "number"}

	case protoreflect.StringKind:
		return map[string]interface{}{"type": "string"}

	case protoreflect.BytesKind:
		// Bytes are base64-encoded in proto JSON
		return map[string]interface{}{"type": "string", "contentEncoding": "base64"}

	case protoreflect.EnumKind:
		return protoEnumSchema(fd.Enum())

	case protoreflect.MessageKind, protoreflect.GroupKind:
		return protoNestedMessageSchema(fd.Message(), opts, depth)

	default:
		return map[string]interface{}{"type": "string"}
	}
}

// protoEnumSchema generates a JSON Schema for a proto enum (string values).
func protoEnumSchema(ed protoreflect.EnumDescriptor) map[string]interface{} {
	values := ed.Values()
	enumVals := make([]interface{}, 0, values.Len())
	for i := 0; i < values.Len(); i++ {
		// Use the enum value name as a string (matching protojson default behavior)
		enumVals = append(enumVals, string(values.Get(i).Name()))
	}
	return map[string]interface{}{
		"type": "string",
		"enum": enumVals,
	}
}

// protoNestedMessageSchema handles nested message types, including well-known types.
func protoNestedMessageSchema(md protoreflect.MessageDescriptor, opts ProtoSchemaOptions, depth int) map[string]interface{} {
	fullName := string(md.FullName())

	// Handle well-known types with their JSON representations
	switch fullName {
	case "google.protobuf.Timestamp":
		return map[string]interface{}{
			"type":        "string",
			"format":      "date-time",
			"description": "RFC 3339 timestamp",
		}
	case "google.protobuf.Duration":
		return map[string]interface{}{
			"type":        "string",
			"pattern":     "^-?[0-9]+(\\.[0-9]+)?s$",
			"description": "Duration (e.g., '1.5s', '300s')",
		}
	case "google.protobuf.Struct":
		return map[string]interface{}{"type": "object"}
	case "google.protobuf.Value":
		return map[string]interface{}{} // Any JSON value
	case "google.protobuf.ListValue":
		return map[string]interface{}{"type": "array"}
	case "google.protobuf.BoolValue":
		return map[string]interface{}{"type": "boolean"}
	case "google.protobuf.StringValue":
		return map[string]interface{}{"type": "string"}
	case "google.protobuf.Int32Value", "google.protobuf.UInt32Value":
		return map[string]interface{}{"type": "integer"}
	case "google.protobuf.Int64Value", "google.protobuf.UInt64Value":
		return map[string]interface{}{
			"oneOf": []interface{}{
				map[string]interface{}{"type": "integer"},
				map[string]interface{}{"type": "string", "pattern": "^-?[0-9]+$"},
			},
		}
	case "google.protobuf.FloatValue", "google.protobuf.DoubleValue":
		return map[string]interface{}{"type": "number"}
	}

	// Regular nested message — recurse
	return protoMessageToSchema(md, opts, depth+1)
}

// protoMapFieldSchema generates a JSON Schema for a proto map field.
// Proto maps are represented as JSON objects with string keys.
func protoMapFieldSchema(fd protoreflect.FieldDescriptor, opts ProtoSchemaOptions, depth int) map[string]interface{} {
	mapEntry := fd.MapValue()
	valueSchema := protoFieldTypeSchema(mapEntry, opts, depth)

	prop := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": valueSchema,
	}

	jsonName := string(fd.JSONName())
	if opts.UseProtoNames {
		jsonName = string(fd.Name())
	}
	if desc := fieldDescription(jsonName, fd, opts); desc != "" {
		prop["description"] = desc
	}

	return prop
}

// fieldDescription returns the description for a field.
func fieldDescription(jsonName string, fd protoreflect.FieldDescriptor, opts ProtoSchemaOptions) string {
	// Check explicit descriptions first
	if opts.Descriptions != nil {
		if desc, ok := opts.Descriptions[jsonName]; ok {
			return desc
		}
	}
	// No auto-generated description — keep schema compact
	return ""
}
