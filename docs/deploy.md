# Deployment (deploy-to-server.sh)

Скрипт `deploy/deploy-to-server.sh` собирает **static linux/amd64** бинарник,
заливает его на удалённый сервер по scp, останавливает юнит, заменяет файл
(без `Text file busy`) и запускает юнит заново.

> Бинарник — единственный способ работы. `CGO_ENABLED=0`, static, без зависимостей.
> `deploy/install.sh` — устаревший, не используется.

---

## Быстрый старт

```bash
# Локально: собрать, залить, перезапустить юнит
deploy/deploy-to-server.sh
```

Скрипт сам:

1. `go build` → static `opencomfy-linux-amd64` (в `.deploy/`)
2. scp → `~/.local/bin/opencomfy.new` на сервере
3. `systemctl --user stop opencomfy`
4. `mv opencomfy.new ~/.local/bin/opencomfy`
5. `systemctl --user start opencomfy`
6. покажет статус, версию и healthcheck

---

## Переменные окружения

| Переменная       | По умолчанию | Назначение |
|------------------|----------------|------------|
| `SERVER_USER`    | `javded`       | SSH-юзер на сервере |
| `SERVER_HOST`    | `192.168.88.252` | IP/FQDN сервера |
| `PORT`           | `8788`         | Порт, который слушает бинарник (флаг `-port`) |
| `VERSION`        | `0.2.0`        | Тег версии для scp-файла (для экспериментов) |

Пример — перезапуск с другим юзером и портом:

```bash
SERVER_USER=javded SERVER_HOST=192.168.88.252 PORT=8080 \
  deploy/deploy-to-server.sh
```

---

## Что проверяется после деплоя

- **юнит**: `systemctl --user status opencomfy` → `active (running)`
- **port**: `listening on [::]:8788`
- **health**: `curl -s http://localhost:8788/health` → `{"status":"ok","version":"0.2.0"}`

> Строка `missing config` при вызове `version` — это **норма**.
> Бинарник стартует только через юнит с флагом `-config`.
> Сам по себе, без конфига, он не запускается.

---

## Как работает скрипт (по шагам)

1. **Проверка env** — `SERVER_HOST`, `SERVER_USER`, `PORT`, `VERSION`.
2. **Локальная сборка** — `mkdir .deploy`, `go build -o .deploy/opencomfy-linux-amd64`.
3. **scp** — `~/.local/bin/opencomfy.new` на сервере.
4. **SSH-сессия**:
   - `systemctl --user stop opencomfy`
   - `mv ~/.local/bin/opencomfy.new ~/.local/bin/opencomfy`
   - `systemctl --user start opencomfy`
5. **Диагностика**:
   - `systemctl --user status opencomfy`
   - `version` по `-config` (если есть)
   - healthcheck по `curl`

---

## Частые проблемы

| Ошибка | Решение |
|--------|---------|
| `scp: ...: No such file or directory` | Проверь, что юнит не в `Loaded: not-found`. `systemctl --user stop` вернёт «No such file» — это ок. |
| `Text file busy` | Скрипт уже останавливает юнит перед `mv`. Если всё же — повтори запуск. |
| `unknown service opencomfy.service` | Юнит не установлен. Установи `deploy/install.sh` или `deploy/opencomfy.service`/`opencomfy.user.service`. |
| `missing config` в `version` | Норма — юнит запускается с `-config`, сам бинарник без него не стартует. |
| `connection refused` на healthcheck | Жди 2–5 сек после старта юнита, пока ComfyUI поднимется. |

---

## Ручной деплой (если скрипт не подошёл)

```bash
# 1. Собрать
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o .deploy/opencomfy ./cmd/opencomfy

# 2. Залить
scp .deploy/opencomfy $SERVER_USER@$SERVER_HOST:~/.local/bin/opencomfy.new

# 3. На сервере
systemctl --user stop opencomfy
mv ~/.local/bin/opencomfy.new ~/.local/bin/opencomfy
systemctl --user start opencomfy
systemctl --user status opencomfy
```
