#!/usr/bin/env bash
# Hot 2/2 reuse-session (window 03). Bypasses the hub.
# reserve (hot slot ids only) → WD New Session + POST /url  or  PW WS
# → print UUID / WS → trap DELETE+release (idle ≠ /login).
#
# Does not touch warm 14441/14442 or job #14. Stand :9090 is reused, never --stop.
set -euo pipefail

ORCHESTRATOR_URL="${WARM_POOL_URL:-http://127.0.0.1:9090}"
PREOPEN_URL="${PREOPEN_URL:-https://autotests.ai/stack/backend-java-spring/frontend-typescript-react/login}"
OWNER="${BUILD_TAG:-hot-local}"
HOT_WD_SLOT="${HOT_WD_SLOT:-pool-hot-chrome-min-1}"
HOT_PW_SLOT="${HOT_PW_SLOT:-pool-hot-pw-min-1}"
HOT_WD_URL="${HOT_WD_URL:-http://127.0.0.1:14641}"
HOT_PW_WS="${HOT_PW_WS:-ws://127.0.0.1:14651/}"
PROTOCOL="${1:-both}"
REFRESH=0
if [[ "${1:-}" == "--refresh" ]]; then
  REFRESH=1
  PROTOCOL="${2:-both}"
fi

WD_SID=""
WD_RESERVED=""
PW_RESERVED=""
TIMING_FILE="${HOT_TIMING_FILE:-/tmp/hot-reuse-timing.json}"

python - "$ORCHESTRATOR_URL" "$PREOPEN_URL" "$OWNER" "$HOT_WD_SLOT" "$HOT_PW_SLOT" \
  "$HOT_WD_URL" "$HOT_PW_WS" "$PROTOCOL" "$REFRESH" "$TIMING_FILE" <<'PY'
import json, os, sys, time, urllib.error, urllib.request

orch, preopen, owner, wd_slot, pw_slot, wd_url, pw_ws, protocol, refresh_s, timing_path = sys.argv[1:]
refresh = refresh_s == "1"
protocol = protocol.lower()

def now():
    return time.perf_counter()

def http(method, url, body=None, timeout=30):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw = r.read().decode() or "{}"
            try:
                return r.status, json.loads(raw)
            except json.JSONDecodeError:
                return r.status, raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode() if e.fp else ""
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = raw
        return e.code, parsed

def slot_by_id(slots, sid):
    for s in slots:
        if s.get("id") == sid:
            return s
    return None

t0 = now()
marks = {}
state = {
    "wd_sid": None,
    "wd_reserved": None,
    "pw_reserved": None,
    "wd_url": wd_url.rstrip("/"),
    "pw_ws": pw_ws,
}

_, slots_body = http("GET", orch.rstrip("/") + "/pool/slots")
slots = slots_body if isinstance(slots_body, list) else []
marks["slots_ms"] = round((now() - t0) * 1000)

def reserve(slot_id, proto, browser):
    found = slot_by_id(slots, slot_id)
    if found is None:
        print(f"hot: orchestrator has no {slot_id} (stand still on old config) — direct ports, no reserve", file=sys.stderr)
        return None, found
    st, body = http("POST", orch.rstrip("/") + "/pool/reserve", {
        "protocol": proto,
        "browser": browser,
        "owner": owner,
        "loopback": True,
        "slotId": slot_id,
    })
    # orchestrator ignores slotId today — filter after
    slot = (body or {}).get("slot") if isinstance(body, dict) else None
    if st != 200 or not slot:
        print(f"hot: reserve {slot_id} -> {st} {body}", file=sys.stderr)
        return None, found
    if slot.get("id") != slot_id:
        # got a warm slot — release immediately, never steal 14441/14442
        http("POST", orch.rstrip("/") + "/pool/release", {"slotId": slot["id"]})
        print(f"hot: reserve returned {slot.get('id')}, not {slot_id} — released, using direct ports", file=sys.stderr)
        return None, found
    return slot["id"], slot

def release(slot_id):
    if not slot_id:
        return
    http("POST", orch.rstrip("/") + "/pool/release", {"slotId": slot_id})

def wd_sessions(base):
    st, body = http("GET", base + "/sessions")
    if st != 200:
        return []
    val = body.get("value", body) if isinstance(body, dict) else []
    if isinstance(val, dict) and "id" in val:
        return [val]
    ids = []
    if isinstance(val, list):
        for item in val:
            if isinstance(item, dict):
                ids.append(item)
            elif isinstance(item, str):
                ids.append({"id": item})
    return ids

def wd_create(base):
    caps = {
        "capabilities": {
            "alwaysMatch": {
                "browserName": "chrome",
                "goog:chromeOptions": {
                    "args": ["--headless=new", "--disable-dev-shm-usage", "--window-size=1740,1080"]
                },
            }
        }
    }
    st, body = http("POST", base + "/session", caps, timeout=45)
    if st != 200:
        raise RuntimeError(f"WD New Session {st}: {body}")
    sid = None
    if isinstance(body, dict):
        val = body.get("value")
        if isinstance(val, dict):
            sid = val.get("sessionId") or val.get("id")
        sid = sid or body.get("sessionId")
    if not sid:
        raise RuntimeError(f"WD New Session no uuid: {body}")
    return sid

def wd_url(base, sid, method="GET", url=None):
    path = f"{base}/session/{sid}/url"
    if method == "POST":
        return http("POST", path, {"url": url}, timeout=60)
    return http("GET", path)

def wd_delete(base, sid):
    http("DELETE", f"{base}/session/{sid}")

def idle_ok(url):
    if not url:
        return True
    s = str(url).lower()
    return "/login" not in s

want_wd = protocol in ("both", "webdriver", "wd")
want_pw = protocol in ("both", "playwright", "pw")

try:
    if want_wd:
        t = now()
        rid, slot = reserve(wd_slot, "webdriver", "chrome")
        state["wd_reserved"] = rid
        if slot and slot.get("webdriverUrl"):
            state["wd_url"] = str(slot["webdriverUrl"]).rstrip("/")
        marks["wd_reserve_ms"] = round((now() - t) * 1000)
        base = state["wd_url"]
        st, status_body = http("GET", base + "/status")
        print(f"WARM_SLOT_ID={wd_slot}")
        print(f"WARM_WD_URL={base}")
        print(f"WD_STATUS={st}")
        existing = wd_sessions(base)
        sid = None
        try:
            if refresh and existing:
                sid = existing[0].get("id") or existing[0].get("sessionId")
            if not sid:
                t = now()
                sid = wd_create(base)
                marks["wd_create_ms"] = round((now() - t) * 1000)
            state["wd_sid"] = sid
            t = now()
            st, _ = wd_url(base, sid, "POST", preopen)
            marks["wd_preopen_ms"] = round((now() - t) * 1000)
            if st >= 400:
                print(f"hot: POST /url {st} (page may still load)", file=sys.stderr)
            _, got = wd_url(base, sid, "GET")
            current = got.get("value") if isinstance(got, dict) else got
            print(f"WARM_SESSION_ID={sid}")
            print(f"PREOPEN_URL={preopen}")
            print(f"CURRENT_URL={current}")
            if not idle_ok(current) and os.environ.get("HOT_KEEP") != "1":
                print("hot: CURRENT_URL still /login before trap", file=sys.stderr)
        except Exception as e:
            marks["wd_create_error"] = f"{type(e).__name__}: {e}"
            print(f"WD_SESSION=driver-ready-chrome-exited ({type(e).__name__})", file=sys.stderr)
            print(f"WD_SESSIONS={existing}")

    if want_pw:
        t = now()
        rid, slot = reserve(pw_slot, "playwright", "chromium")
        state["pw_reserved"] = rid
        if slot and slot.get("playwrightWsUrl"):
            state["pw_ws"] = slot["playwrightWsUrl"]
        marks["pw_reserve_ms"] = round((now() - t) * 1000)
        ws = state["pw_ws"]
        http_probe = ws.replace("ws://", "http://").replace("wss://", "https://").rstrip("/")
        st, _ = http("GET", http_probe + "/")
        marks["pw_probe_ms"] = round((now() - t) * 1000)
        print(f"PW_SLOT_ID={pw_slot}")
        print(f"PW_WS_URL={ws}")
        print(f"PW_HTTP={st}")
        # Page-live keeper is optional (playwright package). Server is already up.
        try:
            from playwright.sync_api import sync_playwright  # type: ignore
            t = now()
            with sync_playwright() as p:
                browser = p.chromium.connect(ws)
                page = browser.contexts[0].pages[0] if browser.contexts and browser.contexts[0].pages else browser.new_page()
                page.goto(preopen, wait_until="domcontentloaded")
                print(f"PW_CURRENT_URL={page.url}")
                browser.close()
            marks["pw_preopen_ms"] = round((now() - t) * 1000)
        except Exception as e:
            print(f"PW_PAGE=server-live (no playwright client: {type(e).__name__})", file=sys.stderr)

    marks["total_ms"] = round((now() - t0) * 1000)
    with open(timing_path, "w") as f:
        json.dump(marks, f, indent=2)
    print(f"HOT_TIMING_FILE={timing_path}")
    print(json.dumps(marks))

    # keep session for the caller; trap in bash deletes
    env_out = os.environ.get("WARM_SESSION_ENV", "")
    if env_out:
        with open(env_out, "w") as f:
            if state["wd_sid"]:
                f.write(f"WARM_SESSION_ID={state['wd_sid']}\n")
                f.write(f"WARM_WD_URL={state['wd_url']}\n")
            f.write(f"PREOPEN_URL={preopen}\n")
            f.write(f"PW_WS_URL={state['pw_ws']}\n")
            if state["wd_reserved"]:
                f.write(f"WARM_SLOT_ID={state['wd_reserved']}\n")

    # write state for bash trap
    state_path = os.environ.get("HOT_STATE_FILE", "/tmp/hot-reuse-state.json")
    with open(state_path, "w") as f:
        json.dump(state, f)
    print(f"HOT_STATE_FILE={state_path}")

    if os.environ.get("HOT_KEEP", "") == "1":
        print("HOT_KEEP=1 — leaving session; caller must trap")
        sys.exit(0)

except Exception as e:
    print(f"hot: {type(e).__name__}: {e}", file=sys.stderr)
    sys.exit(1)
finally:
    if os.environ.get("HOT_KEEP", "") == "1":
        pass
    else:
        # default: trap in this process (proof run)
        try:
            if state.get("wd_sid"):
                wd_delete(state["wd_url"], state["wd_sid"])
            _, sess = http("GET", state["wd_url"] + "/sessions")
            print(f"WD_SESSIONS_AFTER={sess}")
        except Exception as e:
            print(f"hot trap WD: {e}", file=sys.stderr)
        release(state.get("wd_reserved"))
        release(state.get("pw_reserved"))
        _, slots_after = http("GET", orch.rstrip("/") + "/pool/slots")
        if isinstance(slots_after, list):
            for s in slots_after:
                print(f"reservedBy[{s.get('id')}]={s.get('reservedBy')}")
PY
