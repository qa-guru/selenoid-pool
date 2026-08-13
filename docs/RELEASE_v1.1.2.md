# Release v1.1.2 — qa-guru/selenoid-warm-pool

**Дата:** 13 августа 2026  
**Предыдущий:** [v1.1.1](https://github.com/qa-guru/selenoid-warm-pool/releases/tag/v1.1.1)

Hub-attach **без изменений** относительно v1.1.1 (`qaguru/webdriver-chrome:149`, shm 2g, loopback `127.0.0.1:14441/14442`). Compose/orchestrator binary не трогали.

| Изменение | Описание |
|-----------|----------|
| **Preopen** | Jenkins preopen / reuse-session **parked**. Не вшивать в hub-attach job: хаб на `POST /session` поднимает новую WD-сессию → `about:blank`. |
| **PREOPEN_URL** | Дефолт в `scripts/preopen-login.sh` / overlay — teaching `/login` на `autotests.ai/stack/...` (старый `reference-app.autotests.ai` снят). |

Скрипты `scripts/jenkins-preopen.example.sh` и `scripts/preopen-login.sh` остаются заделом на отдельный режим, не канон.
