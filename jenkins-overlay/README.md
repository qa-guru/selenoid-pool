# warm-remote / hot overlay

Java helper for **session-reuse**: Selenide joins an existing ChromeDriver session. **Not container-reuse** (warm). Not in hub ethalon tests. Not in Jenkins job [#14](https://jenkins.qa.guru/job/autotests-ai-multistack-tests-pipeline-java-warm-pool/14/).

`WarmRemoteWebDriver.attach` is the Java API name (Selenium/Appium sense). The pool term is **session-reuse**. Not an Allure attachment.

| | Warm (**container-reuse**) | Hot (**session-reuse**, this overlay) |
|--|-------------------|--------------------|
| Client | Hub `POST /session` → New Session on a warm container | Bypass hub: `WarmRemoteWebDriver.attach` / PW `connect` |
| Session | New, `about:blank` | Same UUID / WS; page already on `PREOPEN_URL` |
| Slots | 4/4 including headed `:149` on `14441/14442` | **2/2 `-min` only**, ports `16440` (WD) / `16441` (PW) |
| `-Dwarm.sessionId` | unused | ChromeDriver **UUID**, not `pool-hot-chrome-min-1` |

Do not copy this into the generator template or the live warm (container-reuse) job.

| File | Role |
|------|------|
| `WarmRemoteWebDriver.java` | `attach(remoteUrl, sessionId)` — no New Session |
| `forward-warm-props.gradle` | Forward `-Dwarm.sessionId` / `-DskipOpen` / `-DcloseBrowserAfterAll` into the test JVM |

`PREOPEN_URL` is an optional `url` field on `POST /pool/lease`. GitHub frontend deploy → lease again with `url` set. `POST /pool/release` keeps the UUID unless `"killSession":true`.

## JVM (hot)

Keep the **Gradle daemon + configuration-cache** already on the agent. Do not introduce JDWP. `closeBrowserAfterAll=false` so the test does not `quit()` the leased session.

Jenkins stubs (disabled): `autotests-ai-multistack-tests-pipeline-{java,python,js}-hot-pool` (+ `-full-attachments`). Java runs on the box1 warm agent (`:16440`). Python/JS stay on box2 until a box1 node exists.

## Wire-in (hot job only — never #14)

1. Copy `WarmRemoteWebDriver.java` into the consuming tests `helpers/`.
2. Lease: `curl POST /pool/lease` → `webdriverUrl` + `sessionId`.
3. `Configuration.remote = null` and `WebDriverRunner.setWebDriver(WarmRemoteWebDriver.attach(webdriverUrl, sessionId))`.
4. Skip `open("/login")` when `-DskipOpen=true` if lease already passed `url`.
5. After the test: `POST /pool/release` with the slot id. Do not DELETE the UUID unless tearing the slot down (`killSession: true`).

```bash
curl -sS -X POST "${WARM_POOL_URL}/pool/lease" \
  -H 'Content-Type: application/json' \
  -d '{"protocol":"webdriver","browser":"chrome","owner":"ci-1","loopback":true,"url":"https://autotests.ai/stack/backend-java-spring/frontend-typescript-react/login"}'
# then Gradle: -DremoteUrl=<webdriverUrl> -Dwarm.sessionId=<sessionId> -DskipOpen=true -DcloseBrowserAfterAll=false
```
