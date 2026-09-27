package httpapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/javded-itres/open-comfy/internal/auth"
	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/importwf"
)

//go:embed import_page.html
var importHTML []byte

//go:embed login_page.html
var loginHTML []byte

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	plain := auth.BearerToken(r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"))
	if _, ok := s.Auth.Lookup(plain); !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(401)
		_, _ = w.Write(loginHTML)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(importHTML)
}

func (s *Server) analyzeComfyWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string          `json:"name"`
		Workflow json.RawMessage `json:"workflow"`
	}
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	allow := importwf.NormalizeAllowlist(s.Cfg.ComfyUI.NodeAllowlist)
	if len(req.Workflow) > 0 {
		info, err := s.Comfy.ObjectInfo(ctx)
		if err != nil {
			writeError(w, 502, "api_error", "comfy_unavailable", err.Error(), "")
			return
		}
		a := importwf.Analyze(req.Workflow, info, allow, s.Cfg.ComfyUI.ModelsDir)
		if req.Name != "" {
			if n, err := importwf.SafeWorkflowName(req.Name); err == nil {
				a.Name = n
			}
		}
		writeJSON(w, 200, a)
		return
	}
	if req.Name == "" {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "name or workflow required", "name")
		return
	}
	a, err := importwf.AnalyzeNamed(ctx, s.Comfy, req.Name, allow, s.Cfg.ComfyUI.ModelsDir)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "name")
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) provisionComfyWorkflow(w http.ResponseWriter, r *http.Request) {
	var req importwf.ProvisionRequest
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if len(req.Workflow) == 0 && req.Name == "" {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "name or workflow required", "name")
		return
	}
	if len(req.Workflow) > 0 {
		req.Save = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	token := ""
	if env := s.Cfg.ComfyUI.HFTokenEnv; env != "" {
		token = os.Getenv(env)
	}
	if token == "" {
		token = os.Getenv("HF_TOKEN")
	}
	res, err := importwf.Provision(ctx, s.Comfy, req, importwf.ProvisionConfig{
		Allowlist:      importwf.NormalizeAllowlist(s.Cfg.ComfyUI.NodeAllowlist),
		CustomNodesDir: s.Cfg.ComfyUI.CustomNodesDir,
		ModelsDir:      s.Cfg.ComfyUI.ModelsDir,
		HFToken:        token,
		HFAllow:        s.Cfg.ComfyUI.HFAllowlist,
		ModelMap:       s.Cfg.ComfyUI.ModelMap,
		Downloads:      s.DL,
	})
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "")
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) comfyDownloadStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j := s.DL.Get(id)
	if j == nil {
		writeError(w, 404, "invalid_request_error", "not_found", "download job not found", "id")
		return
	}
	snap := j.Snapshot()
	writeJSON(w, 200, snap)
}

func (s *Server) comfyQueue(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, err := s.Comfy.Status(ctx)
	if err != nil {
		writeError(w, 502, "api_error", "comfy_unavailable", err.Error(), "")
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) comfyDownloadsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"object": "list", "data": s.DL.List()})
}

func (s *Server) listComfyWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	items, err := importwf.List(ctx, s.Comfy, s.Cfg.ModelsFile, s.Cfg.WorkflowsDir)
	if err != nil {
		writeError(w, 502, "api_error", "comfy_unavailable", err.Error(), "")
		return
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": items})
}

func (s *Server) importComfyWorkflows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if len(req.Names) == 0 {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "select at least one workflow", "names")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, err := importwf.ImportSelected(ctx, s.Comfy, s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, req.Names, false)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "names")
		return
	}
	if len(res.Imported) > 0 {
		if cat, err := catalog.Load(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, s.Cfg.SkipWorkflow); err == nil {
			s.Cat = cat
		}
	}
	status := 200
	if len(res.Imported) == 0 && len(res.Failed) > 0 {
		status = 400
	}
	writeJSON(w, status, res)
}

func (s *Server) unloadComfyModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "select at least one model", "ids")
		return
	}
	res, err := importwf.Unload(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, req.IDs)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "ids")
		return
	}
	if cat, err := catalog.Load(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, s.Cfg.SkipWorkflow); err == nil {
		s.Cat = cat
	}
	status := 200
	if len(res.Removed) == 0 && len(res.Failed) > 0 {
		status = 400
	}
	writeJSON(w, status, res)
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := importwf.Unload(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, []string{id})
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "id")
		return
	}
	if len(res.Removed) == 0 {
		writeError(w, 404, "invalid_request_error", "model_not_found", "model not in OpenComfy", "id")
		return
	}
	if cat, err := catalog.Load(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, s.Cfg.SkipWorkflow); err == nil {
		s.Cat = cat
	}
	writeJSON(w, 200, res)
}
