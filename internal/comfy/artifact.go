package comfy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

type Artifact struct {
	Filename  string
	Subfolder string
	Type      string
	Format    string
	Kind      string // video | image | gif
}

func PickArtifact(history map[string]any, outputNode, modality, outputMIME string) (Artifact, error) {
	outputs, _ := history["outputs"].(map[string]any)
	if outputs == nil {
		if o, ok := history[outputNode].(map[string]any); ok {
			outputs = map[string]any{outputNode: o}
		}
	}
	var nodes []string
	if outputNode != "" {
		nodes = []string{outputNode}
	} else {
		for k := range outputs {
			nodes = append(nodes, k)
		}
	}
	var cands []Artifact
	for _, n := range nodes {
		node, _ := outputs[n].(map[string]any)
		if node == nil {
			continue
		}
		animated := false
		switch t := node["animated"].(type) {
		case []any:
			if len(t) > 0 {
				animated, _ = t[0].(bool)
			}
		case bool:
			animated = t
		}
		for _, key := range []string{"videos", "gifs", "images", "video"} {
			for _, m := range asMaps(node[key]) {
				a := Artifact{
					Filename:  str(m["filename"]),
					Subfolder: str(m["subfolder"]),
					Type:      str(m["type"]),
					Format:    str(m["format"]),
				}
				if a.Type == "" {
					a.Type = "output"
				}
				a.Kind = classify(a)
				if animated && a.Kind == "image" && filepath.Ext(a.Filename) == ".mp4" {
					a.Kind = "video"
				}
				if animated && a.Kind == "image" && strings.Contains(strings.ToLower(a.Filename), ".mp4") {
					a.Kind = "video"
				}
				cands = append(cands, a)
			}
		}
	}
	wantVideo := modality == "video" || strings.HasPrefix(outputMIME, "video/")
	if wantVideo {
		for _, a := range cands {
			if a.Kind == "video" {
				return a, nil
			}
		}
		return Artifact{}, fmt.Errorf("no_output")
	}
	for _, a := range cands {
		if a.Kind == "image" {
			return a, nil
		}
	}
	for _, a := range cands {
		if a.Kind == "gif" {
			return a, nil
		}
	}
	return Artifact{}, fmt.Errorf("no_output")
}

func asMaps(v any) []map[string]any {
	switch t := v.(type) {
	case []any:
		var out []map[string]any
		for _, it := range t {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{t}
	default:
		return nil
	}
}

func classify(a Artifact) string {
	ext := strings.ToLower(filepath.Ext(a.Filename))
	fmtm := strings.ToLower(a.Format)
	name := strings.ToLower(a.Filename)
	if strings.HasPrefix(fmtm, "video/") || ext == ".mp4" || ext == ".webm" || ext == ".mkv" || ext == ".mov" || ext == ".avi" || strings.Contains(name, ".mp4") {
		return "video"
	}
	if ext == ".gif" {
		return "gif"
	}
	return "image"
}

func (c *Client) View(ctx context.Context, a Artifact) ([]byte, error) {
	q := url.Values{}
	q.Set("filename", a.Filename)
	q.Set("subfolder", a.Subfolder)
	q.Set("type", a.Type)
	resp, err := c.do(ctx, http.MethodGet, "/view?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("view %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
