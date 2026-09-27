package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type PromptResult struct {
	RequestedID string
	ReturnedID  string
}

func (c *Client) QueuePrompt(ctx context.Context, graph map[string]any, promptID string) (PromptResult, error) {
	body := map[string]any{
		"prompt":    graph,
		"client_id": c.clientID,
	}
	if promptID != "" {
		body["prompt_id"] = promptID
	}
	if len(c.extraData) > 0 {
		body["extra_data"] = c.extraData
	}
	b, err := json.Marshal(body)
	if err != nil {
		return PromptResult{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/prompt", bytes.NewReader(b), "application/json")
	if err != nil {
		return PromptResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return PromptResult{}, fmt.Errorf("prompt %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		PromptID string `json:"prompt_id"`
	}
	_ = json.Unmarshal(raw, &out)
	ret := out.PromptID
	if ret == "" {
		ret = promptID
	}
	return PromptResult{RequestedID: promptID, ReturnedID: ret}, nil
}

func (c *Client) History(ctx context.Context, promptID string) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/history/"+url.PathEscape(promptID), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("history %d", resp.StatusCode)
	}
	var wrap map[string]any
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	if inner, ok := wrap[promptID].(map[string]any); ok {
		return inner, nil
	}
	return wrap, nil
}

func (c *Client) PollInterval() time.Duration {
	if c.poll <= 0 {
		return 2 * time.Second
	}
	return c.poll
}
