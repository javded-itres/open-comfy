package importwf

import (
	"context"
	"encoding/json"

	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/importwf/analyze"
	"github.com/javded-itres/open-comfy/internal/importwf/catalogimport"
	"github.com/javded-itres/open-comfy/internal/importwf/hf"
)

// Facades keep cmd and httpapi on the importwf root. Subpackages own one job each.

type Analysis = analyze.Analysis
type NodeNeed = analyze.NodeNeed
type ModelNeed = analyze.ModelNeed

type Downloads = hf.Downloads
type HFOpts = hf.HFOpts

type Item = catalogimport.Item
type Result = catalogimport.Result
type UnloadResult = catalogimport.UnloadResult

func UseEmptyNodeMap() { analyze.UseEmptyNodeMap() }

func NormalizeAllowlist(extra []string) []string { return analyze.NormalizeAllowlist(extra) }

func Analyze(raw json.RawMessage, info map[string]comfy.NodeDef, allow []string, modelsDir string) Analysis {
	return analyze.Analyze(raw, info, allow, modelsDir)
}

func NewDownloads() *Downloads { return hf.NewDownloads() }

func List(ctx context.Context, client *comfy.Client, modelsFile, workflowsDir string) ([]Item, error) {
	return catalogimport.List(ctx, client, modelsFile, workflowsDir)
}

func ImportSelected(ctx context.Context, client *comfy.Client, modelsFile, workflowsDir string, names []string, dry bool) (Result, error) {
	return catalogimport.ImportSelected(ctx, client, modelsFile, workflowsDir, names, dry)
}

func Unload(modelsFile, workflowsDir string, ids []string) (UnloadResult, error) {
	return catalogimport.Unload(modelsFile, workflowsDir, ids)
}
