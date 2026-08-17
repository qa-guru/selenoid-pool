# Release v1.3.1 — qa-guru/selenoid-pool

**Дата:** 17 августа 2026  
**Предыдущий:** [v1.3.0](RELEASE_v1.3.0.md)

Drop docker-DNS alias `selenoid-warm-pool`. Orchestrator is reachable as `selenoid-pool` (`container_name`). Jenkins already uses `http://selenoid-pool:9090`. Network stays `selenoid-warm`. Hub flags unchanged (`-warm-pool-url` / `-pool-url`).

| Изменение | Описание |
|-----------|----------|
| **Compose** | No `aliases:` on the orchestrator. `docker-compose.hub.yml` / `.example.yml` / `.min.yml`. |
| **cm embed** | Same: project `selenoid-pool`, no legacy DNS alias. |

GitHub repo redirect `qa-guru/selenoid-warm-pool` → `qa-guru/selenoid-pool` is unchanged.
