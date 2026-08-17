# Release v1.2.0 — qa-guru/selenoid-warm-pool

**Дата:** 17 августа 2026  
**Предыдущий:** [v1.1.2](RELEASE_v1.1.2.md)

Hot session-reuse is now a Go API on this sidecar. Repo name stays `selenoid-warm-pool` until a later rename to `selenoid-pool`. Hub is unchanged.

| Изменение | Описание |
|-----------|----------|
| **`POST /pool/lease`** | Hot slots only. Returns ChromeDriver **UUID** (`sessionId`) or Playwright WS. `created: true` only on New Session; otherwise attach to the live window. Optional `url` navigates. |
| **Release** | Default keeps the UUID (`/warm/reset` + unreserve). `"killSession":true` DELETEs the driver session. |
| **Scripts** | `scripts/lease.sh` is curl-only. `hot-reuse-session.sh` / `preopen-login.sh` wrap it. Python heredoc removed. |

```http
POST /pool/lease
{"protocol":"webdriver","browser":"chrome","owner":"ci-1","loopback":true}
```
