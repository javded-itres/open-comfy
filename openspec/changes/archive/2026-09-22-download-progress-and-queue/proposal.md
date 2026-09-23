# Proposal: download-progress-and-queue

## Why

`internal/importwf/download.go` — функция `downloadOne` делает `io.Copy(f, resp.Body)` **без подсчёта байт во время передачи**: `it.Bytes`/`it.Total` устанавливаются только после завершения копирования. Пока вес качается, прогресс показывает 0%, а по завершении «вспрыскивает» до 100%. При обрыве соединения нельзя продолжить (`.part`-файл создаётся, но не докачивается).

`Downloads.Start` и `Provision` гонят `go d.run(...)` / `go ProvisionForModel(...)` **без лимита параллельных скачиваний** — неограниченное числоgoroutine, нет ватчдога.

`Snapshot()` читает только из памяти — при перезагрузке страницы прогресс и статус теряются.

`download.go` не раскладывает веса по layout-подкаталогам (`model`/`adapter`/`VAE`/`T2V`/`V2V`/`I2V`), хотя `local.go`/`provision.go` уже دارند `Layout`/`LayoutDir`/`modelType`.

В UI/API нет отдельного представления очереди ComfyUI (сколько jobs в ожидании admission, какие `in_flight`).

## What Changes

- **`internal/importwf/download.go`**: chunk-прогресс (io.Reader-обёртка, считающая прочитанные байты; `TotalBytes` из `Content-Length`), персистентный store snapshot-ов (atomic write + TTL + mutex per id), rate-limited воркер вместо `go func`.
- **`internal/importwf/provision.go`**: запуск скачиваний через воркер-лимитер, а не `go ProvisionForModel`.
- **`internal/importwf/download.go`**: раскладка весов по layout-подкаталогам через `modelType`.
- **`internal/comfy/client.go`**: `Status()`/`Snapshot()` очереди ComfyUI.
- **`internal/httpapi`**: новый `GET /comfy/queue` + chunk-прогресс в `GET /comfy/downloads/{id}`.
- **frontend `import.go`**: прогресс-бар + визуальная очередь ComfyUI.

## Impact

- Backward-compat: `GET /comfy/downloads/{id}` должен сохранить `items[].bytes` / `items[].total` / `status`.
- Новый JSON-store под snapshot-ы с TTL — аналогично `internal/jobs`.
- `internal/download` — это не отдельный пакет; изменения вносятся в `internal/importwf`.
