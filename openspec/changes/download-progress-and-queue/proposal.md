# Proposal: download-progress-and-queue

## Why

Текущая `internal/download` — это **one-shot `httpapi.Download`**: воркер делает `go.mod`-сборку через `go build`, `go install`, а весы качаются в `io.Copy`-цикле прямо в `s.go`. У этого два пробела:

1. **Нет персистентного прогресса.** `go build`/`go install` не дают chunk-байт: `io.Copy` знает `TotalBytes` только когда `resp.Header["Content-Length"]` есть, иначе `Total=0`. Пока вес качается, UI видит 0%. По завершени` it.Bytes`=it.Total — прогресс «вспрыскивает» до 100%. При перезагрузке страницы ничего не сохраняется — непонятно, где стояла загрузка.
2. **Нет очереди/воркеров.** `s.go` ставит `go func(){...}()` на каждый `POST /download/{id}`; воркер не ограничен, `Snapshot`/`Status` читаются без синхронизации (data race на `map` и `int64`), `Start`/`Stop`/`Status` не atomic.
3. **Нет раскладки весов по типам.** `local.go`/`provision.go`/`local.go` уже умеют `Layout`/`LayoutDir`/`LayoutBaseDir` (model/adapter/VAE/T2V/V2V/I2V), но `download.go` **раскладывает всё в `LayoutDir`** (единый `download/`), а не по подкаталогам layout-а. Тип модели в `download.go` берётся из `download.Type` — отдельного поля нет (см. `LayoutDir`/`LayoutBaseDir` — там есть `modelType`, но `go build`/`go install` его не передают).
4. **Нет «визуальной очереди» ComfyUI.** `internal/httpapi` + `internal/comfy` уже имеют очередь ComfyUI (`/queue`, `QueuePrompt`), но в UI/API нет отдельного `view`-представления очереди (сколько jobs стоит в ожидании admission `max_in_flight`/`max_waiting`, какие сейчас `in_flight`).

Собрать SDD-задачу на субагентах, показать задачи пользователю, и только после его одобрения запускать выполнение.

## What Changes

- **`internal/download`**: персистентный store snapshot-ов (`snapshot.go`) + chunk-прогресс (`dl.go`/`s.go`), воркеры-лимитеры вместо `go func`.
- **`internal/comfy`**: `Snapshot()`/`Status()` очереди + `view`-эндпоинт в `httpapi`.
- **`internal/httpapi`**: новый `GET /comfy/queue` (UI) + chunk-прогресс в `GET /comfy/downloads/{id}`.
- **`internal/importwf`**: передача `modelType` в `go build`/`go install` → раскладка весов по `LayoutDir`-подкаталогам.
- Frontend `import.go`: прогресс-бар + визуальная очередь.

## Impact

- **Risks**: `httpapi.Download` меняет contract — `POST /comfy/downloads/{id}` и `GET /comfy/downloads/{id}` должны сохранить backward-compat (UI всё ещё ждёт `queued`/`url`). `internal/download` сейчас **не скомпилирован** (нет `package download` — это `s.go`/`dl.go`/`snapshot.go`/`local.go`/`store.go` без `package`-деклараций в одних файлах? — проверить). `go build`/`go install` в воркере — тяжёлые операции, нужны таймакеты и cleanup.
- **Migrations**: персистентный store — новый JSON-файл под snapshot-ы (TTL как у `jobs`).
- **Backward-compat**: старые эндпоинты `GET /comfy/downloads/{id}`/`POST /comfy/downloads/{id}` не ломать.
