# warm-remote / hot overlay

Java helper to attach Selenide to an existing ChromeDriver session. **Not hub-attach.** Not in `stacks/java-spring/tests`. Not in Jenkins job [#14](https://jenkins.qa.guru/job/autotests-ai-multistack-tests-pipeline-java-warm-pool/14/).

| | Warm (hub-attach) | Hot (this overlay) |
|--|-------------------|--------------------|
| Client | Hub `POST /session` → New Session on a warm container | Bypass hub: `WarmRemoteWebDriver.attach` / PW `connect` |
| Session | New, `about:blank` | Same UUID / WS; page already on `PREOPEN_URL` |
| Slots | 4/4 including headed `:149` on `14441/14442` | **2/2 `-min` only**, ports `16440` (WD) / `16441` (PW) |
| `-Dwarm.sessionId` | unused | ChromeDriver **UUID**, not `pool-hot-chrome-min-1` |

Do not copy this into the generator template or the live hub-attach job.

| File | Role |
|------|------|
| `WarmRemoteWebDriver.java` | `attach(remoteUrl, sessionId)` — no New Session |
| `forward-warm-props.gradle` | Forward `-Dwarm.sessionId` / `-DskipOpen` / `-DcloseBrowserAfterAll` into the test JVM |
| [`../scripts/hot-reuse-session.sh`](../scripts/hot-reuse-session.sh) | reserve hot slot → WD `POST /url` or PW WS → trap `DELETE`+`release` |

`PREOPEN_URL` default: `https://autotests.ai/stack/backend-java-spring/frontend-typescript-react/login`. GitHub frontend deploy → re-run the script with `--refresh` (POST `/url` again). Do not leave `/login` without the trap.

## JVM (hot)

Keep the **Gradle daemon + configuration-cache** already on the agent. Do not introduce JDWP. `closeBrowserAfterAll=false` so `quit()` runs in the trap after the test returns, not on wall.

## Wire-in (hot job only — never #14)

1. Copy `WarmRemoteWebDriver.java` into the consuming tests `helpers/`.
2. If `-Dwarm.sessionId` is set: `Configuration.remote = null` and `WebDriverRunner.setWebDriver(WarmRemoteWebDriver.attach(remoteUrl, sessionId))`.
3. Skip `open("/login")` when `-DskipOpen=true` (page already `PREOPEN_URL`).
4. After the test / always: `DELETE` the WD session, then `POST /pool/release`. Idle must not keep `/login`.

```bash
HOT_KEEP=1 ./scripts/hot-reuse-session.sh webdriver
# then Gradle: -DremoteUrl=$WARM_WD_URL -Dwarm.sessionId=$WARM_SESSION_ID -DskipOpen=true -DcloseBrowserAfterAll=false
```
