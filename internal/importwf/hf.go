package importwf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var hfAPI = "https://huggingface.co"

var hfResolveRe = regexp.MustCompile(`(?i)https?://huggingface\.co/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/resolve/[^/\s"'<>]+/([^?\s"'<>]+)`)

var genericWeights = map[string]bool{
	"model.safetensors": true, "model.ckpt": true, "model.pt": true,
	"pytorch_model.bin": true, "diffusion_pytorch_model.safetensors": true,
	"model.fp16.safetensors": true, "model.bf16.safetensors": true,
}

type HFHit struct {
	Repo string
	File string
}

type hfModel struct {
	ID       string `json:"id"`
	Siblings []struct {
		RFilename string `json:"rfilename"`
	} `json:"siblings"`
}

var hfOfficialOrgs = []string{"Comfy-Org", "Lightricks", "comfyanonymous", "Kijai"}

var (
	officialMu       sync.Mutex
	officialByBase   map[string][]HFHit
	officialCacheKey string
)

func resetOfficialHFIndex() {
	officialMu.Lock()
	officialByBase = nil
	officialCacheKey = ""
	officialMu.Unlock()
}

func ResolveHF(ctx context.Context, value string, token string, allow []string, modelMap map[string]string, preferFolder string) (HFHit, error) {
	value = normalizeModelPath(value)
	if value == "" || strings.Contains(value, "..") {
		return HFHit{}, fmt.Errorf("bad model path")
	}
	base := path.Base(value)
	if hit, ok := lookupMap(value, modelMap); ok {
		if !hfRepoAllowed(hit.Repo, allow) {
			return HFHit{}, fmt.Errorf("hf repo %s not on allowlist", hit.Repo)
		}
		return hit, nil
	}
	if genericWeights[strings.ToLower(base)] {
		return HFHit{}, fmt.Errorf("generic filename %s needs comfyui.model_map", base)
	}
	hit, err := searchHF(ctx, value, token, preferFolder, allow)
	if err != nil {
		return HFHit{}, err
	}
	log.Printf("opencomfy hf %s -> %s/%s", value, hit.Repo, hit.File)
	return hit, nil
}

func collectHFMap(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return out
	}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			addHFLinks(out, t)
		case []any:
			for _, x := range t {
				walk(x)
			}
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(root)
	return out
}

func addHFLinks(out map[string]string, s string) {
	for _, m := range hfResolveRe.FindAllStringSubmatch(s, -1) {
		org, repo, file := m[1], m[2], m[3]
		file = strings.TrimRight(file, ".,);]")
		if u, err := url.PathUnescape(file); err == nil {
			file = u
		}
		file = strings.ReplaceAll(file, "\\", "/")
		file = strings.TrimPrefix(file, "/")
		if file == "" || strings.Contains(file, "..") {
			continue
		}
		spec := org + "/" + repo + ":" + file
		if base := path.Base(file); base != "" {
			out[base] = spec
		}
		out[file] = spec
	}
}

func mergeHFMap(cfg, fromWorkflow map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range fromWorkflow {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	for k, v := range cfg {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func lookupMap(value string, modelMap map[string]string) (HFHit, bool) {
	if modelMap == nil {
		return HFHit{}, false
	}
	keys := []string{value, path.Base(value)}
	for _, k := range keys {
		s, ok := modelMap[k]
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		repo, file := splitRepoFile(s, value)
		return HFHit{Repo: repo, File: file}, true
	}
	return HFHit{}, false
}

func splitRepoFile(spec, fallback string) (repo, file string) {
	spec = strings.TrimPrefix(spec, "hf://")
	spec = strings.TrimPrefix(spec, "https://huggingface.co/")
	if i := strings.Index(spec, ":"); i > 0 && !strings.Contains(spec[:i], "/") {
		// not host
	}
	if strings.Count(spec, ":") == 1 && !strings.HasPrefix(spec, "http") {
		a, b, _ := strings.Cut(spec, ":")
		return strings.TrimSpace(a), strings.TrimSpace(b)
	}
	parts := strings.Split(spec, "/")
	if len(parts) >= 3 {
		return parts[0] + "/" + parts[1], strings.Join(parts[2:], "/")
	}
	if len(parts) == 2 {
		file = path.Base(fallback)
		return spec, file
	}
	return spec, path.Base(fallback)
}

func searchHF(ctx context.Context, value, token, preferFolder string, allow []string) (HFHit, error) {
	base := path.Base(value)
	stem := strings.TrimSuffix(base, path.Ext(base))
	seenQ := map[string]bool{}
	var models []hfModel
	for _, q := range []string{base, stem} {
		q = strings.TrimSpace(q)
		key := strings.ToLower(q)
		if len(q) < 4 || seenQ[key] {
			continue
		}
		seenQ[key] = true
		u := strings.TrimRight(hfAPI, "/") + "/api/models?search=" + url.QueryEscape(q) + "&limit=25&full=true"
		raw, err := hfGet(ctx, u, token)
		if err != nil {
			log.Printf("opencomfy hf search %s: %v", q, err)
			continue
		}
		var list []hfModel
		if json.Unmarshal(raw, &list) != nil {
			continue
		}
		models = append(models, list...)
	}
	models = ensureSiblings(ctx, models, token)
	hits := hitsFromModels(models, value)
	if idx := officialIndex(ctx, token); len(idx) > 0 {
		hits = append(hits, idx[strings.ToLower(base)]...)
	}
	return pickHFHit(hits, value, preferFolder, allow)
}

func ensureSiblings(ctx context.Context, models []hfModel, token string) []hfModel {
	for i, m := range models {
		if m.ID == "" || len(m.Siblings) > 0 {
			continue
		}
		raw, err := hfGet(ctx, hfModelURL(m.ID), token)
		if err != nil {
			continue
		}
		var card hfModel
		if json.Unmarshal(raw, &card) != nil {
			continue
		}
		if card.ID == "" {
			card.ID = m.ID
		}
		models[i] = card
	}
	return models
}

func hitsFromModels(models []hfModel, want string) []HFHit {
	var hits []HFHit
	for _, m := range models {
		if m.ID == "" {
			continue
		}
		for _, s := range m.Siblings {
			if hfFileMatch(want, s.RFilename) {
				hits = append(hits, HFHit{Repo: m.ID, File: s.RFilename})
			}
		}
	}
	return hits
}

func officialIndex(ctx context.Context, token string) map[string][]HFHit {
	key := strings.TrimRight(hfAPI, "/")
	officialMu.Lock()
	defer officialMu.Unlock()
	if officialByBase != nil && officialCacheKey == key {
		return officialByBase
	}
	by := map[string][]HFHit{}
	for _, org := range hfOfficialOrgs {
		u := key + "/api/models?author=" + url.QueryEscape(org) + "&full=true&limit=200"
		raw, err := hfGet(ctx, u, token)
		if err != nil {
			log.Printf("opencomfy hf author %s: %v", org, err)
			continue
		}
		var list []hfModel
		if json.Unmarshal(raw, &list) != nil {
			continue
		}
		for _, m := range list {
			if m.ID == "" {
				continue
			}
			for _, s := range m.Siblings {
				fn := strings.ReplaceAll(s.RFilename, "\\", "/")
				if fn == "" {
					continue
				}
				b := strings.ToLower(path.Base(fn))
				by[b] = append(by[b], HFHit{Repo: m.ID, File: fn})
			}
		}
	}
	officialByBase = by
	officialCacheKey = key
	return by
}

func pickHFHit(hits []HFHit, want, preferFolder string, allow []string) (HFHit, error) {
	best := HFHit{}
	bestN := -1
	seen := map[string]bool{}
	for _, h := range hits {
		key := h.Repo + "\t" + h.File
		if seen[key] {
			continue
		}
		seen[key] = true
		if !hfRepoAllowed(h.Repo, allow) {
			continue
		}
		n := scoreHFHit(h, want, preferFolder)
		if n > bestN {
			best, bestN = h, n
		}
	}
	if bestN < 0 {
		return HFHit{}, fmt.Errorf("no huggingface file matching %s", want)
	}
	return best, nil
}

func scoreHFHit(h HFHit, want, preferFolder string) int {
	file := strings.ReplaceAll(h.File, "\\", "/")
	want = strings.ReplaceAll(want, "\\", "/")
	base := path.Base(want)
	n := 0
	switch {
	case file == want || strings.HasSuffix(file, "/"+want):
		n += 100
	case path.Base(file) == base:
		n += 50
	default:
		return -1
	}
	org, _, _ := strings.Cut(h.Repo, "/")
	for i, o := range hfOfficialOrgs {
		if strings.EqualFold(org, o) {
			n += 40 - i
			break
		}
	}
	if preferFolder != "" && (strings.Contains(file, "/"+preferFolder+"/") || strings.HasPrefix(file, preferFolder+"/")) {
		n += 15
	}
	if strings.Contains(file, "split_files/") {
		n += 5
	}
	return n
}

func hfModelURL(id string) string {
	id = strings.Trim(id, "/")
	a, b, ok := strings.Cut(id, "/")
	root := strings.TrimRight(hfAPI, "/") + "/api/models/"
	if !ok {
		return root + url.PathEscape(id)
	}
	return root + url.PathEscape(a) + "/" + url.PathEscape(b)
}

func hfFileURL(repo, file string) string {
	repo = strings.Trim(repo, "/")
	a, b, ok := strings.Cut(repo, "/")
	root := strings.TrimRight(hfAPI, "/") + "/"
	if !ok {
		return root + url.PathEscape(repo) + "/resolve/main/" + encodeHFPath(file)
	}
	return root + url.PathEscape(a) + "/" + url.PathEscape(b) + "/resolve/main/" + encodeHFPath(file)
}

func encodeHFPath(file string) string {
	file = strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "/")
	parts := strings.Split(file, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func hfFileMatch(want, got string) bool {
	want, got = strings.ReplaceAll(want, "\\", "/"), strings.ReplaceAll(got, "\\", "/")
	if want == got {
		return true
	}
	return path.Base(want) == path.Base(got)
}

func hfRepoAllowed(repo string, allow []string) bool {
	repo = strings.TrimSpace(repo)
	if repo == "" || strings.Count(repo, "/") != 1 {
		return false
	}
	for _, r := range repo {
		if r > 127 || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == '/') {
			return false
		}
	}
	if len(allow) == 0 {
		return true
	}
	low := strings.ToLower(repo)
	org, _, _ := strings.Cut(low, "/")
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		a = strings.TrimPrefix(a, "https://huggingface.co/")
		if a == low || a == org {
			return true
		}
	}
	return false
}

func hfGet(ctx context.Context, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "OpenComfy")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("huggingface %s: %s", resp.Status, truncateBytes(b, 180))
	}
	return b, nil
}

func modelFolder(class, field string) string {
	c, f := strings.ToLower(class), strings.ToLower(field)
	switch {
	case strings.Contains(c, "unet") || strings.Contains(c, "diffusion") || f == "unet_name" || f == "diffusion_model":
		return "diffusion_models"
	case strings.Contains(c, "clip") || strings.Contains(c, "t5") || strings.HasPrefix(f, "clip") || strings.Contains(f, "text_encoder"):
		return "text_encoders"
	case strings.Contains(c, "vae") || strings.Contains(f, "vae"):
		return "vae"
	case strings.Contains(c, "lora") || strings.Contains(f, "lora"):
		return "loras"
	case strings.Contains(c, "control") || strings.Contains(f, "control"):
		return "controlnet"
	default:
		return "checkpoints"
	}
}
