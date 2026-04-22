package mcphandler

// Small DSL for describing MCP tool input schemas. These helpers replace the
// map[string]interface{} noise that otherwise dominates tools.go, without
// introducing a codegen step or a shared dependency. Deliberately copied into
// apps/mcp/schema.go (standalone MCP) per the "share by copy, not import"
// rule for the three-module layout.

// prop builds a typed scalar property with a description.
func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// arrProp builds an array property whose items are of a primitive type.
func arrProp(itemType, desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": itemType},
		"description": desc,
	}
}

// objArrayProp builds an array-of-objects property with a nested item schema.
// Used by tools like berth_sandbox_push whose inputs include structured records.
func objArrayProp(desc string, itemProps map[string]any, itemRequired ...string) map[string]any {
	item := map[string]any{
		"type":       "object",
		"properties": itemProps,
	}
	if len(itemRequired) > 0 {
		item["required"] = itemRequired
	}
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       item,
	}
}

// schema builds a top-level input schema from a property map plus optional
// required-field names. Empty props + no required yields a zero-arg schema.
func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
