# selenoid-warm-pool

Protocol-agnostic warm browser pool for fast CI UI tests.

Slots expose the same HTTP warm API whether the browser is **WebDriver** ([`browser-image/webdriver`](../browser-image/webdriver/README.md)) or **Playwright** ([`browser-image/playwright`](../browser-image/playwright/README.md)). The orchestrator only knows `warm_url` + curl.

## Pools

| Pool | Slots | Client | Browser |
|------|-------|--------|---------|
| **Cold** (default, live) | 0 warm — hub `docker run` from `browsers.json` | Hub `POST /session` | Catalog |
| **Warm** (this repo, hub-attach) | **4/4** containers up; **New Session** on that node | Hub → `POST /pool/reserve` (Chrome WD). PW slots up, hub PW still cold | WD `:149` + `:149-min` + PW `1.61.1` + `1.61.1-min` |
| **Hot** | Session + page already live | Bypass hub (UUID / WS) | Only `-min` — window 03 |

Cold is the existing Selenoid path and the hub-attach fallback (409 / not Chrome WD / video/VNC/HAR). Warm is hub-attach New Session. Hot (reuse-session, `PREOPEN_URL`, `WarmRemote`) is window 03 — not this README’s live contract.

## Architecture (live: hub-attach)

```
Selenide / Jenkins  →  hub POST /wd/hub/session  (Chrome WD, no video/VNC/HAR)
                         │
                         ├─ POST /pool/reserve {loopback:true}
                         │    200 + 127.0.0.1:14441 (headed :149, first)
                         │        or 127.0.0.1:14442 ( :149-min, shm 2g)
                         │    session end → POST /pool/release (slot stays up)
                         │
                         └─ 409 / down → cold Docker
```

Warm 4/4 containers: `qaguru/webdriver-chrome:149` · `:149-min` · `qaguru/playwright-chromium:1.61.1` · `:1.61.1-min`. Hub-attach **client** is Chrome WD only (ADR). PW slots are up for metrics / window 03; hub Playwright stays cold. Do not idle `/login`.

Jenkins job: [autotests-ai-multistack-tests-pipeline-java-warm-pool](https://jenkins.qa.guru/job/autotests-ai-multistack-tests-pipeline-java-warm-pool/). Operator guide: [HUB-ATTACH.md](https://github.com/qa-guru/selenoid/blob/main/docs/HUB-ATTACH.md).

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

## Backlog: hot pool (window 03)

**Not hub-attach.** Hub `POST /session` creates a **new** ChromeDriver session (`about:blank`); a preopened page does not survive.

Hot (Jenkins `reserve` + WD preopen → test reuses that WD session, bypassing the hub) is window 03. **Not** in `stacks/java-spring/tests`. Java helper SSOT: [`jenkins-overlay/`](jenkins-overlay/) (`WarmRemoteWebDriver` only — no TestBase/LoginPage copies). Scripts stay as a starting point — do not wire them into the hub-attach job:

- [`scripts/jenkins-preopen.example.sh`](scripts/jenkins-preopen.example.sh)
- [`scripts/preopen-login.sh`](scripts/preopen-login.sh) (default `PREOPEN_URL` = teaching `/login` on autotests.ai/stack)

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

Configs: `docker-compose.hub.yml` + `config.hub.yaml` (warm 4/4). Jenkins path stays on box2 (`docker-compose.min.yml`) and does **not** send `loopback`.

## Hub-attach (Chrome WD)

Hub binary on the host: `-warm-pool-url http://127.0.0.1:9090`. It sends `POST /pool/reserve` with `"loopback":true`. Box1 `docker-compose.hub.yml` publishes `127.0.0.1:14441` (headed `:149`) and `14442` (`:149-min`, shm 2g). Headed is first in `config.hub.yaml` so [job #14](https://jenkins.qa.guru/job/autotests-ai-multistack-tests-pipeline-java-warm-pool/14/) prefers it. Playwright WS loopback is `14501/14502` — hub does not attach PW. Jenkins/box2 omit `loopback` (`docker-compose.min.yml`).

## Status

Go orchestrator. Hub polls `/pool/slots` for UI WARM. Chrome WD hub-attach is live (loopback WD slots + **cold** fallback). Warm compose is **4/4**; hub Playwright remains cold. nginx `/pool/*`, Box2 Jenkins jobs — unchanged. **Hot** pool (reuse-session / preopen) is window 03. Do not squeeze #14 wall (Gradle ~3s / 4216 ms).
