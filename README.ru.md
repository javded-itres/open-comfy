# OpenComfy

[English](README.md) · **Русский**

OpenAI-совместимый REST-шлюз перед **[ComfyUI](https://www.comfy.org/)**. Имена моделей (`flux-dev`, `minimax-hailuo-02`) задаются в YAML и указывают на workflow JSON. Клиенты (OpenAI SDK, LiteLLM, [MikroLLM](https://github.com/javded-itres/mikrollm), [holix-media](https://github.com/javded-itres/holix-media)) ходят на свой `base_url`.

MIT. Go 1.23+, без CGO, один статический бинарь.

Это **не** расширение агента Holix. Holix использует OpenComfy как обычный OpenAI-совместимый медиа-эндпоинт.

## Возможности

- `POST /v1/images/generations` — синхронная картинка (`b64_json` или HMAC-`url`)
- `POST /v1/videos` — асинхронный джоб (`200` + `queued`); опрос `GET /v1/videos/{id}`; байты на `/content`
- У completed-видео в JSON всегда абсолютный HMAC-`url` (holix-media не вызывает `/content`)
- `GET /v1/models` — каталог, фиктивные цены USD, параметры (`parameters` / `required_parameters` / `input_schema`)
- MCP: `POST /mcp` — на каждый workflow свой tool `generate_<id>`; обязательные поля (например `input_image`) сразу в схеме
- Swagger UI: `/docs` (`/openapi.json`)
- Bearer `sk-…` или `X-Api-Key`, RPM, allowlist моделей
- systemd или Docker (`network_mode: host`); ComfyUI остаётся на хосте

## Требования

- Запущенный **ComfyUI** (лучше `127.0.0.1:8188`). OpenComfy должен быть **единственным** клиентом этого инстанса (не делить порт с Telegram-ботами).
- ComfyUI **≥ 0.3.7** (`prompt_id` — UUID).
- Workflow из библиотеки ComfyUI. На `/import` можно залить JSON (`POST /v1/comfy/provision`): сохранение в ComfyUI, анализ missing-нод/весов, git clone **только allowlist**. Дальше обычный импорт в каталог OpenComfy. См. [docs/workflows.md](docs/workflows.md).

## Быстрый старт

```bash
git clone https://github.com/javded-itres/open-comfy.git
cd open-comfy
make build

# без root
mkdir -p ~/.config/opencomfy ~/.local/share/opencomfy ~/.local/bin
export OPENCOMFY_DATA="$HOME/.local/share/opencomfy"
./opencomfy -init -config ~/.config/opencomfy/config.yaml
# один раз печатает sk- — сохраните
cp opencomfy ~/.local/bin/
cp deploy/opencomfy.user.service ~/.config/systemd/user/opencomfy.service
systemctl --user daemon-reload
systemctl --user enable --now opencomfy
# чтобы пережило logout:
# loginctl enable-linger "$USER"
```

Слушает `:8788`. Health: `http://127.0.0.1:8788/health`. Документация API: `http://127.0.0.1:8788/docs`.

### systemd от root

```bash
sudo OPENCOMFY_DATA=/var/lib/opencomfy ./opencomfy -init -config /etc/opencomfy/config.yaml
sudo cp opencomfy /usr/local/bin/
sudo cp deploy/opencomfy.service /etc/systemd/system/
sudo systemctl enable --now opencomfy
```

Нет конфига — код выхода **2**; в unit стоит `RestartPreventExitStatus=2`.

### Docker

ComfyUI уже должен работать на хосте. Конфиг создайте **на хосте до** `compose up`:

```bash
export OPENCOMFY_DATA=/var/lib/opencomfy
sudo ./opencomfy -init -config /etc/opencomfy/config.yaml
docker compose up --build
```

Compose: `network_mode: host`. Образ `scratch`; healthcheck — `/opencomfy -healthcheck`, не curl.

## Конфигурация

| Файл | Назначение |
|------|------------|
| `config.yaml` | listen, URL ComfyUI, таймауты, `public_base_url` |
| `keys.yaml` | ключи (`key:` или `key_sha256`), RPM, allowlist |
| `models.yaml` | id модели → workflow + `maps_to` (node/field) + цены |
| `workflows/*.json` | графы ComfyUI **API format** |

По умолчанию: `/etc/opencomfy/` и `/var/lib/opencomfy/`. Пользовательский режим: `~/.config/opencomfy/` и `OPENCOMFY_DATA`.

Переменные: `OPENCOMFY_CONFIG`, `OPENCOMFY_LISTEN`, `OPENCOMFY_COMFYUI_URL`, `OPENCOMFY_PUBLIC_BASE_URL`, `OPENCOMFY_DATA`, `API_KEYS`.

`public_base_url` — URL, который видят клиенты (например `http://192.168.88.252:8788`). Если пусто, ссылки на файлы будут `http://127.0.0.1:<порт>/…` и сработают только на самой GPU-машине.

### models.yaml

Каждое поле API, которое можно менять, должно иметь `maps_to`. Эвристик по типу ноды нет.

```yaml
models:
  - id: minimax-hailuo-02
    name: Minimax H3
    modality: video
    workflow: minimax_h3_t2v.json
    output_mime: video/mp4
    output_node: "92"
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

Toy-графы из `-init` замените своим API-export. Если node id нет в JSON, процесс **не стартует**.

Дальше: [docs/workflows.md](docs/workflows.md), [docs/install.md](docs/install.md), [docs/api.md](docs/api.md).

## API

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8788/v1", api_key="sk-...", timeout=200)
client.images.generate(model="toy-image", prompt="неоновый город")
job = client.videos.create(model="minimax-hailuo-02", prompt="волны на закате", seconds="6")
```

| Метод | Путь |
|--------|------|
| GET | `/health`, `/ready` |
| GET | `/docs` Swagger UI |
| GET | `/v1/models` |
| POST | `/v1/images/generations` |
| POST | `/v1/videos` → `{status: queued}` |
| GET | `/v1/videos/{id}` — у `completed` есть `url` |
| GET | `/v1/videos/{id}/content` |
| POST | `/v1/chat/completions` (только image-shim) |

Авторизация: `Authorization: Bearer sk-...` или `X-Api-Key`. В Swagger — **Authorize**.

HMAC-URL файлов (`GET /v1/files/{id}?exp=&sig=`) работают **без** Bearer (так качает holix-media).

Drop-in holix-media для видео: T2V-модель, джоб укладывается в poll клиента (180 с, если клиент не патчить), в completed JSON есть `url`.

## MikroLLM

В MikroLLM (текущий main) есть тип бэкенда **OpenComfy**. Добавьте сервер:

- kind: `opencomfy`
- URL: `http://<gpu>:8788` (без `/v1`)
- token: ключ OpenComfy `sk-…`

Обновите каталоги, подключите alias на вкладке **Модели**, дальше вызывайте MikroLLM `/v1/images/generations` и `/v1/videos` как обычно.

## Импорт workflow из ComfyUI

Графы из UI ComfyUI (`user/default/workflows`) — **UI-формат**. OpenComfy нужен **API-format**. Импортёр читает `/userdata?dir=workflows`, конвертирует через `/object_info`, угадывает `prompt` / `seed` / `width` / `height` / `seconds` / `input_image` и дописывает модели в `models.yaml`.

Веб: [http://127.0.0.1:8788/import](http://127.0.0.1:8788/import) — ключ, галочки, загрузка нескольких. Без **prompt** или без **reference** (если в графе LoadImage/LoadVideo) строка заблокирована.

```bash
opencomfy -config ~/.config/opencomfy/config.yaml -list-comfy
opencomfy -config ~/.config/opencomfy/config.yaml -import-comfy "MiniMax H3 Talking Avatar.json" "ND_Flux2_Klein_AIO_v1.json"
```

Существующие id пропускаются. При записи создаётся `models.yaml.bak`.

## Разработка

```bash
go test ./...
make build
```

Лицензия: [MIT](LICENSE).
