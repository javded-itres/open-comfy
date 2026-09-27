package catalog

import "encoding/json"

func EnsureSaver(graph map[string]any, modality, source string) (map[string]any, string) {
	if graph == nil {
		return graph, source
	}
	var existing string
	for id, v := range graph {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		if isSaveLike(ct) {
			existing = id
			break
		}
	}
	if existing != "" {
		if source == "" {
			return graph, existing
		}
		return graph, source
	}
	if source == "" {
		return graph, source
	}
	if _, ok := graph[source]; !ok {
		return graph, source
	}
	g, err := deepcopyGraph(graph)
	if err != nil {
		return graph, source
	}
	id := "oc_save"
	for g[id] != nil {
		id += "x"
	}
	class, field := "SaveImage", "images"
	if modality == "video" {
		class, field = "SaveVideo", "images"
	}
	g[id] = map[string]any{
		"class_type": class,
		"inputs": map[string]any{
			"filename_prefix": "opencomfy",
			field:             []any{source, 0},
		},
	}
	return g, id
}

func deepcopyGraph(graph map[string]any) (map[string]any, error) {
	b, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
