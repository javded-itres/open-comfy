package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) ListUserWorkflows(ctx context.Context) ([]string, error) {
	var raw any
	if err := c.getJSON(ctx, "/userdata?dir=workflows&recurse=true&split=false", &raw); err != nil {
		return nil, err
	}
	var out []string
	switch t := raw.(type) {
	case []any:
		for _, it := range t {
			switch v := it.(type) {
			case string:
				if strings.HasSuffix(strings.ToLower(v), ".json") {
					out = append(out, v)
				}
			case map[string]any:
				p, _ := v["path"].(string)
				if strings.HasSuffix(strings.ToLower(p), ".json") {
					out = append(out, p)
				}
			}
		}
	}
	return out, nil
}

func userdataWorkflowPath(name string) string {
	name = strings.TrimPrefix(name, "/")
	if !strings.HasPrefix(name, "workflows/") {
		name = "workflows/" + name
	}
	return "/userdata/" + url.PathEscape(name)
}

func (c *Client) PutUserWorkflow(ctx context.Context, name string, raw []byte, overwrite bool) error {
	path := userdataWorkflowPath(name)
	if !overwrite {
		path += "?overwrite=false"
	}
	resp, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(raw), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("workflow %s already exists", name)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("userdata write %s: %d %s", name, resp.StatusCode, truncate(body, 300))
	}
	return nil
}

func (c *Client) GetUserWorkflow(ctx context.Context, name string) (json.RawMessage, error) {
	path := userdataWorkflowPath(name)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userdata %s: %d %s", name, resp.StatusCode, raw)
	}
	return raw, nil
}

type NodeDef struct {
	Input      map[string]map[string]any `json:"input"`
	InputOrder map[string][]string       `json:"input_order"`
	OutputNode bool                      `json:"output_node"`
	Name       string                    `json:"name"`
}

func (c *Client) ObjectInfo(ctx context.Context) (map[string]NodeDef, error) {
	var wrap map[string]NodeDef
	if err := c.getJSON(ctx, "/object_info", &wrap); err != nil {
		return nil, err
	}
	return wrap, nil
}
