package catalog

import "strings"

func (m *Model) Param(name string) *Param {
	for i := range m.Parameters {
		if m.Parameters[i].Name == name {
			return &m.Parameters[i]
		}
	}
	return nil
}

func (m *Model) PublicParams() []map[string]any {
	out := make([]map[string]any, 0, len(m.Parameters))
	for _, p := range m.Parameters {
		item := map[string]any{
			"name":     p.Name,
			"type":     p.Type,
			"required": p.Required,
		}
		if p.Default != nil {
			item["default"] = p.Default
		}
		if p.Min != nil {
			item["min"] = *p.Min
		}
		if p.Max != nil {
			item["max"] = *p.Max
		}
		if len(p.Enum) > 0 {
			item["enum"] = p.Enum
		}
		out = append(out, item)
	}
	return out
}

func (m *Model) RequiredNames() []string {
	var out []string
	for _, p := range m.Parameters {
		if p.Required {
			out = append(out, p.Name)
		}
	}
	return out
}

// ToolName is a stable MCP tool id: generate_<model-id> with unsafe runes as '_'.
func (m *Model) ToolName() string {
	id := strings.ToLower(strings.TrimSpace(m.ID))
	var b strings.Builder
	b.WriteString("generate_")
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (m *Model) InputSchema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range m.Parameters {
		props[p.Name] = ParamJSONSchema(p)
		if p.Required {
			required = append(required, p.Name)
		}
		if p.Type == "image" && p.Name == "input_image" {
			if _, ok := props["input_images"]; !ok {
				props["input_images"] = map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
					"description": "Several data URLs; order matches LoadImage maps_to. Alias of input_image. " +
						"Also accepted: input_reference, input_references.",
				}
			}
		}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func ParamJSONSchema(p Param) map[string]any {
	item := map[string]any{}
	switch p.Type {
	case "image":
		item["type"] = "string"
		item["description"] = "Image as a data URL (data:image/png;base64,...). Aliases: input_images, input_reference, input_references."
	case "integer", "int":
		item["type"] = "integer"
	case "number", "float":
		item["type"] = "number"
	case "boolean", "bool":
		item["type"] = "boolean"
	default:
		item["type"] = "string"
	}
	if p.Default != nil {
		item["default"] = p.Default
	}
	if p.Min != nil {
		item["minimum"] = *p.Min
	}
	if p.Max != nil {
		item["maximum"] = *p.Max
	}
	if len(p.Enum) > 0 {
		item["enum"] = p.Enum
	}
	return item
}

func (m *Model) SupportedNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add("prompt")
	add("model")
	if m.Modality == "image" {
		add("size")
		add("n")
		add("response_format")
		add("quality")
	} else {
		add("seconds")
		add("size")
	}
	for _, p := range m.Parameters {
		add(p.Name)
	}
	return out
}
