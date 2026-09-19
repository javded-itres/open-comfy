# Workflows

Export from ComfyUI: **Save (API Format)**, not the UI `{nodes, links}` graph.

Place JSON in `/etc/opencomfy/workflows/` and point `models.yaml` `workflow:` at the basename.

Each overridable API field needs explicit `maps_to: [{node, field, path?}]`. No family heuristics.

Golden fixtures used in CI (no checkpoints):

- `testdata/workflows/image_flux2_text_to_image_9b.json`
- `testdata/workflows/video_ltx2_i2v.json`

VHS `VHS_VideoCombine` writes MP4 under history `gifs[]` with `format: video/h264-mp4`. OpenComfy classifies by extension and format, not `output_mime`.
