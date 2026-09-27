package comfy

import (
	"context"
	"encoding/json"
	"net/http"
)

type Queue struct {
	Running []any `json:"queue_running"`
	Pending []any `json:"queue_pending"`
}

func (c *Client) Queue(ctx context.Context) (Queue, error) {
	resp, err := c.do(ctx, http.MethodGet, "/queue", nil, "")
	if err != nil {
		return Queue{}, err
	}
	defer resp.Body.Close()
	var q Queue
	if err := json.NewDecoder(resp.Body).Decode(&q); err != nil {
		return Queue{}, err
	}
	return q, nil
}

// Status is a lightweight snapshot of the ComfyUI queue driven by /queue.
// ComfyUI exposes no dedicated /status endpoint, so in-flight jobs come from
// queue_running and waiting jobs from queue_pending.
func (c *Client) Status(ctx context.Context) (Status, error) {
	q, err := c.Queue(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{
		InFlight: len(q.Running),
		Waiting:  len(q.Pending),
	}, nil
}

// Status is a count of ComfyUI queue activity.
type Status struct {
	InFlight int `json:"in_flight"`
	Waiting  int `json:"waiting"`
}

func ExtractPromptID(item any) string {
	switch t := item.(type) {
	case string:
		return t
	case []any:
		if len(t) >= 2 {
			if s, ok := t[1].(string); ok {
				return s
			}
		}
	case map[string]any:
		if s, ok := t["prompt_id"].(string); ok {
			return s
		}
		if s, ok := t["id"].(string); ok {
			return s
		}
	}
	return ""
}

func (q Queue) Position(promptID string) (running bool, pendingIndex int) {
	for _, it := range q.Running {
		if ExtractPromptID(it) == promptID {
			return true, 0
		}
	}
	for i, it := range q.Pending {
		if ExtractPromptID(it) == promptID {
			return false, i + 1
		}
	}
	return false, -1
}
