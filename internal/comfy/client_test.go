package comfy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdata(t *testing.T, elem ...string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "testdata")
	return filepath.Join(append([]string{root}, elem...)...)
}

func TestExtractPromptIDQueueReal(t *testing.T) {
	b, err := os.ReadFile(testdata(t, "history", "queue_real.json"))
	if err != nil {
		t.Fatal(err)
	}
	var q Queue
	if err := json.Unmarshal(b, &q); err != nil {
		t.Fatal(err)
	}
	if got := ExtractPromptID(q.Running[0]); got != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Fatalf("running id=%q", got)
	}
	if got := ExtractPromptID(q.Pending[0]); got != "bbbbbbbb-e5f6-7890-abcd-ef1234567890" {
		t.Fatalf("pending id=%q", got)
	}
	run, pos := q.Position("bbbbbbbb-e5f6-7890-abcd-ef1234567890")
	if run || pos != 1 {
		t.Fatalf("pos=%v running=%v", pos, run)
	}
}

func TestPickArtifactVHSGifs(t *testing.T) {
	b, err := os.ReadFile(testdata(t, "history", "history_vhs_gifs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hist map[string]any
	if err := json.Unmarshal(b, &hist); err != nil {
		t.Fatal(err)
	}
	a, err := PickArtifact(hist, "5218", "video", "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	if a.Filename != "LTX2_00001.mp4" || a.Kind != "video" {
		t.Fatalf("%+v", a)
	}
}

func TestPickArtifactSaveVideoMP4InImages(t *testing.T) {
	hist := map[string]any{
		"outputs": map[string]any{
			"92": map[string]any{
				"images": []any{
					map[string]any{"filename": "MiniMax_H3_00018_.mp4", "subfolder": "video", "type": "output"},
				},
				"animated": []any{true},
			},
		},
	}
	a, err := PickArtifact(hist, "92", "video", "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	if a.Filename != "MiniMax_H3_00018_.mp4" || a.Kind != "video" {
		t.Fatalf("%+v", a)
	}
}
