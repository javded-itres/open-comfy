package comfy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base           string
	authHeader     string
	extraData      map[string]any
	http           *http.Client
	poll           time.Duration
	clientID       string
	allowInterrupt bool
}

func New(base, authHeader, clientID string, extra map[string]any, reqTimeout, poll time.Duration, allowInterrupt bool) *Client {
	if extra == nil {
		extra = map[string]any{}
	}
	return &Client{
		base:           strings.TrimRight(base, "/"),
		authHeader:     authHeader,
		extraData:      extra,
		http:           &http.Client{Timeout: reqTimeout},
		poll:           poll,
		clientID:       clientID,
		allowInterrupt: allowInterrupt,
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	return c.http.Do(req)
}

func (c *Client) SystemStats(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/system_stats", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("system_stats %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetJSON(ctx context.Context, path string, dest any) error {
	return c.getJSON(ctx, path, dest)
}

func (c *Client) getJSON(ctx context.Context, path string, dest any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %d: %s", path, resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, dest)
}

// ConvertWorkflow asks ComfyUI to turn a UI-format graph into the same API
// prompt that File → Export Workflow (API) / graphToPrompt produces.
// Requires a convert endpoint (custom node POST /workflow/convert).
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
