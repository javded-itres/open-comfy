# Design: download-progress-and-queue

## Context

OpenComfy — единственный HTTP-клиент одного ComfyUI. Провижн весов из Hugging Face делает `importwf.Provision` → `download.Download` → `downloadOne` (importwf/download.go).

Реальный код (importwf/download.go):
- `Downloads` — структура с `mu sync.Mutex` и `jobs map[string]*DLJob`. `Start(ctx, missing, opt)` создаёт `DLJob`, кладёт в map, гонит `go d.run(...)`.
- `DLJob` — `ID`, `Status DLStatus`, `Items []DLItem`, `Error`, `mu`. `Snapshot()` возвращает `DLJobView`.
- `DLItem` — уже имеет `Bytes int64` и `Total int64`. `downloadOne` устанавливает `it.Total = resp.ContentLength` и `it.Bytes = n` (после `io.Copy`) — **промежуточного прогресса нет**.
- `downloadOne` использует `dest + ".part"` — `.part`-файл уже есть, но докачки (resume) нет.
- `provision.go` — `Provision` гонит `go ProvisionForModel(model)` без лимита.
- `comfy/client.go` — уже есть `Queue(ctx) (Queue, error)`, `QueueResult`, `Status()`.
- `httpapi/import.go` — `comfyDownloadStatus` читает `s.DL.Get(id).Snapshot()`, `provisionComfyWorkflow` вызывает `Provision`.

## Approach

### 1. Chunk-прогресс (download.go, `downloadOne`)
Заменить `io.Copy(f, resp.Body)` на обёртку-счётчик:
```go
n, err := io.Copy(&countingWriter{w: f, onChunk: func(b int64){ j.mu.Lock(); j.Items[i].Bytes += b; j.mu.Unlock() }}, resp.Body)
```
Счётчик пишет в `.part` и вызывает callback на каждом chunk'е, обновляя `it.Bytes`. `Total` берётся из `Content-Length` (если 0 — прогресс в %, но unknown-total). Добавить частичную дозагрузку из `.part` если файл уже существует и `Content-Range` доступен — опционально, минимально: хотя бы обновлять Bytes.

### 2. Персистентный store snapshot-ов (download.go)
Сериализовать `DLJobView` в JSON-файл под каждым job-id (или один файл-индекс), atomic write (write `.tmp` + `os.Rename`), TTL-очистка старых записей, mutex per id. Периодическое обновление на каждом chunk'е (дебаунс, не на каждый байт). `Snapshot()` сначала читает из store, если запись просрочена — вернёт кэшированное.

### 3. Rate-limited воркер (provision.go + download.go)
Заменить `go ProvisionForModel` и `go d.run` на воркер-лимитер: буферизованный channel-семафор с настраиваемым уровнем параллельности из конфига (`provision.max_concurrent`, дефолт 2). Каждая итерация берёт задачу из channel, скачивает, отправляет результат. Ограничение спасает от неограниченного fan-out.

### 4. Раскладка весов по layout (download.go)
Определить `modelType` для `DLItem` (через `class_type`/`field` → model/adapter/VAE/T2V/V2V/I2V) и писать в layout-подкаталог через существующие `Layout`/`LayoutDir`/`modelType` из local.go, а не в единый `download/`.

### 5. ComfyUI queue view (comfy/client.go + httpapi)
`comfy.Client.Status(ctx)` возвращает сводку: `running` count, `pending` count, prompt_ids. Новый `GET /comfy/queue` в httpapi (protect), отдаёт сводку.

### 6. Прогресс в GET /comfy/downloads/{id}
Уже отдаёт `Snapshot()` — добавить вычисление `progress = Bytes/Total` в процентах в view, если `Total > 0`.

### Frontend (import.go)
Прогресс-бар для каждого job (полл GET /comfy/downloads/{id}), визуальная очередь ComfyUI (GET /comfy/queue), сохранение состояния после перезагрузки страницы.

## Constraints
- Только stdlib + yaml.v3. Rate limiter — buffered channel-семафор, не внешняя библиотека.
- constructor DI, no package-level mutable clients.
- `internal/download` — отдельного пакета нет; всё в `internal/importwf`.
- Backward-compat `GET /comfy/downloads/{id}`: не ломать `items[].bytes/total/status`.
