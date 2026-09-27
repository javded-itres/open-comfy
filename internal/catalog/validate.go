package catalog

import (
	"fmt"
	"strings"
)

func ValidateModel(m *Model, dir string) error {
	g, err := LoadGraph(dir, m.Workflow)
	if err != nil {
		return err
	}
	saves := 0
	for id, v := range g {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := node["class_type"].(string)
		if isSaveLike(ct) {
			saves++
			_ = id
		}
	}
	if m.OutputNode != "" {
		if _, ok := g[m.OutputNode]; !ok {
			return fmt.Errorf("output_node %q missing", m.OutputNode)
		}
	} else if saves == 0 {
		return fmt.Errorf("no Save*/VHS* output node")
	}
	if saves > 1 && m.OutputNode == "" {
		return fmt.Errorf("multiple save nodes require output_node")
	}
	for _, p := range m.Parameters {
		for _, mt := range p.MapsTo {
			node, ok := g[mt.Node].(map[string]any)
			if !ok {
				return fmt.Errorf("param %s maps_to node %q missing", p.Name, mt.Node)
			}
			inputs, ok := node["inputs"].(map[string]any)
			if !ok {
				return fmt.Errorf("param %s node %q inputs not a map", p.Name, mt.Node)
			}
			if _, ok := inputs[mt.Field]; !ok {
				return fmt.Errorf("param %s node %q missing field %q", p.Name, mt.Node, mt.Field)
			}
			if err := checkPath(inputs, mt.Field, mt.Path); err != nil {
				return fmt.Errorf("param %s: %w", p.Name, err)
			}
		}
	}
	return nil
}

func checkPath(inputs map[string]any, field string, path []string) error {
	if len(path) == 0 {
		return nil
	}
	cur, ok := inputs[field]
	if !ok {
		return fmt.Errorf("field %s missing", field)
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return fmt.Errorf("field %s is not a map for path", field)
	}
	for i, p := range path {
		if i == len(path)-1 {
			if _, ok := m[p]; !ok {
				return fmt.Errorf("path %s missing under %s", p, field)
			}
			return nil
		}
		next, ok := m[p]
		if !ok {
			return fmt.Errorf("path %s missing under %s", p, field)
		}
		m, ok = next.(map[string]any)
		if !ok {
			return fmt.Errorf("path %s is not a map", p)
		}
	}
	return nil
}

func isSaveLike(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "save") || strings.HasPrefix(ct, "vhs_")
}

// EnsureSaver attaches SaveImage/SaveVideo when the graph only has a UI
// output_node (e.g. FluxKleinOneNode). ComfyUI API skips nodes that do not
