# OpenComfy engineering rules

Product design lives in [`docs/DESIGN.md`](docs/DESIGN.md). This file is the **engineering contract**: every change must keep the architecture clean, fast, and testable. English is the source of truth.

If a change fights these rules, change the design first — do not grow a workaround.

## 1. What this process is

OpenComfy is a **single static Go binary** (`CGO_ENABLED=0`) that is the **exclusive** HTTP client of one ComfyUI. It is an OpenAI-compatible media gateway, not a workflow editor, not a ComfyUI installer, and not a Holix plugin.

Normative job protocol: OpenAI Images (sync) + OpenAI Videos (Sora-style async). Queue in v1 is **ComfyUI only** — no Redis, no Celery, no extra GPU schedulers.

Clients (OpenAI SDK, LiteLLM, MikroLLM, holix-media) talk REST. Holix is a client only.

## 2. Composition root and DI

**DI pattern: constructor injection at the composition root.** No DI container (`fx`, `wire`, `dig`), no service locator, no `init()` that registers HTTP routes or global clients.

| Place | Role |
|-------|------|
| `cmd/opencomfy/main.go` | Composition root: load config, construct services, start `http.Server` |
| `httpapi.New(...)` | HTTP adapter: receives already-built collaborators |
| `engine.Run(...)` | Application use-case: one generation |

Rules:

1. Construct collaborators in `main` (or a tiny `newApp` helper in `cmd/`) and pass them in. Today: `config` → `auth` → `catalog` → `comfy.Client` → `jobs` → `files` → `queue.Admission` → `importwf.Downloads` → `httpapi.New`.
2. Dependencies flow **inward**. `httpapi` may call `engine`, `importwf`, `auth`, `catalog`. Domain packages must not import `httpapi`.
3. Define **small interfaces at the consumer** when a second implementation or a fake is needed (`ComfyQueue`, `JobStore`, `FileStore`). Do not invent interfaces “for SOLID” with a single production type.
4. No package-level mutable clients, downloaders, or HTTP servers. Package `var` is allowed only for pure tables (allowlists, generic filenames) or test stubs that tests reset.
5. `context.Context` is the first argument of any I/O. Deadlines come from `model.timeout_s + 15s`, not from `http.Server.WriteTimeout`.

## 3. Layers (allowed imports)

```
cmd/opencomfy          composition root
        │
        ▼
internal/httpapi       transport (OpenAI HTTP, chat shim, MCP, /import)
        │
        ├──────────────► internal/engine      one ComfyUI run (inject → prompt → poll → view)
        ├──────────────► internal/importwf    facade + provision (bounded context)
        │                      ├─ convert         UI graph → API graph
        │                      ├─ analyze         missing nodes / weights
        │                      ├─ hf              Hub resolve + weight downloads
        │                      └─ catalogimport   userdata → models.yaml
        │
        ▼
internal/workflow      maps_to inject, prompt clean
internal/catalog       models.yaml, validate, EnsureSaver
internal/comfy         ComfyUI HTTP adapter
internal/auth          keys, RPM, allowlist
internal/jobs          video job JSON store
internal/files         HMAC file URLs
internal/queue         in-process admission
internal/config        YAML config
internal/ids           UUID / public ids
```

**Forbidden edges**

- `catalog`, `workflow`, `comfy`, `auth`, `jobs`, `files`, `queue`, `ids`, `config` → `httpapi` or `engine`
- `engine` → `httpapi`, `importwf`, `jobs` (engine returns bytes + `PromptResult`; HTTP/jobs persist)
- `importwf` and its subpackages → `httpapi`, `engine` (import must not queue `/prompt`)
- subpackages of `importwf` must not import the `importwf` root
- `analyze` may import `convert` and `hf`; `hf` must not import `analyze` (map `ModelNeed` → `hf.WeightRef` at the root)
- A new package must not import `net/http` unless it is `httpapi`, `comfy`, or an `importwf` subpackage that talks to Hugging Face or the node-pack map (`hf`, `analyze`)

Keep the graph acyclic. `go list` / compile is the check.

## 4. SOLID in this repo

| Principle | How we apply it |
|-----------|-----------------|
| **S** | One reason to change per type. HTTP mapping stays in `httpapi`. Graph inject stays in `workflow`. Comfy wire format stays in `comfy`. HF resolve stays out of `engine`. |
| **O** | New models = YAML `maps_to`, not `switch` on family names. New Comfy node packs for provision = allowlist, not a new installer core. |
| **L** | Adapters honor the same contracts as production (`QueuePrompt` returns requested **and** returned `prompt_id`; `/queue` item `[1]` is `prompt_id`). |
| **I** | Do not grow `httpapi.Server` into a god object with business logic. Handlers parse, authorize, call one use-case, write JSON. |
| **D** | `engine.Run` depends on `engine.Runner`. `*comfy.Client` is the production implementation. New backends are new adapters, not `if kind ==` inside inject. |

## 5. Package contracts

### `cmd/opencomfy`

Flags, signals, `http.Server` timeouts, exit **2** on missing config. No business rules.

`http.Server` (required — long Flux/video writes):

- `ReadHeaderTimeout` 10s
- `ReadTimeout` 0, `WriteTimeout` 0
- `IdleTimeout` 120s

### `internal/httpapi`

OpenAI/MCP/import HTTP only. Dual routes with and without `/v1`. Auth via `protect()`. `/health` unauthenticated **200** even without a key. `/ready` is ComfyUI liveness, not a process killer.

Do not embed a second copy of inject/poll/HF here. Chat shim maps chat → `images` and stops.

### `internal/engine`

One function-shaped use-case: load graph → upload refs → `workflow.BuildValues` + `Inject` → `EnsureSaver` → `QueuePrompt` → poll history → `PickArtifact`. No YAML I/O besides `catalog.LoadGraph`. No Hugging Face.

### `internal/workflow`

Explicit `maps_to[{node,field,path}]` only. No family heuristics. `CleanChatPrompt` belongs here (MikroLLM wrappers). `IgnoreOpenAI` must keep `n`, `messages`, `input_*`, `prompt`, `negative_prompt` from being injected as Extra graph fields.

### `internal/catalog`

Named models, aliases, size/quality, `ValidateModel`, `EnsureSaver`. One file per reason to change: `load.go`, `graph.go`, `validate.go`, `saver.go`, `schema.go`. Invalid **one** model is skipped (log + continue); do not fail the whole process unless **zero** models remain.

UI graphs (`nodes`/`links`) are rejected at catalog load. API graphs with leftover UUID `class_type` are rejected at convert and at `engine.Run`.

### `internal/comfy`

The only package that speaks ComfyUI HTTP: `/prompt`, `/history`, `/queue`, `/view`, `/upload/image`, `/object_info`, `/userdata`, `/workflow/convert`. Methods are split by area (`prompt.go`, `queue.go`, `artifact.go`, `upload.go`, `convert.go`, `userdata.go`). Reuse this client. Do not `http.Get` ComfyUI from `httpapi` or `importwf`.

`/queue` list item is `[number, prompt_id, ...]`. Poll history with the **returned** `prompt_id`. Non-UUID ids are replaced with `ids.UUID()` before `QueuePrompt`.

### `internal/importwf` (bounded context)

Variant C: save JSON to ComfyUI userdata, analyze missing classes/weights, git-clone **allowlisted** packs only, disk-reuse then Hugging Face, then existing catalog import.

Hard rules:

- Never `git clone` a URL that is not on `comfyui.node_allowlist` (default list in code is still an allowlist).
- Never download weights as a side effect of `POST /prompt`.
- Never reboot ComfyUI.
- Import gate: prompt required; LoadImage/LoadVideo requires `input_image` / `input_video`.
- Disk search by exact relative path, then **unique** basename; generic names (`model.safetensors`) need `model_map` or an exact path.
- Hub search is by **repo name**; official org index is the fallback. Prefer `Comfy-Org` / `Lightricks` over random forks. Do not substitute a different quant (fp8 ≠ nvfp4).

Root package is the facade (`List`, `ImportSelected`, `Provision`, `Downloads`) plus node-pack install. Work lives in subpackages:

- `convert` — UI/API convert, subgraph expand, frontend-only expand
- `analyze` — missing classes and weights, node allowlist
- `hf` — Hub resolve, on-disk reuse, download jobs
- `catalogimport` — selected userdata workflows into `models.yaml`

Do not add a new concern to the root. Subpackages still must not import `httpapi`.

### `internal/auth`, `jobs`, `files`, `queue`, `ids`, `config`

Keep them boring. Auth takes header strings (`BearerToken`, `ClientHost`), not `*http.Request`: Bearer `sk-` or `X-Api-Key`, SHA-256, tumbling RPM, model allowlist. Files: HMAC GET **without** Authorization. Jobs: atomic JSON, mutex per id, TTL. Queue: in-process admission (`max_in_flight` / `max_waiting`), constructed in `main`.

## 6. Performance

- One process, exclusive ComfyUI. Do not open extra ComfyUI sessions or interrupt unless `allow_interrupt`.
- Poll interval from config (default 2s). Do not busy-loop `/history`.
- Sync image holds the HTTP connection until ComfyUI finishes — that is why `WriteTimeout` is 0.
- Admission: if no slot and waiting is full → **429** + `Retry-After`. Do not unbounded-goroutine `/prompt`.
- Video: **200 queued** immediately; worker waits for a slot. Completed JSON **must** include an absolute HMAC `url` (`public_base_url` or `http://127.0.0.1:<boundPort>`).
- Walk `models_dir` only for missing weights, skip `.git` / `.part` / empty files. Cache official HF org indexes per process; reset in tests.
- `CGO_ENABLED=0`, no ORM, no extra HTTP frameworks. Stdlib `net/http` + `gopkg.in/yaml.v3` only unless a dependency is justified in the PR.

## 7. Reuse — do not rebuild

Reuse, in order:

1. Stdlib (`net/http`, `encoding/json`, `context`, `os/exec` for allowlisted `git clone`).
2. Existing internal packages (`comfy.Client`, `workflow.Inject`, `catalog.EnsureSaver`, `engine.Run`).
3. ComfyUI itself (`POST /workflow/convert` when installed; local subgraph expand as fallback).
4. Hugging Face Hub HTTP API (no `huggingface_hub` Python).

Do **not**:

- Add Gin/Echo/Fiber, a DI container, Redis, or a worker pool library for v1.
- Fork Python OpenAI-Comfy adapters.
- Copy image_bot family heuristics (`guess_family`).
- Treat `item[0]` of `/queue` as `prompt_id`.
- Git-clone arbitrary custom nodes (RCE).
- Auto-install weights during generation.

## 8. No god types

A file or type is too big when it mixes **transport + domain + I/O adapters**. Split along those seams, not by “helper.go”.

Practical limits (soft, but PRs that grow past them must split):

- HTTP handler function: parse → authorize → one use-case → write. No nested Comfy poll loops.
- `httpapi.Server` holds collaborators only (composition). New endpoints get a new file, not more methods that call ComfyUI directly.
- The `/import` page is `import_page.html`, embedded once. Do not grow a SPA there, and do not add another HTML page in Go.
- New ComfyUI routes go on `comfy.Client`, not as ad-hoc `http.NewRequest` in handlers.

## 9. Testing

- Prefer `httptest` fakes of ComfyUI/HF over live GPU.
- Catalog fixtures: `testdata/workflows` (no real checkpoints).
- Dual-route tests: `/v1/...` and `/...`.
- HMAC file GET must succeed **without** `Authorization`.
- Video completed JSON must contain an absolute `url`.
- `WriteTimeout` of the process server must stay 0.
- Import/analyze tests must cover subgraph loaders with empty `inputs` + `widgets_values` lists.
- Tests that mutate package-level HF base URL must restore it (`t.Cleanup`).

## 10. Product invariants (do not regress)

- Exclusive ComfyUI; do not share `:8188` with image_bot.
- `maps_to` is the only inject mechanism.
- Images default `b64_json` when `public_base_url` is empty, else HMAC `url`.
- Strip `openai/` and `opencomfy/` model prefixes.
- Missing config → exit **2**; systemd `RestartPreventExitStatus=2`.
- `/health` 200 without API key. Empty `GET /v1/models` with healthy `/health` is usually a bad key or base URL that already includes `/v1`.
- FluxKleinOneNode is frontend-only: import must expand via `/flux_klein/workflow_t2i` (or equivalent), never queue the noop node.
- Do not reboot ComfyUI from OpenComfy.

## 11. How to change the architecture

1. Update `docs/DESIGN.md` if the **product** changes (protocol, clients, non-goals).
2. Update this file if **package boundaries, DI, or invariants** change.
3. Then code. PRs that only add code against a silent new pattern will be rejected.

Compliance check for a PR: new imports respect §3, constructors stay in `cmd` / `httpapi.New`, no new globals, no new god file, tests cover the seam you touched.

## 12. Current gaps (do not deepen)

These exist today. New code must not make them worse; touch-and-split is allowed.

| Gap | Where | Rule it violates |
|-----|--------|------------------|
| Package-level HF API base + official-org cache | `importwf/hf/hf.go` | §2.4 — tests must `t.Cleanup` reset |
| Variant C (nodes/weights) was a DESIGN non-goal | `importwf` + `/v1/comfy/provision` | Product evolved; keep it **off** the `/prompt` path |
| Prometheus text is written in the HTTP handler | `httpapi` `metrics` | DESIGN once listed `internal/metrics`; three gauges do not justify a package |

Constructor DI in `cmd/opencomfy` → `httpapi.New` is the pattern to copy. Stdlib + `yaml.v3` only (`go.mod`).
