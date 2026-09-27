package hf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindLocalWeightByBasename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "checkpoints", "z_image_turbo_bf16.safetensors")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := findLocalWeight([]string{dir}, "ZIT/z_image_turbo_bf16.safetensors", "diffusion_models")
	if got != src {
		t.Fatalf("got %q want %q", got, src)
	}
}

func TestFindLocalWeightPrefersExpectedFolder(t *testing.T) {
	dir := t.TempDir()
	ckpt := filepath.Join(dir, "checkpoints", "foo.safetensors")
	diff := filepath.Join(dir, "diffusion_models", "bar", "foo.safetensors")
	for _, p := range []string{ckpt, diff} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := findLocalWeight([]string{dir}, "foo.safetensors", "diffusion_models")
	if got != diff {
		t.Fatalf("got %q want %q", got, diff)
	}
}

func TestFindLocalWeightAmbiguous(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "checkpoints", "foo.safetensors")
	b := filepath.Join(dir, "loras", "foo.safetensors")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := findLocalWeight([]string{dir}, "foo.safetensors", "diffusion_models"); got != "" {
		t.Fatalf("ambiguous hit %q", got)
	}
}

func TestFindLocalWeightSkipsGeneric(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "checkpoints", "model.safetensors")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findLocalWeight([]string{dir}, "checkpoints/model.safetensors", "checkpoints"); got != src {
		t.Fatalf("exact relative should hit, got %q", got)
	}
	if got := findLocalWeight([]string{dir}, "model.safetensors", "vae"); got != "" {
		t.Fatalf("generic basename should not match, got %q", got)
	}
	if got := findLocalWeight([]string{dir}, "other/model.safetensors", "vae"); got != "" {
		t.Fatalf("generic other path should not match, got %q", got)
	}
}
