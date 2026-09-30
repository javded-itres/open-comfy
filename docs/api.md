# API

Base: `http://<host>:8788/v1` (aliases without `/v1` exist for holix-media).

Errors:

```json
{"error": {"message": "...", "type": "invalid_request_error", "code": "model_not_found", "param": "model"}}
```

## Models

`GET /v1/models` — OpenAI list plus `architecture`, `pricing` (dummy USD from YAML), `supported_parameters` as a **string array**, plus `parameters` (name/type/required/default/min/max/enum), `required_parameters`, JSON Schema `input_schema`, and `mcp_tool` (`generate_<id>`).

`GET /v1/images/models` / `GET /v1/videos/models` — OpenRouter-shaped capability maps.

Strip `openai/` and `opencomfy/` prefixes on model ids.

## Images

`POST /v1/images/generations`

OpenAI fields plus `negative_prompt`, `seed`, `width`/`height`. `stream: true` → 400. `style`/`moderation` ignored.

Default `response_format`: `b64_json` if `public_base_url` is empty, else HMAC `url`.

If ComfyUI is already generating (or another OpenComfy job is active), the call does **not** wait. It returns **200**:

```json
{"id":"img_…","object":"image","status":"queued","queue_ahead":2,"polling_url":"http://127.0.0.1:8788/v1/images/img_…"}
```

`queue_ahead` is how many generations run before this one (ComfyUI's queue plus local jobs not submitted yet). Poll `GET /v1/images/{id}` until `completed`; then `data` is the usual `b64_json` or `url` array. A free GPU still returns the OpenAI image object directly.

## Videos

`POST /v1/videos` always **200** `{status: queued, queue_ahead}` unless 256 active jobs (then 429). `queue_ahead` is how many generations run before this job.

`GET /v1/videos/{id}` on `completed` includes absolute HMAC `url` (holix-media does not call `/content`).

Reference images map to YAML `input_image` (each `maps_to` is one Comfy `LoadImage`). `file_id` → 400. HTTP(S) URLs are rejected.

One image:

```json
"input_reference": { "image_url": "data:image/png;base64,...." }
```

or `input_image`: the same data URL. Multipart: field `input_reference` (file).

Several images (order = LoadImage nodes in `maps_to`):

```json
"input_images": [
  "data:image/png;base64,....",
  "data:image/jpeg;base64,...."
]
```

or `"input_reference": [ { "image_url": "data:..." }, { "image_url": "data:..." } ]`.

Multipart: repeat the file field `input_reference` (or `input_image` / `input_images`). Extra files beyond mapped nodes reuse the last name; missing files leave graph defaults.

## Files

`GET /v1/files/{id}?exp=&sig=` — HMAC, no Bearer.

## Chat

`POST /v1/chat/completions` with an image model runs the image pipeline. Video model → 400. `stream` → 400.

## MCP

`POST /mcp` — [MCP](https://modelcontextprotocol.io) Streamable HTTP, JSON-RPC 2.0. Same Bearer `sk-…` as REST.

| Method | Purpose |
|---|---|
| `initialize` / `ping` | handshake |
| `tools/list` | `list_models`, `get_model`, `get_video`, and **one `generate_<id>` tool per workflow** whose `inputSchema.required` matches the YAML (e.g. `input_image`) |
| `tools/call` | run those tools |

`input_image` is a data URL (`data:image/png;base64,…`). Aliases: `input_images`, `input_reference`, `input_references`. Video tools return a queued job; poll `get_video`.

Grok / Cursor example (`~/.grok/config.toml`):

```toml
[mcp_servers.opencomfy]
url = "http://192.168.88.252:8788/mcp"
enabled = true
headers = { "Authorization" = "Bearer sk-…" }
```
