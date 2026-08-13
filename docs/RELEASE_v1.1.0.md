# Release v1.1.0 — qa-guru/selenoid-warm-pool

**Дата:** 13 августа 2026  
**Предыдущий:** [v1.0.0](https://github.com/qa-guru/selenoid-warm-pool/releases/tag/v1.0.0)

## Что нового

| Изменение | Описание |
|-----------|----------|
| **Box1 hub-attach** | `docker-compose.hub.yml` publishes `127.0.0.1:14441/14442→4444`; `config.hub.yaml` sets `webdriver_url_loopback`. Slots: `qaguru/webdriver-chrome:149` (Xvfb via `ENABLE_VNC`, shm 2g). |
| **Bind** | WD ports are loopback-only — not on the public NIC. |

Hub pin [v3.0.9](https://github.com/qa-guru/selenoid/releases/tag/v3.0.9) already sends `loopback:true`. After this compose, reserve returns 200 + loopback URL instead of 409.
