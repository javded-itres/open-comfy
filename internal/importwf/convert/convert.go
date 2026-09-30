package convert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
)

func IsAPIFormat(raw json.RawMessage) bool {
	g, err := catalog.UnwrapRaw(raw)
	return err == nil && g != nil
}

// ConvertWith prefers ComfyUI's own Save-(API) / graphToPrompt conversion
// (POST /workflow/convert). Falls back to the local expander if the endpoint
// is missing or still returns UUID subgraph nodes.
func ConvertWith(ctx context.Context, client *comfy.Client, raw json.RawMessage, info map[string]comfy.NodeDef) (map[string]any, error) {
	var g map[string]any
	if client != nil && IsUIWorkflow(raw) {
		if cg, err := client.ConvertWorkflow(ctx, raw); err == nil && catalog.FirstUUIDClass(cg) == "" {
			g = cg
		}
	}
	if g == nil {
		var err error
		g, err = Convert(raw, info)
		if err != nil {
			return nil, err
		}
	}
	overlaySubgraphPromoted(raw, g)
	return g, nil
}

func Convert(raw json.RawMessage, info map[string]comfy.NodeDef) (map[string]any, error) {
	if IsUIWorkflow(raw) {
		expanded, err := expandUIWorkflow(raw)
		if err != nil {
			return nil, err
		}
		raw = expanded
	} else if g, err := catalog.UnwrapRaw(raw); err == nil {
		if n := catalog.FirstUUIDClass(g); n != "" {
			return nil, fmt.Errorf("unexpanded subgraph node %s (UUID class_type); re-import the UI workflow from ComfyUI", n)
		}
		return g, nil
	}
	var ui struct {
		Nodes  []uiNode       `json:"nodes"`
		Links  [][]any        `json:"links"`
		Prompt map[string]any `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &ui); err != nil {
		return nil, err
	}
	if ui.Prompt != nil && leftoverUUIDNode(uiNodesAny(ui.Nodes)) == "" {
		if g, err := catalog.UnwrapRaw(mustJSON(ui.Prompt)); err == nil && catalog.FirstUUIDClass(g) == "" {
			return g, nil
		}
	}
	if len(ui.Nodes) == 0 {
		return nil, fmt.Errorf("not a ComfyUI workflow")
	}
	links := map[int][]any{}
	for _, l := range ui.Links {
		if len(l) < 5 {
			continue
		}
		id := int(asFloat(l[0]))
		links[id] = l
	}
	out := map[string]any{}
	for _, n := range ui.Nodes {
		if n.Mode == 2 { // Never
			continue
		}
		if n.Type == "Note" || n.Type == "MarkdownNote" || n.Type == "Reroute" {
			continue
		}
		if catalog.IsUUIDClass(n.Type) {
			return nil, fmt.Errorf("unexpanded subgraph node %s (class_type %s)", NodeIDString(n.ID), n.Type)
		}
		nid := NodeIDString(n.ID)
		inputs := map[string]any{}
		linked := map[string]bool{}
		for _, inp := range n.Inputs {
			if inp.Link == nil {
				continue
			}
			l, ok := links[*inp.Link]
			if !ok || len(l) < 5 {
				continue
			}
			from := NodeIDString(l[1])
			slot := int(asFloat(l[2]))
			inputs[inp.Name] = []any{from, slot}
			linked[inp.Name] = true
		}
		applyWidgets(inputs, linked, n, info)
		out[nid] = map[string]any{
			"class_type": n.Type,
			"inputs":     inputs,
			"_meta":      map[string]any{"title": n.Title},
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty graph after convert")
	}
	return out, nil
}

func IsUIWorkflow(raw json.RawMessage) bool {
	var probe struct {
		Nodes json.RawMessage `json:"nodes"`
		Links json.RawMessage `json:"links"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	return len(probe.Nodes) > 2 && string(probe.Nodes) != "null" && string(probe.Nodes) != "[]"
}

func uiNodesAny(nodes []uiNode) any {
	arr := make([]any, 0, len(nodes))
	for _, n := range nodes {
		arr = append(arr, map[string]any{"id": n.ID, "type": n.Type})
	}
	return arr
}

type uiNode struct {
	ID      any             `json:"id"`
	Type    string          `json:"type"`
	Mode    int             `json:"mode"`
	Title   string          `json:"title"`
	Inputs  []uiInput       `json:"inputs"`
	Widgets json.RawMessage `json:"widgets_values"`
	Named   json.RawMessage `json:"widgets_values_named"`
}

type uiInput struct {
	Name   string `json:"name"`
	Link   *int   `json:"link"`
	Widget *struct {
		Name string `json:"name"`
	} `json:"widget"`
}

func applyWidgets(inputs map[string]any, linked map[string]bool, n uiNode, info map[string]comfy.NodeDef) {
	var named map[string]any
	if len(n.Named) > 0 && json.Unmarshal(n.Named, &named) == nil {
		for k, v := range named {
			if linked[k] {
				continue
			}
			inputs[k] = v
		}
	}
	if len(n.Widgets) == 0 || string(n.Widgets) == "null" {
		return
	}
	var asMap map[string]any
	if json.Unmarshal(n.Widgets, &asMap) == nil && asMap != nil {
		for k, v := range asMap {
			if linked[k] {
				continue
			}
			inputs[k] = v
		}
		return
	}
	var arr []any
	if json.Unmarshal(n.Widgets, &arr) != nil {
		return
	}
	def, ok := info[n.Type]
	names := WidgetNames(def)
	if !ok || len(names) == 0 {
		names = widgetNamesFromNode(n)
	}
	wi := 0
	for _, name := range names {
		for wi < len(arr) && IsControlWidget(arr[wi]) {
			wi++
		}
		if wi >= len(arr) {
			break
		}
		val := arr[wi]
		wi++
		if !linked[name] {
			if _, exists := inputs[name]; !exists {
				inputs[name] = val
			}
		}
		spec := widgetSpec(def, name)
		key, _ := val.(string)
		if s, ok := inputs[name].(string); ok && s != "" {
			key = s
		}
		for _, child := range dynamicChildNames(spec, key) {
			for wi < len(arr) && IsControlWidget(arr[wi]) {
				wi++
			}
			if wi >= len(arr) {
				break
			}
			full := name + "." + child
			if !linked[full] {
				inputs[full] = arr[wi]
			}
			wi++
		}
	}
}

func widgetSpec(def comfy.NodeDef, name string) any {
	for _, group := range []string{"required", "optional"} {
		if def.Input[group] == nil {
			continue
		}
		if spec, ok := def.Input[group][name]; ok {
			return spec
		}
	}
	return nil
}

// FieldSpec is the object_info spec for a widget, including a dynamic-combo
// child addressed as parent.child (sampling_mode.temperature).
func FieldSpec(def comfy.NodeDef, name string) any {
	if spec := widgetSpec(def, name); isWidgetSpec(spec) {
		return spec
	}
	parent, child, ok := strings.Cut(name, ".")
	if !ok || child == "" {
		return nil
	}
	parentSpec := widgetSpec(def, parent)
	if !isWidgetSpec(parentSpec) {
		return nil
	}
	if spec := dynamicChildSpecAny(parentSpec, child); isWidgetSpec(spec) {
		return spec
	}
	return nil
}

func dynamicChildSpecAny(spec any, child string) any {
	arr, ok := spec.([]any)
	if !ok || len(arr) < 2 {
		return nil
	}
	meta, _ := arr[1].(map[string]any)
	options, _ := meta["options"].([]any)
	for _, opt := range options {
		om, _ := opt.(map[string]any)
		key, _ := om["key"].(string)
		if key == "" {
			continue
		}
		if s := dynamicChildSpec(spec, key, child); isWidgetSpec(s) {
			return s
		}
	}
	return nil
}

// ValuesFromWidgets reads a positional widgets_values list in definition order.
// Dynamic-combo children use the same slots as the canvas.
func ValuesFromWidgets(arr []any, def comfy.NodeDef) map[string]any {
	inputs := map[string]any{}
	if len(arr) == 0 {
		return inputs
	}
	applyWidgets(inputs, map[string]bool{}, uiNode{Widgets: mustJSON(arr)}, map[string]comfy.NodeDef{"": def})
	return inputs
}

func dynamicChildNames(spec any, key string) []string {
	arr, ok := spec.([]any)
	if !ok || len(arr) < 2 || key == "" {
		return nil
	}
	meta, ok := arr[1].(map[string]any)
	if !ok {
		return nil
	}
	if ord, ok := meta["_oc_order"].(map[string]any); ok {
		if raw, ok := ord[key].([]any); ok {
			var names []string
			for _, n := range raw {
				if s, ok := n.(string); ok && s != "" {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				return names
			}
		}
	}
	return nil
}

func WidgetNames(def comfy.NodeDef) []string {
	var names []string
	for _, group := range []string{"required", "optional"} {
		order := def.InputOrder[group]
		defs := def.Input[group]
		if len(order) == 0 && defs != nil {
			for k := range defs {
				order = append(order, k)
			}
		}
		for _, name := range order {
			spec, ok := defs[name]
			if !ok {
				continue
			}
			if isWidgetSpec(spec) {
				names = append(names, name)
			}
		}
	}
	return names
}

func widgetNamesFromNode(n uiNode) []string {
	var names []string
	for _, inp := range n.Inputs {
		if inp.Widget != nil {
			name := inp.Widget.Name
			if name == "" {
				name = inp.Name
			}
			names = append(names, name)
		}
	}
	return names
}

func isWidgetSpec(spec any) bool {
	arr, ok := spec.([]any)
	if !ok || len(arr) == 0 {
		return false
	}
	switch t := arr[0].(type) {
	case []any:
		return true // combo options list
	case string:
		u := strings.ToUpper(t)
		switch u {
		case "INT", "FLOAT", "STRING", "BOOLEAN", "COMBO", "NUMBER":
			return true
		default:
			// COMFY_DYNAMICCOMBO_V3 and later combo widgets.
			return strings.Contains(u, "COMBO")
		}
	default:
		return false
	}
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
