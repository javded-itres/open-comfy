# Install

## systemd

1. `make build`
2. `sudo ./opencomfy -init -config /etc/opencomfy/config.yaml`
3. Save the printed `sk-` key
4. Bind ComfyUI to `127.0.0.1:8188`. Do not run image_bot against the same instance.
5. Install `deploy/opencomfy.service`
6. Missing config exits **2**; the unit has `RestartPreventExitStatus=2`
7. Hugging Face downloads: put `HF_TOKEN=hf_…` in `~/.config/opencomfy/hf.env` (user unit) or `/etc/opencomfy/env` (system unit). `chmod 600`. Config key `comfyui.hf_token_env` defaults to `HF_TOKEN`. Do not commit the token.

## Docker

`network_mode: host` only for the documented path. Config and workflows live on the host under `/etc/opencomfy`. Data: `/var/lib/opencomfy`.

Scratch image: no curl. Healthcheck binary flag: `-healthcheck`.
