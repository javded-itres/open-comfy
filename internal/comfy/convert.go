package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (c *Client) ConvertWorkflow(ctx context.Context, raw []byte) (map[string]any, error) {
	var last error
	for _, path := range []string{"/workflow/convert", "/api/workflow/convert"} {
		g, err := c.postConvert(ctx, path, raw)
		if err == nil {
			return g, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("workflow convert unavailable")
	}
	return nil, last
}

func (c *Client) postConvert(ctx context.Context, path string, raw []byte) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(raw), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %d: %s", path, resp.StatusCode, truncate(body, 400))
	}
	return parseAPIPrompt(body)
}

func parseAPIPrompt(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if s, _ := m["error"].(string); s != "" {
		return nil, fmt.Errorf("comfy convert: %s", s)
	}
	if ok, _ := m["success"].(bool); ok == false && m["success"] != nil {
		if s, _ := m["error"].(string); s != "" {
			return nil, fmt.Errorf("comfy convert: %s", s)
		}
		return nil, fmt.Errorf("comfy convert failed")
	}
	if g, err := unwrapRawGraph(raw); err == nil {
		if n := firstUUID(g); n != "" {
			return nil, fmt.Errorf("unexpanded subgraph node %s", n)
		}
		return g, nil
	}
	try := [][]byte{}
	for _, key := range []string{"output", "prompt", "workflow", "data"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		b, err := json.Marshal(v)
		if err == nil {
			try = append(try, b)
		}
		if inner, ok := v.(map[string]any); ok {
			if w, ok := inner["workflow"]; ok {
				if b, err := json.Marshal(w); err == nil {
					try = append(try, b)
				}
			}
			if w, ok := inner["output"]; ok {
				if b, err := json.Marshal(w); err == nil {
					try = append(try, b)
				}
			}
		}
	}
	for _, b := range try {
		g, err := unwrapPromptGraph(b)
		if err == nil {
			return g, nil
		}
	}
	return nil, fmt.Errorf("comfy convert: response is not an API workflow")
}

func unwrapPromptGraph(raw []byte) (map[string]any, error) {
	g, err := unwrapRawGraph(raw)
	if err != nil {
		return nil, err
	}
	if n := firstUUID(g); n != "" {
		return nil, fmt.Errorf("unexpanded subgraph node %s", n)
	}
	return g, nil
}

func unwrapRawGraph(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if _, hasNodes := m["nodes"]; hasNodes {
		if _, hasLinks := m["links"]; hasLinks {
			return nil, fmt.Errorf("UI-format workflow")
		}
	}
	n := 0
	for _, v := range m {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := node["class_type"]; ok {
			n++
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("no class_type nodes")
	}
	return m, nil
}

func firstUUID(g map[string]any) string {
	for id, v := range g {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := node["class_type"].(string)
		if len(ct) == 36 && ct[8] == '-' && ct[13] == '-' && ct[18] == '-' && ct[23] == '-' {
			return id
		}
	}
	return ""
}
