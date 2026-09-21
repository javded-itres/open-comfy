package importwf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
)

type Item struct {
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Modality  string   `json:"modality,omitempty"`
	Params    []string `json:"params,omitempty"`
	Exists    bool     `json:"exists"`
	CanImport bool     `json:"can_import"`
	Errors    []string `json:"errors,omitempty"`
}

type Result struct {
	Imported []string `json:"imported"`
	Skipped  []string `json:"skipped"`
	Failed   []string `json:"failed"`
}

func List(ctx context.Context, client *comfy.Client, modelsFile, workflowsDir string) ([]Item, error) {
	names, err := client.ListUserWorkflows(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	info, err := client.ObjectInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("object_info: %w", err)
	}
	cat, err := loadCat(modelsFile, workflowsDir)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(names))
	for _, name := range names {
		out = append(out, inspect(client, ctx, name, info, cat))
	}
	return out, nil
}

func inspect(client *comfy.Client, ctx context.Context, name string, info map[string]comfy.NodeDef, cat *catalog.Catalog) Item {
	id := Slug(name)
	title := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	it := Item{Name: name, ID: id, Title: title, Exists: cat.Has(id)}
	raw, err := client.GetUserWorkflow(ctx, name)
	if err != nil {
		it.Errors = []string{"fetch: " + err.Error()}
		return it
	}
	graph, err := ConvertWith(ctx, client, raw, info)
	if err != nil {
		it.Errors = []string{"convert: " + err.Error()}
		return it
	}
	graph, err = ExpandFrontend(ctx, client, graph)
	if err != nil {
		it.Errors = []string{err.Error()}
		return it
	}
	m, inferErr := Infer(id, name, title, graph, info)
	it.Modality = m.Modality
	for _, p := range m.Parameters {
		it.Params = append(it.Params, p.Name)
	}
	if inferErr != nil {
		it.Errors = append(it.Errors, inferErr.Error())
	} else {
		it.Errors = append(it.Errors, Gate(&m, graph)...)
	}
	if it.Exists {
		it.Errors = append(it.Errors, "already in models.yaml")
	}
	it.CanImport = len(it.Errors) == 0
	return it
}

func ImportSelected(ctx context.Context, client *comfy.Client, modelsFile, workflowsDir string, names []string, dry bool) (Result, error) {
	var res Result
	if len(names) == 0 {
		return res, fmt.Errorf("select at least one workflow")
	}
	info, err := client.ObjectInfo(ctx)
	if err != nil {
		return res, fmt.Errorf("object_info: %w", err)
	}
	cat, err := loadCat(modelsFile, workflowsDir)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(workflowsDir, 0o755); err != nil {
		return res, err
	}
	available, err := client.ListUserWorkflows(ctx)
	if err != nil {
		return res, err
	}
	allow := map[string]string{}
	for _, n := range available {
		allow[n] = n
		allow[filepath.Base(n)] = n
		allow[Slug(n)] = n
	}
	changed := false
	for _, sel := range names {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		name, ok := allow[sel]
		if !ok {
			res.Failed = append(res.Failed, sel+": not in ComfyUI library")
			continue
		}
		it := inspect(client, ctx, name, info, cat)
		if it.Exists {
			res.Skipped = append(res.Skipped, it.ID+" (exists)")
			continue
		}
		if !it.CanImport {
			res.Failed = append(res.Failed, name+": "+strings.Join(it.Errors, "; "))
			continue
		}
		raw, err := client.GetUserWorkflow(ctx, name)
		if err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		graph, err := ConvertWith(ctx, client, raw, info)
		if err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		graph, err = ExpandFrontend(ctx, client, graph)
		if err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		m, err := Infer(it.ID, name, it.Title, graph, info)
		if err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		graph, pick := catalog.EnsureSaver(graph, m.Modality, m.OutputNode)
		m.OutputNode = pick
		if dry {
			res.Imported = append(res.Imported, fmt.Sprintf("%s [%s] params=%s (dry-run)", m.ID, m.Modality, strings.Join(it.Params, ",")))
			continue
		}
		b, err := json.MarshalIndent(graph, "", "  ")
		if err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		wfPath := filepath.Join(workflowsDir, m.Workflow)
		if err := os.WriteFile(wfPath, append(b, '\n'), 0o644); err != nil {
			res.Failed = append(res.Failed, name+": write: "+err.Error())
			continue
		}
		if err := catalog.ValidateModel(&m, workflowsDir); err != nil {
			_ = os.Remove(wfPath)
			res.Failed = append(res.Failed, name+": validate: "+err.Error())
			continue
		}
		cat.Add(m)
		changed = true
		res.Imported = append(res.Imported, fmt.Sprintf("%s [%s] → %s (%d params)", m.ID, m.Modality, m.Workflow, len(m.Parameters)))
	}
	if changed && !dry {
		bak := modelsFile + ".bak"
		if b, err := os.ReadFile(modelsFile); err == nil {
			_ = os.WriteFile(bak, b, 0o644)
		}
		if err := cat.Save(modelsFile); err != nil {
			return res, err
		}
	}
	return res, nil
}

type UnloadResult struct {
	Removed []string `json:"removed"`
	Failed  []string `json:"failed"`
}

// Unload drops models from OpenComfy (models.yaml + copied JSON).
// ComfyUI userdata/workflows is never touched.
func Unload(modelsFile, workflowsDir string, ids []string) (UnloadResult, error) {
	var res UnloadResult
	if len(ids) == 0 {
		return res, fmt.Errorf("select at least one model")
	}
	cat, err := loadCat(modelsFile, workflowsDir)
	if err != nil {
		return res, err
	}
	changed := false
	for _, raw := range ids {
		id := catalog.NormalizeID(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		m, ok := cat.Remove(id)
		if !ok {
			res.Failed = append(res.Failed, id+": not in OpenComfy")
			continue
		}
		if m.Workflow != "" {
			still := false
			for _, x := range cat.All() {
				if x.Workflow == m.Workflow {
					still = true
					break
				}
			}
			if !still {
				if p, err := catalog.WorkflowPath(workflowsDir, m.Workflow); err == nil {
					_ = os.Remove(p)
				}
			}
		}
		changed = true
		res.Removed = append(res.Removed, m.ID)
	}
	if changed {
		bak := modelsFile + ".bak"
		if b, err := os.ReadFile(modelsFile); err == nil {
			_ = os.WriteFile(bak, b, 0o644)
		}
		if err := cat.Save(modelsFile); err != nil {
			return res, err
		}
	}
	return res, nil
}

func loadCat(modelsFile, workflowsDir string) (*catalog.Catalog, error) {
	cat, err := catalog.Load(modelsFile, workflowsDir, true)
	if err != nil {
		if os.IsNotExist(err) {
			return &catalog.Catalog{WorkflowsDir: workflowsDir}, nil
		}
		return nil, err
	}
	return cat, nil
}
