# warm-remote (backlog)

Java helper to attach Selenide to an existing ChromeDriver session. **Not hub-attach.** Not in `stacks/java-spring/tests`.

Do not copy this into the generator template or the live Jenkins hub-attach job.

| File | Role |
|------|------|
| `WarmRemoteWebDriver.java` | `attach(remoteUrl, sessionId)` — no New Session |
| `forward-warm-props.gradle` | Forward `-Dwarm.sessionId` / `-DskipOpen` into the test JVM |

Preopen scripts live in [`../scripts/`](../scripts/) (`jenkins-preopen.example.sh`, `preopen-login.sh`). They do not compose yet — see backlog in the root README.

## Wire-in (when reuse-session is go)

1. Copy `WarmRemoteWebDriver.java` into the consuming tests `helpers/`.
2. If `-Dwarm.sessionId` is set: `Configuration.remote = null` and `WebDriverRunner.setWebDriver(WarmRemoteWebDriver.attach(remoteUrl, sessionId))`.
3. Skip `open("/login")` only when the page is already that URL (`-DskipOpen=true`).
4. After the test: `DELETE` the WD session, then `POST /pool/release`. Idle must not keep `/login`.

`sessionId` is the ChromeDriver UUID, not orchestrator `slot.sessionId` (`pool-chrome-1`).
