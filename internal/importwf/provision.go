package importwf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/javded-itres/open-comfy/internal/comfy"
)

type ProvisionRequest struct {
	Name          string          `json:"name"`
	Overwrite     bool            `json:"overwrite"`
	Save          bool            `json:"save"`
	InstallNodes  bool            `json:"install_nodes"`
	InstallModels bool            `json:"install_models"`
	Workflow      json.RawMessage `json:"workflow"`
}

type ProvisionConfig struct {
	Allowlist      []string
	CustomNodesDir string
	ModelsDir      string
	HFToken        string
	HFAllow        []string
	ModelMap       map[string]string
	Downloads      *Downloads
}

type ProvisionResult struct {
	Analysis
	Saved       bool     `json:"saved"`
	Path        string   `json:"path,omitempty"`
	Installed   []string `json:"installed,omitempty"`
	SkippedGit  []string `json:"skipped_git,omitempty"`
	NeedsReboot bool     `json:"needs_reboot"`
	DownloadID  string   `json:"download_id,omitempty"`
}

func SafeWorkflowName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimPrefix(name, ".")
	if name == "" || name == "/" {
		return "", fmt.Errorf("empty workflow name")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	if strings.ContainsAny(name, "/\\:\x00") {
		return "", fmt.Errorf("invalid workflow name")
	}
	if len(name) > 180 {
		return "", fmt.Errorf("workflow name too long")
	}
	for _, r := range name {
		if r < 32 || (!unicode.IsPrint(r) && r != ' ') {
			return "", fmt.Errorf("invalid workflow name")
		}
	}
	return name, nil
}

func AnalyzeNamed(ctx context.Context, client *comfy.Client, name string, allow []string, modelsDir string) (Analysis, error) {
	raw, err := client.GetUserWorkflow(ctx, name)
	if err != nil {
		return Analysis{}, err
	}
	info, err := client.ObjectInfo(ctx)
	if err != nil {
		return Analysis{}, err
	}
	a := Analyze(raw, info, allow, modelsDir)
	a.Name = name
	return a, nil
}

func Provision(ctx context.Context, client *comfy.Client, req ProvisionRequest, cfg ProvisionConfig) (ProvisionResult, error) {
	var out ProvisionResult
	name, err := SafeWorkflowName(req.Name)
	if err != nil {
		if len(req.Workflow) > 0 {
			name = "uploaded.json"
		} else {
			return out, err
		}
	}
	out.Name = name
	raw := req.Workflow
	if len(raw) == 0 {
		got, err := client.GetUserWorkflow(ctx, name)
		if err != nil {
			return out, err
		}
		raw = got
	}
	if req.Save || req.Overwrite || len(req.Workflow) > 0 {
		if err := client.PutUserWorkflow(ctx, name, raw, req.Overwrite || req.Save); err != nil {
			return out, err
		}
		out.Saved = true
		out.Path = "workflows/" + name
	}
	info, err := client.ObjectInfo(ctx)
	if err != nil {
		return out, err
	}
	out.Analysis = Analyze(raw, info, cfg.Allowlist, cfg.ModelsDir)
	out.Name = name
	modelMap := mergeHFMap(cfg.ModelMap, collectHFMap(raw))
	if req.InstallNodes && cfg.CustomNodesDir != "" {
		installed, skipped, reboot := installAllowlisted(ctx, cfg.CustomNodesDir, out.Allowlisted)
		out.Installed = installed
		out.SkippedGit = skipped
		out.NeedsReboot = reboot
		if reboot {
			out.Ready = false
		}
	} else if req.InstallNodes && cfg.CustomNodesDir == "" {
		out.SkippedGit = append(out.SkippedGit, "comfyui.custom_nodes_dir is empty; node git clone disabled")
	}
	if req.InstallModels && len(out.MissingModels) > 0 {
		if cfg.ModelsDir == "" {
			out.Errors = append(out.Errors, "comfyui.models_dir is empty; Hugging Face download disabled")
		} else if cfg.Downloads != nil {
			job := cfg.Downloads.Start(ctx, out.MissingModels, HFOpts{
				ModelsDir: cfg.ModelsDir,
				Token:     cfg.HFToken,
				Allow:     cfg.HFAllow,
				ModelMap:  modelMap,
			})
			out.DownloadID = job.ID
			out.Ready = false
		}
	}
	return out, nil
}

func installAllowlisted(ctx context.Context, dir string, packs []NodeNeed) (installed, skipped []string, reboot bool) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, []string{err.Error()}, false
	}
	seen := map[string]bool{}
	for _, p := range packs {
		if !p.Allowlisted || p.Git == "" {
			continue
		}
		git := p.Git
		if seen[git] {
			continue
		}
		seen[git] = true
		dest := filepath.Join(dir, packName(git))
		if st, err := os.Stat(dest); err == nil && st.IsDir() {
			skipped = append(skipped, dest+" (exists; restart ComfyUI if class is still missing)")
			reboot = true
			continue
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", git, dest)
		b, err := cmd.CombinedOutput()
		if err != nil {
			skipped = append(skipped, git+": "+err.Error()+" "+truncateBytes(b, 200))
			continue
		}
		installed = append(installed, dest)
		reboot = true
	}
	return installed, skipped, reboot
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
