package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
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

type Artifact struct {
	Filename  string
	Subfolder string
	Type      string
	Format    string
	Kind      string // video | image | gif
}

func PickArtifact(history map[string]any, outputNode, modality, outputMIME string) (Artifact, error) {
	outputs, _ := history["outputs"].(map[string]any)
	if outputs == nil {
		if o, ok := history[outputNode].(map[string]any); ok {
			outputs = map[string]any{outputNode: o}
		}
	}
	var nodes []string
	if outputNode != "" {
		nodes = []string{outputNode}
	} else {
		for k := range outputs {
			nodes = append(nodes, k)
		}
	}
	var cands []Artifact
	for _, n := range nodes {
		node, _ := outputs[n].(map[string]any)
		if node == nil {
			continue
		}
		animated := false
		switch t := node["animated"].(type) {
		case []any:
			if len(t) > 0 {
				animated, _ = t[0].(bool)
			}
		case bool:
			animated = t
		}
		for _, key := range []string{"videos", "gifs", "images", "video"} {
			for _, m := range asMaps(node[key]) {
				a := Artifact{
					Filename:  str(m["filename"]),
					Subfolder: str(m["subfolder"]),
					Type:      str(m["type"]),
					Format:    str(m["format"]),
				}
				if a.Type == "" {
					a.Type = "output"
				}
				a.Kind = classify(a)
				if animated && a.Kind == "image" && filepath.Ext(a.Filename) == ".mp4" {
					a.Kind = "video"
				}
				if animated && a.Kind == "image" && strings.Contains(strings.ToLower(a.Filename), ".mp4") {
					a.Kind = "video"
				}
				cands = append(cands, a)
			}
		}
	}
	wantVideo := modality == "video" || strings.HasPrefix(outputMIME, "video/")
	if wantVideo {
		for _, a := range cands {
			if a.Kind == "video" {
				return a, nil
			}
		}
		return Artifact{}, fmt.Errorf("no_output")
	}
	for _, a := range cands {
		if a.Kind == "image" {
			return a, nil
		}
	}
	for _, a := range cands {
		if a.Kind == "gif" {
			return a, nil
		}
	}
	return Artifact{}, fmt.Errorf("no_output")
}

func asMaps(v any) []map[string]any {
	switch t := v.(type) {
	case []any:
		var out []map[string]any
		for _, it := range t {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{t}
	default:
		return nil
	}
}

func classify(a Artifact) string {
	ext := strings.ToLower(filepath.Ext(a.Filename))
	fmtm := strings.ToLower(a.Format)
	name := strings.ToLower(a.Filename)
	if strings.HasPrefix(fmtm, "video/") || ext == ".mp4" || ext == ".webm" || ext == ".mkv" || ext == ".mov" || ext == ".avi" || strings.Contains(name, ".mp4") {
		return "video"
	}
	if ext == ".gif" {
		return "gif"
	}
	return "image"
}

func (c *Client) View(ctx context.Context, a Artifact) ([]byte, error) {
	q := url.Values{}
	q.Set("filename", a.Filename)
	q.Set("subfolder", a.Subfolder)
	q.Set("type", a.Type)
	resp, err := c.do(ctx, http.MethodGet, "/view?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("view %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

type UploadResult struct {
	Name      string `json:"name"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

func (c *Client) UploadImage(ctx context.Context, filename string, data []byte) (UploadResult, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("image", filename)
	if err != nil {
		return UploadResult{}, err
	}
	if _, err := fw.Write(data); err != nil {
		return UploadResult{}, err
	}
	_ = w.WriteField("overwrite", "true")
	w.Close()
	resp, err := c.do(ctx, http.MethodPost, "/upload/image", &buf, w.FormDataContentType())
	if err != nil {
		return UploadResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return UploadResult{}, fmt.Errorf("upload %d: %s", resp.StatusCode, raw)
	}
	var out UploadResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return UploadResult{}, err
	}
	return out, nil
}

func ComfyImageName(u UploadResult) string {
	if u.Subfolder == "" {
		return u.Name
	}
	return u.Subfolder + "/" + u.Name
}

func (c *Client) PollInterval() time.Duration {
	if c.poll <= 0 {
		return 2 * time.Second
	}
	return c.poll
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
