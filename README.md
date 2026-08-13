# selenoid-warm-pool

Protocol-agnostic warm browser pool for fast CI UI tests.

Slots expose the same HTTP warm API whether the browser is **WebDriver** ([`browser-image/webdriver`](../browser-image/webdriver/README.md)) or **Playwright** ([`browser-image/playwright`](../browser-image/playwright/README.md)). The orchestrator only knows `warm_url` + curl.

## Architecture

```
Jenkins job start
    │
    ├─ POST /pool/reserve          → slotId, webdriverUrl / playwrightWsUrl
    ├─ POST /pool/preopen {url}    → curl slot /warm/goto  (parallel with Gradle)
    ├─ POST /pool/video/start      → optional, on demand
    │
    ├─ ./gradlew test -Dwarm_driver=true ...
    │
    └─ POST /pool/video/stop
       POST /pool/release
```

## Warm API contract (on each slot, port `8080`)

Canonical implementation: [`warm-api/`](warm-api/).

| Method | Path | Body | Description |
|--------|------|------|-------------|
| GET | `/warm/status` | — | Slot readiness, protocol, sessionId, video state |
| POST | `/warm/goto` | `{"url":"..."}` | Open URL on hot browser |
| POST | `/warm/reset` | — | Cookies cleared, `about:blank` |
| POST | `/warm/video/start` | `{"sessionId":"pool-chrome-1"}` optional | Start ffmpeg x11grab |
| POST | `/warm/video/stop` | — | Stop recording, finalize mp4 |

### Video naming

`sessionId` is **stable per slot** (e.g. `pool-chrome-1`). Each recording gets a unique file:

```
{sessionId}-{timestamp}.mp4
```

Example: `pool-chrome-1-1782638876524.mp4`

Same logical session, many runs — no overwrite. Toggle recording per run via `/warm/video/start` and `/warm/video/stop` without restarting the browser.

## Orchestrator API (port `9090`)

| Method | Path | Body |
|--------|------|------|
| GET | `/` | — (same as `/health`; stand URL gate) |
| GET | `/health` | — |
| GET | `/pool/slots` | — |
| POST | `/pool/reserve` | `{"protocol":"webdriver","browser":"chrome","owner":"jenkins-42"}` · hub adds `"loopback":true` |
| POST | `/pool/release` | `{"slotId":"pool-chrome-1"}` |
| POST | `/pool/preopen` | `{"slotId":"...","url":"https://..."}` |
| POST | `/pool/video/start` | `{"slotId":"...","sessionId":"..."}` |
| POST | `/pool/video/stop` | `{"slotId":"..."}` |

`loopback: true` (hub-on-host): only slots with a loopback WebDriver URL are reserved (`webdriver_url` already `127.0.0.1`/`localhost`/`::1`, or `webdriver_url_loopback`). Response `webdriverUrl` is that loopback address. No matching slot → **409**. Jenkins/box2 omit `loopback` and keep docker-DNS URLs.

Hub-attach operator guide (Chrome WD, loopback reserve, cold fallback): [HUB-ATTACH.md on qa-guru/selenoid](https://github.com/qa-guru/selenoid/blob/main/docs/HUB-ATTACH.md).

## Quick start

Go 1.26+ (matches `selenoid` / `cm`). No Python, no pip.

```bash
cd selenoid-warm-pool
go build -o selenoid-warm-pool .

# terminal 1 — build/start slot containers first (see browser-image/README.md)
# terminal 2
./selenoid-warm-pool --config config.example.yaml
# then: curl -s http://127.0.0.1:9090/health
```

Config path, host and port come from flags (`--config --host --port`) or env
(`WARM_POOL_CONFIG`, `WARM_POOL_HOST`, `WARM_POOL_PORT`). Defaults:
`config.example.yaml`, `0.0.0.0`, `9090`.

Docker Compose (example):

```bash
docker compose -f docker-compose.example.yml up --build
```

The orchestrator image is a multi-stage static Go binary on `scratch`.

## Jenkins: preopen before Gradle

See [`scripts/jenkins-preopen.example.sh`](scripts/jenkins-preopen.example.sh).

```bash
export WARM_POOL_URL=http://127.0.0.1:9090
export PREOPEN_URL=https://your-app/login.html?ru
./scripts/jenkins-preopen.example.sh &
./gradlew test --tests 'tests.LoginTests.successfulAuthorizationTest' -Dwarm_driver=true
```

Gradle flags (planned in tests-java):

| Flag | Meaning |
|------|---------|
| `-Dwarm_driver=true` | Use reserved slot instead of cold Selenoid |
| `-Dpreopen_url=` | Skip `open()` if page already loaded (empty = disabled) |
| `-Dwarm_slot_id=` | Slot from orchestrator reserve |

## Slot environment

| Env | Default | Description |
|-----|---------|-------------|
| `WARM_SLOT_ID` | hostname | Pool slot name |
| `WARM_SESSION_ID` | `WARM_SLOT_ID` | Stable id for video file prefix |
| `WARM_PORT` | `8080` | Warm API port |
| `WARM_VIDEO_DIR` | `/data/video` | mp4 output |
| `WARM_VIDEO` | `true` | Keep Xvfb for ffmpeg |

Playwright slots additionally: `WARM_ENABLED=true`, `PW_WS_ENDPOINT` (set by entrypoint).

## Hub UI metrics (box1)

GitHub: https://github.com/qa-guru/selenoid-warm-pool

Deploy next to the Selenoid hub so `/status` exposes `warmReady` / `warmTotal` for selenoid-ui WARM:

```bash
docker compose -f docker-compose.hub.yml up -d --build
# hub flag: -warm-pool-url http://127.0.0.1:9090
```

Configs: `docker-compose.hub.yml` + `config.hub.yaml`. Jenkins path stays on box2 (`docker-compose.min.yml`) and does **not** send `loopback`.

## Hub-attach (Chrome WD)

Hub binary on the host: `-warm-pool-url http://127.0.0.1:9090`. It sends `POST /pool/reserve` with `"loopback":true`. Local compose (`config.local.yaml` + published `14441/14442`) is attach-ready. Box1 `config.hub.yaml` has docker-DNS only → 409 → cold until loopback URLs **and** published WD ports exist. Do not treat this repo change as enabling attach on box1: `config.hub.yaml` stays docker-DNS only. Rebuild the orchestrator so `loopback:true` returns 409 instead of a docker-DNS URL.

## Status

Go orchestrator. Hub polls `/pool/slots` for UI WARM. Chrome WD hub-attach is implemented (loopback slots + cold fallback). Playwright slots, nginx `/pool/*`, Box2 Jenkins jobs — unchanged.
