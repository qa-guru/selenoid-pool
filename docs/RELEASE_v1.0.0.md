# Release v1.0.0 — qa-guru/selenoid-warm-pool

**Дата:** 13 августа 2026  
**GitHub:** https://github.com/qa-guru/selenoid-warm-pool/releases/tag/v1.0.0

First git tag. Orchestrator already served box1 WARM metrics; this cut adds the container-reuse reserve filter.

## Что нового

| Изменение | Описание |
|-----------|----------|
| **`loopback` on POST /pool/reserve** | When `loopback: true`, only slots with a host-reachable WebDriver URL (`127.0.0.1` / `localhost` / `::1`, or `webdriver_url_loopback`) are reserved. Otherwise **409**. |
| **Jenkins / box2** | Omit `loopback` — docker-DNS `webdriver_url` unchanged. |

Container-reuse: [CONTAINER-REUSE.md](https://github.com/qa-guru/selenoid/blob/main/docs/CONTAINER-REUSE.md)

Box1 `config.hub.yaml` stays docker-DNS only (metrics, no attach). Rebuild orchestrator from this tag; do not publish WD ports in this cut.
