# Workflows

Import from the ComfyUI userdata library (UI `{nodes, links}` graphs).

ComfyUI itself has **no backend convert API**. `File → Export Workflow (API)` is frontend `graphToPrompt()` (`ComfyUI_frontend` `src/utils/executionUtil.ts`): it walks execution order, expands subgraphs via `ExecutableNodeDTO.getInnerNodes()`, writes widget values and resolved links, and drops muted/virtual nodes (Note, Reroute, Primitive).

OpenComfy therefore:

1. Prefers ComfyUI `POST /workflow/convert` (custom node that ports the same Save-API logic in Python, using live `/object_info`). Install on the GPU host:

```bash
cd /path/to/ComfyUI/custom_nodes
git clone https://github.com/SethRobinson/comfyui-workflow-to-api-converter-endpoint
# restart ComfyUI
```

2. If that endpoint is missing, falls back to the local expander (`definitions.subgraphs` → node ids `outer:inner`).
3. After convert, widget values from the subgraph **instance** (folder-prefixed `clip_name` / `unet_name`, seed, size) are copied onto the inner nodes. The converter otherwise keeps blueprint basenames that fail ComfyUI `value_not_in_list`.

A leftover UUID `class_type` cannot be queued (`Node '' not found`).

List and import **selected** graphs (multi-select). UI: `/import`. API: `GET /v1/comfy/workflows`, `POST /v1/comfy/import` `{"names":["a.json","b.json"]}`.

### Upload a JSON (variant C)

1. `POST /v1/comfy/analyze` `{workflow}` or `{name}` — missing `class_type` vs `/object_info`, missing combo weights vs installed files. If `comfyui.models_dir` is set, a combo miss is treated as present when the same filename already exists anywhere under that tree (unique basename, or the expected relative path). OpenComfy then symlinks it into the folder ComfyUI expects (`diffusion_models`, `text_encoders`, `vae`, …). Ambiguous duplicates and generic names like `model.safetensors` stay missing. Hits appear in `reused_models`.
2. `POST /v1/comfy/provision` `{name, workflow, overwrite, install_nodes, install_models}` — writes ComfyUI userdata `workflows/<name>`. Custom nodes are **git-cloned only** if `comfyui.custom_nodes_dir` is set **and** the pack is on `comfyui.node_allowlist`. Weights with `install_models: true`: **disk search first** (same basename walk; Windows `\` paths normalized), then Hugging Face. Hub `search` is by **repo name**, so OpenComfy also scans official orgs (`Comfy-Org`, `Lightricks`, `comfyanonymous`, `Kijai`) for the exact filename (e.g. `qwen3vl_32b_minimax_h3_int8_convrot.safetensors` inside `Comfy-Org/MiniMax-H3`). Explicit `comfyui.model_map` still wins. Downloads land in the matching Comfy folder, preserving workflow subpaths. Poll `GET /v1/comfy/downloads/{id}`. Token: env `HF_TOKEN` or `comfyui.hf_token_env`. Optional `hf_allowlist` (org or `org/repo`). Generic names like `model.safetensors` need `model_map` unless the exact relative path is already on disk. OpenComfy does not reboot ComfyUI.
3. After a ComfyUI restart (if nodes were cloned), use the existing import into the OpenComfy catalog.

The `/import` page has a file picker for this flow.

A workflow cannot be imported if **prompt** is missing, or if the graph has LoadImage/LoadVideo but **reference** was not mapped.

```bash
opencomfy -config /path/to/config.yaml -list-comfy
opencomfy -config /path/to/config.yaml -import-comfy "MiniMax H3 Talking Avatar.json"
```

Place JSON in `/etc/opencomfy/workflows/` and point `models.yaml` `workflow:` at the basename.

Each overridable API field needs explicit `maps_to: [{node, field, path?}]`. No family heuristics.

Golden fixtures used in CI (no checkpoints):

- `testdata/workflows/image_flux2_text_to_image_9b.json`
- `testdata/workflows/video_ltx2_i2v.json`

VHS `VHS_VideoCombine` writes MP4 under history `gifs[]` with `format: video/h264-mp4`. OpenComfy classifies by extension and format, not `output_mime`.
