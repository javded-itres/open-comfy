package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func WorkflowPath(dir, name string) (string, error) {
	base := filepath.Base(name)
	if base != name && !strings.HasPrefix(filepath.Clean(name), filepath.Clean(dir)+string(os.PathSeparator)) {
		// allow only basename
		name = base
	} else {
		name = base
	}
	p := filepath.Join(dir, name)
	if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(dir)+string(os.PathSeparator)) && filepath.Clean(p) != filepath.Clean(dir) {
		return "", fmt.Errorf("workflow path escapes workflows_dir")
	}
	return p, nil
}

func LoadGraph(dir, name string) (map[string]any, error) {
	p, err := WorkflowPath(dir, name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return unwrapGraph(raw)
}

func UnwrapRaw(b []byte) (map[string]any, error) {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return unwrapGraph(raw)
}

func FirstUUIDClass(g map[string]any) string {
	for id, v := range g {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := node["class_type"].(string)
		if IsUUIDClass(ct) {
			if id != "" {
				return id
			}
			return ct
		}
	}
	return ""
}

func IsUUIDClass(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

func unwrapGraph(raw any) (map[string]any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("workflow is not an object")
	}
	if _, hasNodes := m["nodes"]; hasNodes {
		if _, hasLinks := m["links"]; hasLinks {
			return nil, fmt.Errorf("UI-format workflow (nodes/links); export API format")
		}
	}
	if prompt, ok := m["prompt"].(map[string]any); ok {
		m = prompt
	}
	for _, v := range m {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := node["class_type"]; ok {
			return m, nil
		}
	}
	return nil, fmt.Errorf("workflow missing class_type (not API format)")
}
