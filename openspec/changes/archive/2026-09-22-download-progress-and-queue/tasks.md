# Tasks: download-progress-and-queue

## 0. Discovery / coordination (main)

- [ ] 0.1 Реальный код + mapping — прочитать importwf/download.go, provision.go, local.go, hf.go, comfy/client.go, httpapi/import.go, httpapi/server.go. Установить точки chunk-прогресса, раскладки весов, очереди ComfyUI.
  - **assignee:** `main`
  - **size:** `s`
  - **reason:** подтвердить реальную структуру кода

## 1. Chunk-прогресс + персистентный store

- [ ] 1.1 Chunk-прогресс в downloadOne — io.Reader-обёртка, считающая прочитанные байты и обновляющая it.Bytes на chunk; Total из Content-Length.
  - **assignee:** `main`
  - **size:** `s`
  - **reason:** ядро прогресса
  - **depends_on:** `0.1`

- [ ] 1.2 Персистентный store snapshot-ов — JSON-файл под job-id, atomic write (write .tmp + rename), TTL, mutex per id; Snapshot читает из store.
  - **assignee:** `main`
  - **size:** `s`
  - **reason:** прогресс переживает перезагрузку
  - **depends_on:** `1.1`

- [ ] 1.3 Rate-limited воркер — воркер-лимитер (buffered channel-семафор, max_concurrent из конфига) вместо go ProvisionForModel / go d.run.
  - **assignee:** `main`
  - **size:** `m`
  - **reason:** ограничивает fan-out скачиваний
  - **depends_on:** `1.2`

## 2. Раскладка весов по layout

- [ ] 2.1 Раскладка по layout-подкаталогам — определить modelType для DLItem и писать в layout-подкаталог (model/adapter/VAE/T2V/V2V/I2V) через Layout/LayoutDir, а не единый download/.
  - **assignee:** `main`
  - **size:** `m`
  - **reason:** исправляет раскладку весов
  - **depends_on:** `1.3`

- [ ] 2.2 Проверка local.go/analyze.go — убедиться, что изменения download.go не ломают локальный search / layout.
  - **assignee:** `main`
  - **size:** `s`
  - **reason:** защита обратной совместимости
  - **depends_on:** `2.1`

## 3. Визуальная очередь ComfyUI

- [ ] 3.1 comfy Status() — добавить Status()/Snapshot() очереди ComfyUI: running count, pending count, prompt_ids.
  - **assignee:** `main`
  - **size:** `s`
  - **reason:** источник данных для queue view
  - **depends_on:** `0.1`

- [ ] 3.2 httpapi: GET /comfy/queue + chunk-прогресс — новый protected GET /comfy/queue, прогресс в GET /comfy/downloads/{id}.
  - **assignee:** `main`
  - **size:** `m`
  - **reason:** API-слой queue view
  - **depends_on:** `1.3, 3.1`

## 4. Frontend

- [ ] 4.1 import.go — прогресс-бар для job, визуальная очередь ComfyUI, сохранение состояния после перезагрузки.
  - **assignee:** `main`
  - **size:** `m`
  - **reason:** UI-визуализация
  - **depends_on:** `3.2`

## 5. Тесты + smoke

- [ ] 5.1 go test ./... — store, dl chunk, provision layout, comfy queue.
  - **assignee:** `main`
  - **size:** `m`
  - **reason:** регрессия
  - **depends_on:** `2.2`

- [ ] 5.2 build + vet + smoke — make build, go vet ./..., прогнать POST /comfy/provision (прогресс 0%→100%) и GET /comfy/queue.
  - **assignee:** `main`
  - **size:** `s`
  - **reason:** end-to-end проверка
  - **depends_on:** `5.1`
