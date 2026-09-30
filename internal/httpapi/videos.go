package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/javded-itres/open-comfy/internal/engine"
	"github.com/javded-itres/open-comfy/internal/ids"
	"github.com/javded-itres/open-comfy/internal/jobs"
	"github.com/javded-itres/open-comfy/internal/workflow"
)

func (s *Server) createVideo(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	req, wait, err := s.parseVideoReq(r)
	if err != nil {
		mapErr(w, err)
		return
	}
	m, err := s.Cat.Resolve(req.Model, "video")
	if err != nil {
		writeError(w, 400, "invalid_request_error", "model_not_found", err.Error(), "model")
		return
	}
	if !s.allowModel(w, p, m) {
		return
	}
	if _, err := workflow.BuildValues(s.Cat, m, req); err != nil {
		mapErr(w, err)
		return
	}
	if s.Jobs.CountActive() >= s.Cfg.Jobs.MaxActive {
		w.Header().Set("Retry-After", "30")
		writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "too many active video jobs", "")
		return
	}
	now := time.Now()
	id := ids.New("video_")
	j := &jobs.Job{
		ID:        id,
		Object:    "video",
		KeyHash:   p.Key.Hash,
		KeyPrefix: p.Key.Prefix,
		Model:     m.ID,
		Status:    jobs.Queued,
		Progress:  0,
		Prompt:    req.Prompt,
		Params:    map[string]any{"seconds": req.Seconds, "size": req.Size},
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(s.Cfg.JobTTL()).Unix(),
	}
	blobs := req.ImageBlobs()
	if len(blobs) > 0 {
		_ = os.MkdirAll(s.Cfg.Jobs.Dir, 0o700)
		for i, b := range blobs {
			ip := filepath.Join(s.Cfg.Jobs.Dir, fmt.Sprintf("%s.input.%d", id, i))
			if err := os.WriteFile(ip, b, 0o600); err == nil {
				j.InputPaths = append(j.InputPaths, ip)
				if j.InputPath == "" {
					j.InputPath = ip
				}
			}
		}
	}
	if err := s.Jobs.Put(j); err != nil {
		writeError(w, 500, "api_error", "internal_error", err.Error(), "")
		return
	}
	s.kick(id)

	if wait {
		capWait := time.Duration(s.Cfg.HTTP.MaxWaitS) * time.Second
		if s.Cfg.HTTP.AllowLongWait {
			capWait = time.Duration(m.TimeoutS) * time.Second
		}
		deadline := time.Now().Add(capWait)
		for time.Now().Before(deadline) {
			got, err := s.Jobs.Get(id)
			if err == nil && (got.Status == jobs.Completed || got.Status == jobs.Failed || got.Status == jobs.Cancelled) {
				s.writeVideo(w, r, got)
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		got, _ := s.Jobs.Get(id)
		if got != nil {
			s.writeVideo(w, r, got)
			return
		}
	}
	s.writeVideo(w, r, j)
}

func (s *Server) parseVideoReq(r *http.Request) (workflow.Request, bool, error) {
	wait := r.URL.Query().Get("wait") == "true"
	ct := r.Header.Get("Content-Type")
	req := workflow.Request{Extra: map[string]any{}}
	if strings.HasPrefix(ct, "multipart/") {
		if err := r.ParseMultipartForm(s.Cfg.HTTP.MaxBodyBytes); err != nil {
			return req, wait, err
		}
		req.Model = r.FormValue("model")
		req.Prompt = workflow.CleanChatPrompt(r.FormValue("prompt"))
		if v := r.FormValue("seconds"); v != "" {
			req.Seconds = v
		}
		if v := r.FormValue("duration"); v != "" {
			req.Duration = v
		}
		req.Size = r.FormValue("size")
		if r.MultipartForm != nil {
			for _, key := range []string{"input_reference", "input_image", "input_images"} {
				for _, fh := range r.MultipartForm.File[key] {
					f, err := fh.Open()
					if err != nil {
						continue
					}
					b, _ := io.ReadAll(f)
					f.Close()
					if len(b) > 0 {
						req.InputImages = append(req.InputImages, b)
						if len(req.InputImage) == 0 {
							req.InputImage = b
						}
						req.HasInputImage = true
					}
				}
			}
		}
		return req, wait, nil
	}
	body, err := io.ReadAll(s.maxBody(r))
	if err != nil {
		return req, wait, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return req, wait, &workflow.Error{Code: "invalid_value", Message: "invalid json"}
	}
	if v, _ := raw["wait"].(bool); v {
		wait = true
	}
	req.Model, _ = raw["model"].(string)
	req.Prompt, _ = raw["prompt"].(string)
	req.Prompt = workflow.CleanChatPrompt(req.Prompt)
	req.NegativePrompt, _ = raw["negative_prompt"].(string)
	req.Size, _ = raw["size"].(string)
	req.Quality, _ = raw["quality"].(string)
	req.Seconds = raw["seconds"]
	req.Duration = raw["duration"]
	if v, ok := asInt64Ptr(raw["seed"]); ok {
		req.Seed = v
	}
	if v, ok := asIntPtr(raw["fps"]); ok {
		req.FPS = v
	}
	if img, ok := raw["input_image"]; ok {
		if err := applyInputReference(&req, img); err != nil {
			return req, wait, err
		}
	}
	if imgs, ok := raw["input_images"]; ok {
		if err := applyInputReference(&req, imgs); err != nil {
			return req, wait, err
		}
	}
	if ir, ok := raw["input_reference"]; ok {
		if err := applyInputReference(&req, ir); err != nil {
			return req, wait, err
		}
	}
	if ir, ok := raw["input_references"]; ok {
		if err := applyInputReference(&req, ir); err != nil {
			return req, wait, err
		}
	}
	for k, v := range raw {
		if workflow.IgnoreOpenAI(k) {
			continue
		}
		req.Extra[k] = v
	}
	return req, wait, nil
}

func (s *Server) listVideos(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	list, _ := s.Jobs.ListByHash(p.Key.Hash)
	var data []map[string]any
	for _, j := range list {
		if j.Object != "" && j.Object != "video" {
			continue
		}
		data = append(data, s.videoObj(r.Context(), j))
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *Server) getVideo(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	j, err := s.Jobs.Get(r.PathValue("id"))
	if err != nil || !sameHash(j.KeyHash, p.Key.Hash) {
		writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
		return
	}
	s.writeVideo(w, r, j)
}

func (s *Server) videoContent(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	j, err := s.Jobs.Get(r.PathValue("id"))
	if err != nil || !sameHash(j.KeyHash, p.Key.Hash) {
		writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
		return
	}
	if j.Status != jobs.Completed || j.FileID == "" {
		writeError(w, 400, "invalid_request_error", "invalid_value", "video not ready", "")
		return
	}
	meta, b, err := s.Files.Get(j.FileID)
	if err != nil {
		writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
		return
	}
	mime := j.MIME
	if mime == "" && meta != nil {
		mime = meta.MIME
	}
	if mime == "" {
		mime = "video/mp4"
	}
	w.Header().Set("Content-Type", mime)
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

func (s *Server) deleteVideo(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	j, err := s.Jobs.Update(r.PathValue("id"), func(j *jobs.Job) error {
		if !sameHash(j.KeyHash, p.Key.Hash) {
			return os.ErrNotExist
		}
		j.Status = jobs.Cancelled
		now := time.Now().Unix()
		j.CompletedAt = &now
		return nil
	})
	if err != nil {
		writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
		return
	}
	if j.FileID != "" {
		s.Files.Unlink(j.FileID)
	}
	s.writeVideo(w, r, j)
}

func (s *Server) writeVideo(w http.ResponseWriter, r *http.Request, j *jobs.Job) {
	writeJSON(w, 200, s.videoObj(r.Context(), j))
}

func (s *Server) videoObj(ctx context.Context, j *jobs.Job) map[string]any {
	st := string(j.Status)
	if j.ExpiresAt > 0 && time.Now().Unix() > j.ExpiresAt && j.Status == jobs.Completed {
		st = "expired"
	}
	out := map[string]any{
		"id":           j.ID,
		"object":       "video",
		"model":        j.Model,
		"status":       st,
		"progress":     j.Progress,
		"created_at":   j.CreatedAt,
		"completed_at": j.CompletedAt,
		"expires_at":   j.ExpiresAt,
		"error":        j.Error,
		"prompt":       nil,
	}
	if s.Cfg.Jobs.StorePrompt {
		out["prompt"] = j.Prompt
	}
	if sec, ok := j.Params["seconds"]; ok && sec != nil {
		out["seconds"] = workflow.SecondsString(sec)
	}
	if sz, ok := j.Params["size"].(string); ok {
		out["size"] = sz
	}
	if j.Status == jobs.Completed && j.FileID != "" {
		if u, err := s.Files.SignURL(j.FileID); err == nil {
			out["url"] = u
			out["unsigned_urls"] = []string{u}
		}
	}
	if origin := s.origin(); origin != "" {
		out["polling_url"] = origin + "/v1/videos/" + j.ID
	}
	out["queue_ahead"] = s.queueAhead(ctx, j)
	return out
}

func (s *Server) videoLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.videoCh:
			j, err := s.Jobs.Get(id)
			if err != nil {
				continue
			}
			if j.Object == "image" {
				s.runImage(ctx, id)
				continue
			}
			s.runVideo(ctx, id)
		}
	}
}

func (s *Server) runVideo(ctx context.Context, id string) {
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
	req := workflow.Request{
		Model:  m.ID,
		Prompt: j.Prompt,
		Extra:  map[string]any{},
	}
	if sec, ok := j.Params["seconds"]; ok {
		req.Seconds = sec
	}
	paths := j.InputPaths
	if len(paths) == 0 && j.InputPath != "" {
		paths = []string{j.InputPath}
	}
	for _, ip := range paths {
		if b, err := os.ReadFile(ip); err == nil && len(b) > 0 {
			req.InputImages = append(req.InputImages, b)
			req.HasInputImage = true
		}
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
	res, pr, err := engine.Run(tctx, s.Comfy, s.Cat, m, req, id, func(pct, pos int, promptID string) {
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
	if _, err := s.Files.SignURL(meta.ID); err != nil {
		_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
			j.Status = jobs.Failed
			j.Error = &jobs.JobError{Message: "cannot emit url: " + err.Error(), Code: "internal_error"}
			return nil
		})
		return
	}
	_, _ = s.Jobs.Update(id, func(j *jobs.Job) error {
		j.Status = jobs.Completed
		j.Progress = 100
		j.FileID = meta.ID
		j.MIME = res.MIME
		j.RequestedPromptID = pr.RequestedID
		j.ComfyPromptID = pr.ReturnedID
		now := time.Now().Unix()
		j.CompletedAt = &now
		return nil
	})
}

func (s *Server) cleanerLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Jobs.CleanExpired(time.Now(), s.Files.Unlink)
		}
	}
}
