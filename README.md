# OpenComfy

**English** · [Русский](README.ru.md)

OpenAI-compatible REST gateway in front of **[ComfyUI](https://www.comfy.org/)**. Named models (`flux-dev`, `minimax-hailuo-02`) map to workflow JSON via YAML. Clients (OpenAI SDK, LiteLLM, [MikroLLM](https://github.com/javded-itres/mikrollm), [holix-media](https://github.com/javded-itres/holix-media)) use a custom `base_url`.

MIT. Go 1.23+, CGO off, single static binary.

This is **not** a Holix agent extension. Holix talks to OpenComfy as just another OpenAI-compatible media endpoint.

## Features

- `POST /v1/images/generations` — sync image (default `b64_json`, or HMAC `url`)
- `POST /v1/videos` — async job (`200` + `queued`); poll `GET /v1/videos/{id}`; bytes at `/content`
- Completed video JSON always includes an absolute HMAC `url` (holix-media never calls `/content`)
- `GET /v1/models` — catalog, dummy USD prices, overridable parameters (`parameters` / `required_parameters` / `input_schema`)
- MCP at `POST /mcp` — one `generate_<id>` tool per workflow; required fields (e.g. `input_image`) appear in the tool schema
- Swagger UI at `/docs` (`/openapi.json`)
- Bearer `sk-…` or `X-Api-Key`, RPM, model allowlist
- systemd or Docker (`network_mode: host`); ComfyUI stays on the host

## Requirements

- A running **ComfyUI** (`127.0.0.1:8188` recommended). OpenComfy should be the **only** client of that instance (do not share with Telegram bots on the same port).
- ComfyUI **≥ 0.3.7** (client `prompt_id` must be a UUID).
- Workflows imported from ComfyUI userdata (UI graphs). Upload JSON on `/import` (`POST /v1/comfy/provision`): save to ComfyUI, analyze missing nodes/weights, git-clone **allowlisted** custom nodes only. Then the existing import into the OpenComfy catalog. See [docs/workflows.md](docs/workflows.md).
- Code changes follow [RULES.md](RULES.md) (layers, constructor DI, SOLID). Product design: [docs/DESIGN.md](docs/DESIGN.md).

## Quick start

```bash
git clone https://github.com/javded-itres/open-comfy.git
cd open-comfy
make build

# user-space (no root)
mkdir -p ~/.config/opencomfy ~/.local/share/opencomfy ~/.local/bin
export OPENCOMFY_DATA="$HOME/.local/share/opencomfy"
./opencomfy -init -config ~/.config/opencomfy/config.yaml
# prints a sk- key once — save it
cp opencomfy ~/.local/bin/
cp deploy/opencomfy.user.service ~/.config/systemd/user/opencomfy.service
systemctl --user daemon-reload
systemctl --user enable --now opencomfy
# persist after logout:
# loginctl enable-linger "$USER"
```

Listen `:8788`. Health: `http://127.0.0.1:8788/health`. Docs: `http://127.0.0.1:8788/docs`.

### systemd (root)

```bash
sudo OPENCOMFY_DATA=/var/lib/opencomfy ./opencomfy -init -config /etc/opencomfy/config.yaml
sudo cp opencomfy /usr/local/bin/
sudo cp deploy/opencomfy.service /etc/systemd/system/
sudo systemctl enable --now opencomfy
```

Missing config exits **2**; the unit has `RestartPreventExitStatus=2`.

### Docker

ComfyUI must already run on the host. Create config **on the host first**:

```bash
export OPENCOMFY_DATA=/var/lib/opencomfy
sudo ./opencomfy -init -config /etc/opencomfy/config.yaml
docker compose up --build
```

Compose uses `network_mode: host`. The image is `scratch`; healthcheck is `/opencomfy -healthcheck`, not curl.

## Configuration

| File | Role |
|------|------|
| `config.yaml` | listen, ComfyUI URL, timeouts, `public_base_url` |
| `keys.yaml` | API keys (`key:` or `key_sha256`), RPM, model allowlist |
| `models.yaml` | model id → workflow + `maps_to` (node/field) + pricing |
| `workflows/*.json` | ComfyUI **API format** graphs |

Default paths: `/etc/opencomfy/` + `/var/lib/opencomfy/`. User-space: `~/.config/opencomfy/` + `OPENCOMFY_DATA`.

Env overrides: `OPENCOMFY_CONFIG`, `OPENCOMFY_LISTEN`, `OPENCOMFY_COMFYUI_URL`, `OPENCOMFY_PUBLIC_BASE_URL`, `OPENCOMFY_DATA`, `API_KEYS`.

Set `public_base_url` to the URL clients use (e.g. `http://192.168.88.252:8788`). If empty, file URLs become `http://127.0.0.1:<port>/…` and only work on the GPU box.

### models.yaml

Each API field you allow must list `maps_to`. Nothing is guessed from node class names.

```yaml
models:
  - id: minimax-hailuo-02
    name: Minimax H3
    modality: video
    workflow: minimax_h3_t2v.json
    output_mime: video/mp4
    output_node: "92"          # SaveVideo / VHS node id
    timeout_s: 900
    parameters:
      - name: prompt
        type: string
        required: true
        maps_to:
          - {node: "140:131", field: prompt}
      - name: seconds
        type: number
        default: 6
        maps_to:
          - {node: "140:133", field: value}
```

Replace toy graphs from `-init` with your API-export JSON. Node ids must exist or the process **refuses to start**.

More: [docs/workflows.md](docs/workflows.md), [docs/install.md](docs/install.md), [docs/api.md](docs/api.md).

## API

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8788/v1", api_key="sk-...", timeout=200)
client.images.generate(model="toy-image", prompt="neon city")
job = client.videos.create(model="minimax-hailuo-02", prompt="waves at sunset", seconds="6")
```

| Method | Path |
|--------|------|
| GET | `/health`, `/ready` |
| GET | `/docs` Swagger UI |
| GET | `/v1/models` |
| POST | `/v1/images/generations` |
| POST | `/v1/videos` → `{status: queued}` |
| GET | `/v1/videos/{id}` — `completed` includes `url` |
| GET | `/v1/videos/{id}/content` |
| POST | `/v1/chat/completions` (image shim only) |

Auth: `Authorization: Bearer sk-...` or `X-Api-Key`. In Swagger click **Authorize**.

HMAC file URLs (`GET /v1/files/{id}?exp=&sig=`) work **without** a Bearer header (holix-media downloads that way).

Video drop-in for holix-media: T2V model, job within the client poll budget (180 s unless you patch the client), completed JSON has `url`.

## MikroLLM

MikroLLM has an **OpenComfy** backend kind. Add a server:

- kind: `opencomfy`
- URL: `http://<gpu>:8788` (no `/v1`)
- token: the OpenComfy `sk-…`

Refresh catalogs, connect aliases on **Models**, then call MikroLLM `/v1/images/generations` and `/v1/videos` as usual.

## Import workflows from ComfyUI

Saved graphs in the ComfyUI UI (`user/default/workflows`) are **UI format**. OpenComfy needs **API format**. The importer lists `/userdata?dir=workflows`, converts via `/object_info`, infers `prompt` / `seed` / `width` / `height` / `seconds` / `input_image`, and appends models to `models.yaml`.

Web UI (multi-select): [http://127.0.0.1:8788/import](http://127.0.0.1:8788/import) — paste the API key, tick workflows, import. Rows without a detected **prompt**, or without a **reference** when the graph has LoadImage/LoadVideo, are blocked.

```bash
opencomfy -config ~/.config/opencomfy/config.yaml -list-comfy
opencomfy -config ~/.config/opencomfy/config.yaml -import-comfy "MiniMax H3 Talking Avatar.json" "ND_Flux2_Klein_AIO_v1.json"
```

Existing model ids are skipped. `models.yaml.bak` is written on change.

## Develop

```bash
go test ./...
make build
```

License: [MIT](LICENSE).
