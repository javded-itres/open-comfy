package comfy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestConvertWorkflowUsesComfyEndpoint(t *testing.T) {
	var gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"57:27":{"class_type":"CLIPTextEncode","inputs":{"text":"hello"}},"9":{"class_type":"SaveImage","inputs":{"images":["57:8",0]}}}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "", "cid", nil, 5*time.Second, 20*time.Millisecond, false)
	g, err := c.ConvertWorkflow(context.Background(), []byte(`{"nodes":[],"links":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/workflow/convert" {
		t.Fatalf("path %s", gotPath)
	}
	if !strings.Contains(string(gotBody), `"nodes"`) {
		t.Fatalf("body %s", gotBody)
	}
	if g["57:27"].(map[string]any)["class_type"] != "CLIPTextEncode" {
		t.Fatalf("%v", g)
	}
}

func TestConvertWorkflowRejectsUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"57":{"class_type":"f2fdebf6-dfaf-43b6-9eb2-7f70613cfdc1","inputs":{}}}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "", "cid", nil, 5*time.Second, 20*time.Millisecond, false)
	_, err := c.ConvertWorkflow(context.Background(), []byte(`{"nodes":[],"links":[]}`))
	if err == nil || !strings.Contains(err.Error(), "unexpanded") {
		t.Fatalf("err=%v", err)
	}
}
