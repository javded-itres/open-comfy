package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func InitFiles(configPath string) (plainKey string, err error) {
	if configPath == "" {
		configPath = "/etc/opencomfy/config.yaml"
	}
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data := env("OPENCOMFY_DATA", "/var/lib/opencomfy")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return "", err
	}
	cfg := filepath.Join(dir, "config.yaml")
	if configPath != "" {
		cfg = configPath
	}
	writes := []struct {
		path string
		mode os.FileMode
		body string
	}{
		{cfg, 0o644, exampleConfig(dir, data)},
		{filepath.Join(dir, "keys.yaml"), 0o600, ""},
		{filepath.Join(dir, "models.yaml"), 0o644, exampleModels()},
	}
	wf := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		return "", err
	}
	for _, w := range writes {
		if _, err := os.Stat(w.path); err == nil {
			continue
		}
		body := w.body
		if filepath.Base(w.path) == "keys.yaml" {
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			plainKey = "sk-" + hex.EncodeToString(b)
			body = fmt.Sprintf("keys:\n  - name: default\n    key: %s\n    models: [\"*\"]\n    rpm: 0\n    max_concurrent: 2\n    enabled: true\n", plainKey)
		}
		if err := os.WriteFile(w.path, []byte(body), w.mode); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(wf, "toy_image.json"), []byte(toyImageGraph()), 0o644); err != nil && !os.IsExist(err) {
		_ = os.WriteFile(filepath.Join(wf, "toy_image.json"), []byte(toyImageGraph()), 0o644)
	}
	_ = os.WriteFile(filepath.Join(wf, "minimax_h3_t2v.json"), []byte(toyVideoGraph()), 0o644)
	secret := filepath.Join(data, "hmac.secret")
	if _, err := os.Stat(secret); os.IsNotExist(err) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return plainKey, err
		}
		if err := os.WriteFile(secret, []byte(hex.EncodeToString(b)+"\n"), 0o600); err != nil {
			return plainKey, err
		}
	}
	return plainKey, nil
}

func exampleConfig(etc, data string) string {
	return fmt.Sprintf(`listen: ":8788"
public_base_url: ""
log_level: info
http:
  max_body_bytes: 33554432
  max_wait_s: 120
  allow_long_wait: false
  chat_shim: true
  read_header_timeout_s: 10
  allow_remote_images: false
comfyui:
  base_url: "http://127.0.0.1:8188"
  request_timeout_s: 60
  poll_interval_s: 2
  max_in_flight: 2
  max_waiting: 32
  exclusive: true
  allow_interrupt: false
  min_version: "0.3.7"
files:
  dir: %s/files
  ttl: 24h
  max_bytes: 21474836480
  hmac_secret_file: %s/hmac.secret
jobs:
  dir: %s/jobs
  ttl: 48h
  max_active: 256
  store_prompt: false
metrics:
  enabled: true
  auth: auto
auth:
  disabled: false
  keys_file: %s/keys.yaml
models_file: %s/models.yaml
workflows_dir: %s/workflows
`, data, data, data, etc, etc, etc)
}

func exampleModels() string {
	return `default_image: toy-image
default_video: minimax-hailuo-02

size_aliases:
  "1024x1024": {width: 1024, height: 1024}
  "16:9": {width: 1280, height: 720}
  "9:16": {width: 720, height: 1280}
  "720p": {width: 1280, height: 720}
  "1080p": {width: 1920, height: 1080}
  "1K": {width: 1024, height: 1024}

models:
  - id: toy-image
    name: Toy Image
    aliases: ["flux-dev", "Flux", "flux"]
    modality: image
    workflow: toy_image.json
    output_mime: image/png
    output_node: "9"
    timeout_s: 180
    max_concurrent: 1
    pricing: {currency: USD, per_image: 0.02}
    quality_map:
      low:    {steps: 8,  cfg_scale: 1.0}
      medium: {steps: 20, cfg_scale: 3.5}
      high:   {steps: 35, cfg_scale: 4.0}
    parameters:
      - name: prompt
        type: string
        required: true
        maps_to:
          - {node: "6", field: text}
      - name: negative_prompt
        type: string
        default: ""
        maps_to:
          - {node: "7", field: text}
      - name: seed
        type: integer
        default: -1
        maps_to:
          - {node: "3", field: seed}
      - name: width
        type: integer
        default: 1024
        maps_to:
          - {node: "5", field: width}
      - name: height
        type: integer
        default: 1024
        maps_to:
          - {node: "5", field: height}
      - name: steps
        type: integer
        default: 20
        maps_to:
          - {node: "3", field: steps}
      - name: cfg_scale
        type: number
        default: 3.5
        maps_to:
          - {node: "3", field: cfg}
      - name: n
        type: integer
        default: 1
        min: 1
        max: 1

  - id: minimax-hailuo-02
    name: Minimax H3
    aliases: ["Minimax H3", "minimax/hailuo-3", "hailuo-3"]
    modality: video
    workflow: minimax_h3_t2v.json
    output_mime: video/mp4
    output_node: "save_video"
    timeout_s: 180
    max_concurrent: 1
    pricing: {currency: USD, per_second: 0.05}
    parameters:
      - name: prompt
        type: string
        required: true
        maps_to:
          - {node: "positive", field: text}
      - name: seconds
        type: integer
        default: 6
        enum: [5, 6, 10]
        maps_to:
          - {node: "length", field: value}
      - name: input_image
        type: image
        required: false
        maps_to:
          - {node: "load_image", field: image}
`
}

func toyImageGraph() string {
	return `{
  "3": {"class_type": "KSampler", "inputs": {"seed": 1, "steps": 20, "cfg": 3.5, "sampler_name": "euler", "scheduler": "normal", "denoise": 1, "model": ["4", 0], "positive": ["6", 0], "negative": ["7", 0], "latent_image": ["5", 0]}},
  "4": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "model.safetensors"}},
  "5": {"class_type": "EmptyLatentImage", "inputs": {"width": 1024, "height": 1024, "batch_size": 1}},
  "6": {"class_type": "CLIPTextEncode", "inputs": {"text": "", "clip": ["4", 1]}},
  "7": {"class_type": "CLIPTextEncode", "inputs": {"text": "", "clip": ["4", 1]}},
  "8": {"class_type": "VAEDecode", "inputs": {"samples": ["3", 0], "vae": ["4", 2]}},
  "9": {"class_type": "SaveImage", "inputs": {"filename_prefix": "toy", "images": ["8", 0]}}
}
`
}

func toyVideoGraph() string {
	return `{
  "positive": {"class_type": "CLIPTextEncode", "inputs": {"text": "", "clip": ["clip", 0]}},
  "clip": {"class_type": "CLIPLoader", "inputs": {"clip_name": "clip.safetensors"}},
  "length": {"class_type": "PrimitiveInt", "inputs": {"value": 6}},
  "load_image": {"class_type": "LoadImage", "inputs": {"image": ""}},
  "save_video": {"class_type": "SaveVideo", "inputs": {"filename_prefix": "mm", "images": ["positive", 0]}}
}
`
}
