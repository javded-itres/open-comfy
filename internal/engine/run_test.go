package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/workflow"
)

type fakeComfy struct {
	uploads int
	graphs  int
}

func (f *fakeComfy) UploadImage(context.Context, string, []byte) (comfy.UploadResult, error) {
	f.uploads++
	return comfy.UploadResult{Name: "up.png"}, nil
}

func (f *fakeComfy) QueuePrompt(context.Context, map[string]any, string) (comfy.PromptResult, error) {
	f.graphs++
	return comfy.PromptResult{RequestedID: "11111111-1111-1111-1111-111111111111", ReturnedID: "11111111-1111-1111-1111-111111111111"}, nil
}

func (f *fakeComfy) PollInterval() time.Duration { return time.Millisecond }

func (f *fakeComfy) Queue(context.Context) (comfy.Queue, error) { return comfy.Queue{}, nil }

func (f *fakeComfy) History(context.Context, string) (map[string]any, error) {
	return map[string]any{
		"status": map[string]any{"status_str": "success", "completed": true},
		"outputs": map[string]any{
			"9": map[string]any{"images": []any{map[string]any{"filename": "a.png", "type": "output"}}},
		},
	}, nil
}

func (f *fakeComfy) View(context.Context, comfy.Artifact) ([]byte, error) {
	return []byte("png"), nil
}

func TestRunUsesPort(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"9":{"class_type":"SaveImage","inputs":{"images":["6",0],"filename_prefix":"x"}},"6":{"class_type":"CLIPTextEncode","inputs":{"text":"","clip":["1",0]}}}`)
	if err := os.WriteFile(filepath.Join(dir, "toy.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &catalog.Model{
		ID: "toy", Modality: "image", Workflow: "toy.json", OutputNode: "9",
		Parameters: []catalog.Param{{
			Name:   "prompt",
			MapsTo: []catalog.MapTo{{Node: "6", Field: "text"}},
		}},
	}
	cat := &catalog.Catalog{WorkflowsDir: dir}
	fake := &fakeComfy{}
	res, pr, err := Run(context.Background(), fake, cat, m, workflow.Request{Prompt: "hi"}, "not-a-uuid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Bytes) != "png" || pr.ReturnedID == "" || fake.graphs != 1 || fake.uploads != 0 {
		t.Fatalf("res=%q pr=%+v fake=%+v", res.Bytes, pr, fake)
	}
}
