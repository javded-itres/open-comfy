package httpapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/engine"
	"github.com/javded-itres/open-comfy/internal/ids"
	"github.com/javded-itres/open-comfy/internal/jobs"
	"github.com/javded-itres/open-comfy/internal/workflow"
)

func (s *Server) enqueueImage(w http.ResponseWriter, r *http.Request, p principal, m *catalog.Model, req workflow.Request) {
	if s.Jobs.CountActive() >= s.Cfg.Jobs.MaxActive {
		w.Header().Set("Retry-After", "10")
		writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "too many active jobs", "")
		return
	}
	now := time.Now()
	id := ids.New("img_")
	j := &jobs.Job{
		ID:        id,
		Object:    "image",
		KeyHash:   p.Key.Hash,
		KeyPrefix: p.Key.Prefix,
		Model:     m.ID,
		Status:    jobs.Queued,
		Prompt:    req.Prompt,
		Params:    imageParams(req),
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(s.Cfg.JobTTL()).Unix(),
	}
	if err := s.storeInputBlobs(j, req.ImageBlobs()); err != nil {
		writeError(w, 500, "api_error", "internal_error", err.Error(), "")
		return
	}
	if err := s.Jobs.Put(j); err != nil {
		writeError(w, 500, "api_error", "internal_error", err.Error(), "")
		return
	}
	s.kick(id)
	writeJSON(w, 200, s.imageObj(r.Context(), j))
}

func imageParams(req workflow.Request) map[string]any {
	p := map[string]any{
		"response_format": req.ResponseFormat,
		"n":               req.N,
		"size":            req.Size,
		"quality":         req.Quality,
		"negative_prompt": req.NegativePrompt,
		"resolution":      req.Resolution,
		"aspect_ratio":    req.AspectRatio,
	}
	if req.Seed != nil {
		p["seed"] = *req.Seed
	}
	if len(req.Extra) > 0 {
		p["extra"] = req.Extra
	}
	return p
}

func (s *Server) storeInputBlobs(j *jobs.Job, blobs [][]byte) error {
	if len(blobs) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.Cfg.Jobs.Dir, 0o700); err != nil {
		return err
	}
	for i, b := range blobs {
		ip := filepath.Join(s.Cfg.Jobs.Dir, fmt.Sprintf("%s.input.%d", j.ID, i))
		if err := os.WriteFile(ip, b, 0o600); err != nil {
			return err
		}
		j.InputPaths = append(j.InputPaths, ip)
		if j.InputPath == "" {
			j.InputPath = ip
		}
	}
	return nil
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	j, err := s.Jobs.Get(r.PathValue("id"))
	if err != nil || j.Object != "image" || !sameHash(j.KeyHash, p.Key.Hash) {
		writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
		return
	}
	writeJSON(w, 200, s.imageObj(r.Context(), j))
}

func (s *Server) imageObj(ctx context.Context, j *jobs.Job) map[string]any {
	out := map[string]any{
		"id":          j.ID,
		"object":      "image",
		"model":       j.Model,
		"status":      string(j.Status),
		"progress":    j.Progress,
		"created_at":  j.CreatedAt,
		"queue_ahead": s.queueAhead(ctx, j),
		"error":       j.Error,
	}
	if origin := s.origin(); origin != "" {
		out["polling_url"] = origin + "/v1/images/" + j.ID
	}
	if j.Status != jobs.Completed {
		return out
	}
	format, _ := j.Params["response_format"].(string)
	ids := j.FileIDs
	if len(ids) == 0 && j.FileID != "" {
		ids = []string{j.FileID}
	}
	var data []map[string]any
	for _, id := range ids {
		if format == "url" {
			u, err := s.Files.SignURL(id)
			if err != nil {
				continue
			}
			data = append(data, map[string]any{"url": u, "revised_prompt": nil})
			continue
		}
		_, b, err := s.Files.Get(id)
		if err != nil {
			continue
		}
		data = append(data, map[string]any{
			"b64_json":       base64.StdEncoding.EncodeToString(b),
			"revised_prompt": nil,
		})
	}
	out["created"] = j.CreatedAt
	out["data"] = data
	return out
}

func (s *Server) runImage(ctx context.Context, id string) {
	j, err := s.Jobs.Get(id)
	if err != nil || j.Status == jobs.Cancelled {
		return
	}
	m := s.Cat.Get(j.Model)
	if m == nil {
		_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
			j.Status = jobs.Failed
			j.Error = &jobs.JobError{Message: "unknown model", Code: "model_not_found"}
			return nil
		})
		return
	}
	req := imageRequest(j)
	for _, ip := range inputPaths(j) {
		if b, err := os.ReadFile(ip); err == nil && len(b) > 0 {
			req.InputImages = append(req.InputImages, b)
			req.HasInputImage = true
		}
	}
	n := req.N
	if n <= 0 {
		n = 1
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutS+15)*time.Second)
	defer cancel()
	rel, err := s.acquireSlot(tctx, m.ID, m.MaxConcurrent)
	if err != nil {
		_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
			j.Status = jobs.Failed
			j.Error = &jobs.JobError{Message: "timeout waiting for gpu", Code: "generation_timeout"}
			now := time.Now().Unix()
			j.CompletedAt = &now
			return nil
		})
		return
	}
	defer rel()
	_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
		j.Status = jobs.InProgress
		j.Progress = 10
		return nil
	})

	var fileIDs []string
	var mime string
	baseSeed := req.Seed
	for i := 0; i < n; i++ {
		one := req
		if baseSeed != nil {
			seed := *baseSeed + int64(i)
			one.Seed = &seed
		} else {
			one.Seed = nil
		}
		res, pr, err := engine.Run(tctx, s.Comfy, s.Cat, m, one, ids.New("img_"), func(pct, pos int, promptID string) {
			_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
				if j.Status == jobs.Cancelled {
					return nil
				}
				j.Status = jobs.InProgress
				j.Progress = pct
				j.QueuePosition = pos
				if promptID != "" {
					j.ComfyPromptID = promptID
				}
				return nil
			})
		})
		if err != nil {
			_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
				if j.Status == jobs.Cancelled {
					return nil
				}
				j.Status = jobs.Failed
				code := "internal_error"
				if tctx.Err() != nil {
					code = "generation_timeout"
				}
				j.Error = &jobs.JobError{Message: err.Error(), Code: code}
				now := time.Now().Unix()
				j.CompletedAt = &now
				return nil
			})
			return
		}
		meta, err := s.Files.Put(j.KeyHash, res.MIME, res.Bytes)
		if err != nil {
			_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
				j.Status = jobs.Failed
				j.Error = &jobs.JobError{Message: err.Error(), Code: "storage_full"}
				return nil
			})
			return
		}
		_ = pr
		fileIDs = append(fileIDs, meta.ID)
		mime = res.MIME
	}
	_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
		j.Status = jobs.Completed
		j.Progress = 100
		j.FileIDs = fileIDs
		if len(fileIDs) > 0 {
			j.FileID = fileIDs[0]
		}
		j.MIME = mime
		now := time.Now().Unix()
		j.CompletedAt = &now
		return nil
	})
}

func imageRequest(j *jobs.Job) workflow.Request {
	req := workflow.Request{Model: j.Model, Prompt: j.Prompt, Extra: map[string]any{}}
	if j.Params == nil {
		return req
	}
	req.NegativePrompt, _ = j.Params["negative_prompt"].(string)
	req.Size, _ = j.Params["size"].(string)
	req.Quality, _ = j.Params["quality"].(string)
	req.ResponseFormat, _ = j.Params["response_format"].(string)
	req.Resolution, _ = j.Params["resolution"].(string)
	req.AspectRatio, _ = j.Params["aspect_ratio"].(string)
	switch n := j.Params["n"].(type) {
	case float64:
		req.N = int(n)
	case int:
		req.N = n
	}
	req.Seed = seedParam(j.Params["seed"])
	if extra, ok := j.Params["extra"].(map[string]any); ok {
		req.Extra = extra
	}
	return req
}

func seedParam(v any) *int64 {
	switch t := v.(type) {
	case float64:
		s := int64(t)
		return &s
	case int64:
		s := t
		return &s
	case int:
		s := int64(t)
		return &s
	default:
		return nil
	}
}

func inputPaths(j *jobs.Job) []string {
	if len(j.InputPaths) > 0 {
		return j.InputPaths
	}
	if j.InputPath != "" {
		return []string{j.InputPath}
	}
	return nil
}
