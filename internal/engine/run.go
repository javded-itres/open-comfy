package engine

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/ids"
	"github.com/javded-itres/open-comfy/internal/workflow"
)

type Result struct {
	Bytes  []byte
	MIME   string
	Width  int
	Height int
}

func DecodeDataURL(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "data:") {
		return nil, fmt.Errorf("not a data url")
	}
	i := strings.Index(s, ",")
	if i < 0 {
		return nil, fmt.Errorf("bad data url")
	}
	meta, data := s[:i], s[i+1:]
	if !strings.Contains(meta, ";base64") {
		return nil, fmt.Errorf("data url must be base64")
	}
	return base64.StdEncoding.DecodeString(data)
}

// Runner is the ComfyUI port engine needs. *comfy.Client implements it.
type Runner interface {
	UploadImage(ctx context.Context, filename string, data []byte) (comfy.UploadResult, error)
	QueuePrompt(ctx context.Context, graph map[string]any, promptID string) (comfy.PromptResult, error)
	PollInterval() time.Duration
	Queue(ctx context.Context) (comfy.Queue, error)
	History(ctx context.Context, promptID string) (map[string]any, error)
	View(ctx context.Context, a comfy.Artifact) ([]byte, error)
}

func Run(ctx context.Context, client Runner, cat *catalog.Catalog, m *catalog.Model, req workflow.Request, promptID string, onProgress func(pct int, pos int)) (Result, comfy.PromptResult, error) {
	graph, err := catalog.LoadGraph(cat.WorkflowsDir, m.Workflow)
	if err != nil {
		return Result{}, comfy.PromptResult{}, err
	}
	if n := catalog.FirstUUIDClass(graph); n != "" {
		return Result{}, comfy.PromptResult{}, fmt.Errorf("unexpanded subgraph node %s (UUID class_type); re-import the UI workflow from ComfyUI", n)
	}
	blobs := req.ImageBlobs()
	if len(blobs) > 0 && len(req.ImageFiles()) == 0 {
		var names []string
		for i, b := range blobs {
			up, err := client.UploadImage(ctx, fmt.Sprintf("input_%d.png", i), b)
			if err != nil {
				return Result{}, comfy.PromptResult{}, fmt.Errorf("upload: %w", err)
			}
			names = append(names, comfy.ComfyImageName(up))
		}
		req.InputNames = names
		if len(names) == 1 {
			req.InputName = names[0]
		}
		req.HasInputImage = true
	}
	values, err := workflow.BuildValues(cat, m, req)
	if err != nil {
		return Result{}, comfy.PromptResult{}, err
	}
	if req.InputName != "" {
		values["input_image"] = req.InputName
	}
	injected, err := workflow.Inject(graph, m, values)
	if err != nil {
		return Result{}, comfy.PromptResult{}, err
	}
	injected, pickNode := catalog.EnsureSaver(injected, m.Modality, m.OutputNode)
	if !ids.IsUUID(promptID) {
		promptID = ids.UUID()
	}
	pr, err := client.QueuePrompt(ctx, injected, promptID)
	if err != nil {
		return Result{}, pr, err
	}
	pollID := pr.ReturnedID
	ticker := time.NewTicker(client.PollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Result{}, pr, ctx.Err()
		case <-ticker.C:
			q, _ := client.Queue(ctx)
			running, pos := q.Position(pollID)
			if !running && pos < 0 {
				running, pos = q.Position(pr.RequestedID)
			}
			if onProgress != nil {
				pct := 10
				if running {
					pct = 50
				}
				if pos > 0 {
					pct = 15
				}
				onProgress(pct, pos)
			}
			h, err := client.History(ctx, pollID)
			if err != nil {
				continue
			}
			if h == nil || len(h) == 0 {
				continue
			}
			done := false
			if st, ok := h["status"].(map[string]any); ok {
				if s, _ := st["status_str"].(string); s == "error" {
					return Result{}, pr, fmt.Errorf("comfy error")
				}
				if s, _ := st["status_str"].(string); s == "success" {
					done = true
				}
				if c, ok := st["completed"].(bool); ok && c {
					done = true
				}
			}
			art, err := comfy.PickArtifact(h, pickNode, m.Modality, m.OutputMIME)
			if err != nil {
				if !done {
					continue
				}
				return Result{}, pr, err
			}
			b, err := client.View(ctx, art)
			if err != nil {
				return Result{}, pr, err
			}
			mime := m.OutputMIME
			if mime == "" {
				if art.Kind == "video" {
					mime = "video/mp4"
				} else {
					mime = "image/png"
				}
			}
			return Result{Bytes: b, MIME: mime}, pr, nil
		}
	}
}
