package importwf

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectHFMapFromMarkdown(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"type":"MarkdownNote","widgets_values":["see [unet](https://huggingface.co/Comfy-Org/FLUX.1-Krea-dev_ComfyUI/resolve/main/split_files/diffusion_models/flux1-krea-dev_fp8_scaled.safetensors) and https://huggingface.co/comfyanonymous/flux_text_encoders/resolve/main/clip_l.safetensors"]}]}`)
	got := collectHFMap(raw)
	if got["flux1-krea-dev_fp8_scaled.safetensors"] != "Comfy-Org/FLUX.1-Krea-dev_ComfyUI:split_files/diffusion_models/flux1-krea-dev_fp8_scaled.safetensors" {
		t.Fatalf("%+v", got)
	}
	if got["clip_l.safetensors"] != "comfyanonymous/flux_text_encoders:clip_l.safetensors" {
		t.Fatalf("%+v", got)
	}
}

func TestSplitRepoFile(t *testing.T) {
	repo, file := splitRepoFile("org/name", "ZIT/foo.safetensors")
	if repo != "org/name" || file != "foo.safetensors" {
		t.Fatalf("%s %s", repo, file)
	}
	repo, file = splitRepoFile("org/name:weights/foo.safetensors", "x")
	if repo != "org/name" || file != "weights/foo.safetensors" {
		t.Fatalf("%s %s", repo, file)
	}
}

func TestHfRepoAllowed(t *testing.T) {
	if !hfRepoAllowed("org/name", nil) {
		t.Fatal("empty allow")
	}
	if hfRepoAllowed("org/name", []string{"other"}) {
		t.Fatal("blocked")
	}
	if !hfRepoAllowed("org/name", []string{"org"}) {
		t.Fatal("org")
	}
}

func TestDownloadMappedFile(t *testing.T) {
	payload := []byte("safetensors-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/owner/repo/resolve/main/foo.safetensors") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", "17")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	old := hfAPI
	hfAPI = srv.URL
	t.Cleanup(func() { hfAPI = old })
	dir := t.TempDir()
	it := &DLItem{Value: "ZIT/foo.safetensors", Field: "unet_name", Class: "UNETLoader"}
	if err := downloadOne(context.Background(), it, HFOpts{
		ModelsDir: dir,
		ModelMap:  map[string]string{"ZIT/foo.safetensors": "owner/repo:foo.safetensors"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "diffusion_models", "ZIT", "foo.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("%q", got)
	}
	if it.Repo != "owner/repo" {
		t.Fatalf("repo %s", it.Repo)
	}
}

func TestResolveHFGenericNeedsMap(t *testing.T) {
	_, err := ResolveHF(context.Background(), "model.safetensors", "", nil, nil, "")
	if err == nil || !strings.Contains(err.Error(), "model_map") {
		t.Fatalf("%v", err)
	}
}

func TestSearchHFOfficialFallback(t *testing.T) {
	resetOfficialHFIndex()
	t.Cleanup(resetOfficialHFIndex)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/models") && q.Get("author") == "Comfy-Org":
			_, _ = io.WriteString(w, `[{"id":"Comfy-Org/MiniMax-H3","siblings":[{"rfilename":"text_encoders/qwen3vl_32b_minimax_h3_int8_convrot.safetensors"}]}]`)
		case strings.HasPrefix(r.URL.Path, "/api/models") && q.Get("search") != "":
			_, _ = io.WriteString(w, `[{"id":"random/unrelated","siblings":[{"rfilename":"README.md"}]}]`)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	t.Cleanup(srv.Close)
	old := hfAPI
	hfAPI = srv.URL
	t.Cleanup(func() { hfAPI = old; resetOfficialHFIndex() })
	hit, err := ResolveHF(context.Background(), "qwen3vl_32b_minimax_h3_int8_convrot.safetensors", "", nil, nil, "text_encoders")
	if err != nil {
		t.Fatal(err)
	}
	if hit.Repo != "Comfy-Org/MiniMax-H3" || !strings.HasSuffix(hit.File, "qwen3vl_32b_minimax_h3_int8_convrot.safetensors") {
		t.Fatalf("%+v", hit)
	}
}

func TestSearchHFPrefersOfficialOverFork(t *testing.T) {
	resetOfficialHFIndex()
	t.Cleanup(resetOfficialHFIndex)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("author") == "Comfy-Org" {
			_, _ = io.WriteString(w, `[{"id":"Comfy-Org/z_image_turbo","siblings":[{"rfilename":"split_files/diffusion_models/z_image_turbo_bf16.safetensors"}]}]`)
			return
		}
		if q.Get("search") != "" {
			_, _ = io.WriteString(w, `[{"id":"fork/z_image_turbo_bf16","siblings":[{"rfilename":"z_image_turbo_bf16.safetensors"}]}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)
	old := hfAPI
	hfAPI = srv.URL
	t.Cleanup(func() { hfAPI = old; resetOfficialHFIndex() })
	hit, err := ResolveHF(context.Background(), "ZIT/z_image_turbo_bf16.safetensors", "", nil, nil, "diffusion_models")
	if err != nil {
		t.Fatal(err)
	}
	if hit.Repo != "Comfy-Org/z_image_turbo" {
		t.Fatalf("%+v", hit)
	}
}

func TestExpectedDestNormalizesBackslash(t *testing.T) {
	dir := t.TempDir()
	got := expectedDest(dir, "LoraLoaderModelOnly", "lora_name", `LTX2\foo.safetensors`)
	want := filepath.Join(dir, "loras", "LTX2", "foo.safetensors")
	if got != want {
		t.Fatalf("%q != %q", got, want)
	}
}

func TestDownloadReusesBasenameInOtherFolder(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	old := hfAPI
	hfAPI = srv.URL
	t.Cleanup(func() { hfAPI = old })
	dir := t.TempDir()
	src := filepath.Join(dir, "checkpoints", "z_image_turbo_bf16.safetensors")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := &DLItem{Value: "ZIT/z_image_turbo_bf16.safetensors", Field: "unet_name", Class: "UNETLoader"}
	if err := downloadOne(context.Background(), it, HFOpts{ModelsDir: dir}); err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Fatalf("huggingface called %d times", hits)
	}
	dest := filepath.Join(dir, "diffusion_models", "ZIT", "z_image_turbo_bf16.safetensors")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "weights" {
		t.Fatalf("%q", got)
	}
	if it.Local == "" {
		t.Fatal("local path empty")
	}
}

func TestDownloadSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "vae", "ae.safetensors")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	it := &DLItem{Value: "ae.safetensors", Field: "vae_name", Class: "VAELoader"}
	if err := downloadOne(context.Background(), it, HFOpts{
		ModelsDir: dir,
		ModelMap:  map[string]string{"ae.safetensors": "owner/repo:ae.safetensors"},
	}); err != nil {
		t.Fatal(err)
	}
	if it.Bytes != 2<<20 {
		t.Fatalf("bytes %d", it.Bytes)
	}
}

func TestHfGetUsesToken(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "[]")
	}))
	t.Cleanup(srv.Close)
	old := hfAPI
	hfAPI = srv.URL
	t.Cleanup(func() { hfAPI = old })
	_, err := hfGet(context.Background(), srv.URL+"/api/models", "hf_test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer hf_test" {
		t.Fatalf("auth %q", got)
	}
}
