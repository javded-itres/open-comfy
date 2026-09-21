package importwf

import (
	"context"
	"fmt"

	"github.com/javded-itres/open-comfy/internal/comfy"
)

const frontendOnlyMsg = "this workflow is a ComfyUI frontend widget (JS generate); API only returns an empty 64x64 image"

func hasClass(graph map[string]any, class string) bool {
	for _, v := range graph {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		if node["class_type"] == class {
			return true
		}
	}
	return false
}

func ExpandFrontend(ctx context.Context, client *comfy.Client, graph map[string]any) (map[string]any, error) {
	if graph == nil || !hasClass(graph, "FluxKleinOneNode") {
		return graph, nil
	}
	if client == nil {
		return nil, fmt.Errorf("%s", frontendOnlyMsg)
	}
	var t2i map[string]any
	if err := client.GetJSON(ctx, "/flux_klein/workflow_t2i", &t2i); err != nil {
		return nil, fmt.Errorf("%s (%v)", frontendOnlyMsg, err)
	}
	if len(t2i) == 0 {
		return nil, fmt.Errorf("%s", frontendOnlyMsg)
	}
	overlayLoaders(t2i, graph)
	return t2i, nil
}

func overlayLoaders(dst, src map[string]any) {
	clip, vae, unet := "", "", ""
	for _, v := range src {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		in, _ := node["inputs"].(map[string]any)
		if in == nil {
			continue
		}
		switch ct {
		case "CLIPLoader":
			if s, ok := in["clip_name"].(string); ok {
				clip = s
			}
		case "VAELoader":
			if s, ok := in["vae_name"].(string); ok {
				vae = s
			}
		case "UNETLoader":
			if s, ok := in["unet_name"].(string); ok {
				unet = s
			}
		case "DiffusionModelLoaderKJ":
			if s, ok := in["model_name"].(string); ok {
				unet = s
			}
		}
	}
	for _, v := range dst {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		in, _ := node["inputs"].(map[string]any)
		if in == nil {
			continue
		}
		switch ct {
		case "CLIPLoader":
			if clip != "" {
				in["clip_name"] = clip
			}
		case "VAELoader":
			if vae != "" {
				in["vae_name"] = vae
			}
		case "UNETLoader":
			if unet != "" {
				in["unet_name"] = unet
			}
		}
	}
}
