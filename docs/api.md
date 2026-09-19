# API

Base: `http://<host>:8788/v1` (aliases without `/v1` exist for holix-media).

Errors:

```json
{"error": {"message": "...", "type": "invalid_request_error", "code": "model_not_found", "param": "model"}}
```

## Models

`GET /v1/models` — OpenAI list plus `architecture`, `pricing` (dummy USD from YAML), `supported_parameters` as a **string array**.

`GET /v1/images/models` / `GET /v1/videos/models` — OpenRouter-shaped capability maps.

Strip `openai/` and `opencomfy/` prefixes on model ids.

## Images

`POST /v1/images/generations`

OpenAI fields plus `negative_prompt`, `seed`, `width`/`height`. `stream: true` → 400. `style`/`moderation` ignored.

Default `response_format`: `b64_json` if `public_base_url` is empty, else HMAC `url`.

## Videos

`POST /v1/videos` always **200** `{status: queued}` unless 256 active jobs (then 429).

`GET /v1/videos/{id}` on `completed` includes absolute HMAC `url` (holix-media does not call `/content`).

`input_reference` (multipart file or `{image_url: "data:..."}`) aliases to YAML `input_image`. `file_id` → 400.

## Files

`GET /v1/files/{id}?exp=&sig=` — HMAC, no Bearer.

## Chat

`POST /v1/chat/completions` with an image model runs the image pipeline. Video model → 400. `stream` → 400.
