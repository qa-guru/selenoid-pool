# Release v1.3.0 — qa-guru/selenoid-pool

**Дата:** 17 августа 2026  
**Предыдущий:** [v1.2.0](RELEASE_v1.2.0.md)

Rename: `qa-guru/selenoid-warm-pool` → `qa-guru/selenoid-pool`. Hub binary is unchanged (`-warm-pool-url` stays; alias `-pool-url`).

| Изменение | Описание |
|-----------|----------|
| **GitHub** | [qa-guru/selenoid-pool](https://github.com/qa-guru/selenoid-pool). Old URL redirects. |
| **Go module** | `github.com/qa-guru/selenoid-pool` |
| **Image** | `qaguru/selenoid-pool:min` (compose `container_name: selenoid-pool`; docker-DNS alias `selenoid-warm-pool`) |
| **Compose** | Project `selenoid-pool` (`docker-compose.hub.yml`). Network stays `selenoid-warm`. Hot overlay stays project `selenoid-hot`. |
| **cm** | `--pool` alias for `--warm-pool` |

Not separate services: no `selenoid-cold-pool` / `selenoid-hot-pool` binaries.
