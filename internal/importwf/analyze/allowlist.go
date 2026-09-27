package analyze

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultNodeMapURL = "https://raw.githubusercontent.com/ltdrdata/ComfyUI-Manager/main/extension-node-map.json"

var nodeMapURL = defaultNodeMapURL

var defaultAllowlist = []string{
	"https://github.com/kijai/ComfyUI-KJNodes",
	"https://github.com/Kosinkadink/ComfyUI-VideoHelperSuite",
	"https://github.com/city96/ComfyUI-GGUF",
	"https://github.com/ltdrdata/ComfyUI-Impact-Pack",
	"https://github.com/ltdrdata/ComfyUI-Impact-Subpack",
	"https://github.com/Fannovel16/comfyui_controlnet_aux",
	"https://github.com/yolain/ComfyUI-Easy-Use",
	"https://github.com/kijai/ComfyUI-WanVideoWrapper",
	"https://github.com/Lightricks/ComfyUI-LTXVideo",
	"https://github.com/cubiq/ComfyUI_essentials",
	"https://github.com/Kosinkadink/ComfyUI-AnimateDiff-Evolved",
	"https://github.com/FizzleDorf/ComfyUI_FizzNodes",
	"https://github.com/WASasquatch/was-node-suite-comfyui",
	"https://github.com/pythongosssss/ComfyUI-Custom-Scripts",
	"https://github.com/rgthree/rgthree-comfy",
	"https://github.com/SethRobinson/comfyui-workflow-to-api-converter-endpoint",
}

var (
	mapMu    sync.Mutex
	mapCache map[string]string // class_type -> git url
	mapAt    time.Time
)

func UseEmptyNodeMap() {
	mapMu.Lock()
	mapCache = map[string]string{}
	mapAt = time.Now().Add(24 * time.Hour)
	mapMu.Unlock()
}

func NormalizeAllowlist(extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = normalizeGit(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range defaultAllowlist {
		add(s)
	}
	for _, s := range extra {
		add(s)
	}
	return out
}

func PackAllowed(git string, allow []string) bool {
	git = normalizeGit(git)
	if git == "" {
		return false
	}
	if len(allow) == 0 {
		allow = defaultAllowlist
	}
	for _, a := range allow {
		if normalizeGit(a) == git {
			return true
		}
	}
	return false
}

func LookupPack(classType string) (pack, git string, ok bool) {
	m := nodeClassMap()
	git, ok = m[classType]
	if !ok {
		return "", "", false
	}
	return PackName(git), git, true
}

func nodeClassMap() map[string]string {
	mapMu.Lock()
	defer mapMu.Unlock()
	if mapCache != nil && time.Since(mapAt) < 6*time.Hour {
		return mapCache
	}
	m, err := fetchNodeMap(nodeMapURL)
	if err != nil || len(m) == 0 {
		if mapCache != nil {
			return mapCache
		}
		return map[string]string{}
	}
	mapCache = m
	mapAt = time.Now()
	return mapCache
}

func fetchNodeMap(url string) (map[string]string, error) {
	cl := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "OpenComfy")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("node map %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for git, v := range payload {
		for _, class := range packClasses(v) {
			if class != "" {
				out[class] = git
			}
		}
	}
	return out, nil
}

func packClasses(v any) []string {
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			return nil
		}
		if _, ok := t[0].(string); ok {
			return stringList(t)
		}
		return stringList(t[0])
	default:
		return nil
	}
}

func normalizeGit(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "git+")
	if i := strings.Index(s, "github.com/"); i >= 0 {
		s = "https://github.com/" + strings.TrimPrefix(s[i+len("github.com/"):], "/")
	}
	return s
}

func PackName(git string) string {
	git = strings.TrimSuffix(git, ".git")
	if i := strings.LastIndex(git, "/"); i >= 0 {
		return git[i+1:]
	}
	return git
}
