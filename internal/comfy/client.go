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

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

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

func str(v any) string {
	s, _ := v.(string)
	return s
}
