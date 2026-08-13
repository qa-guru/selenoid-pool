# Release v1.1.1 — qa-guru/selenoid-warm-pool

**Дата:** 13 августа 2026  
**Предыдущий:** [v1.1.0](https://github.com/qa-guru/selenoid-warm-pool/releases/tag/v1.1.0)

v1.1.0 published loopback ports on `149-min` with 256m shm — ChromeDriver answered, Chrome exited on New Session.

| Изменение | Описание |
|-----------|----------|
| **Image** | `qaguru/webdriver-chrome:149` (warm entrypoint + Xvfb via `ENABLE_VNC=true`; 5900 not published) |
| **shm** | `2gb` — same as prod `browsers-production.json` chrome 149.0 |
| **tmpfs** | `/tmp` 512m |

Loopback bind `127.0.0.1:14441/14442` unchanged.
