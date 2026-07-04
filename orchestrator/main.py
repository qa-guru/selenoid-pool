from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml
from flask import Flask, jsonify, request


@dataclass
class Slot:
    id: str
    protocol: str
    browser: str
    warm_url: str
    session_id: str
    webdriver_url: str | None = None
    playwright_ws_url: str | None = None
    reserved_by: str | None = None


@dataclass
class Pool:
    slots: list[Slot] = field(default_factory=list)

    def by_id(self, slot_id: str) -> Slot | None:
        return next((slot for slot in self.slots if slot.id == slot_id), None)

    def available(self, protocol: str | None = None, browser: str | None = None) -> list[Slot]:
        result = [slot for slot in self.slots if slot.reserved_by is None]
        if protocol:
            result = [slot for slot in result if slot.protocol == protocol]
        if browser:
            result = [slot for slot in result if slot.browser == browser]
        return result


def load_pool(config_path: Path) -> Pool:
    raw = yaml.safe_load(config_path.read_text(encoding="utf-8")) or {}
    slots: list[Slot] = []
    for item in raw.get("slots", []):
        slots.append(
            Slot(
                id=str(item["id"]),
                protocol=str(item.get("protocol", "webdriver")),
                browser=str(item.get("browser", "chrome")),
                warm_url=str(item["warm_url"]).rstrip("/"),
                session_id=str(item.get("session_id", item["id"])),
                webdriver_url=item.get("webdriver_url"),
                playwright_ws_url=item.get("playwright_ws_url"),
            )
        )
    return Pool(slots=slots)


def http_json(method: str, url: str, payload: dict[str, Any] | None = None, timeout: float = 30.0) -> dict[str, Any]:
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            body = response.read().decode("utf-8")
            return json.loads(body) if body else {}
    except urllib.error.HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {url} -> HTTP {error.code}: {detail}") from error


def slot_payload(slot: Slot) -> dict[str, Any]:
    return {
        "id": slot.id,
        "protocol": slot.protocol,
        "browser": slot.browser,
        "sessionId": slot.session_id,
        "warmUrl": slot.warm_url,
        "webdriverUrl": slot.webdriver_url,
        "playwrightWsUrl": slot.playwright_ws_url,
        "reservedBy": slot.reserved_by,
    }


def create_app(pool: Pool) -> Flask:
    app = Flask(__name__)

    @app.get("/health")
    def health() -> Any:
        return jsonify({"ok": True, "slots": len(pool.slots)})

    @app.get("/pool/slots")
    def list_slots() -> Any:
        return jsonify([slot_payload(slot) for slot in pool.slots])

    @app.post("/pool/reserve")
    def reserve() -> Any:
        body = request.get_json(silent=True) or {}
        protocol = body.get("protocol")
        browser = body.get("browser")
        owner = str(body.get("owner", "anonymous"))

        candidates = pool.available(protocol=protocol, browser=browser)
        if not candidates:
            return jsonify({"error": "no available slots"}), 409

        slot = candidates[0]
        slot.reserved_by = owner
        return jsonify({"ok": True, "slot": slot_payload(slot)})

    @app.post("/pool/release")
    def release() -> Any:
        body = request.get_json(silent=True) or {}
        slot_id = body.get("slotId")
        if not slot_id:
            return jsonify({"error": "slotId is required"}), 400

        slot = pool.by_id(str(slot_id))
        if slot is None:
            return jsonify({"error": "slot not found"}), 404

        try:
            http_json("POST", f"{slot.warm_url}/warm/reset")
        except RuntimeError:
            pass

        slot.reserved_by = None
        return jsonify({"ok": True, "slotId": slot.id})

    @app.post("/pool/preopen")
    def preopen() -> Any:
        body = request.get_json(silent=True) or {}
        slot_id = body.get("slotId")
        url = body.get("url")
        if not slot_id or not url:
            return jsonify({"error": "slotId and url are required"}), 400

        slot = pool.by_id(str(slot_id))
        if slot is None:
            return jsonify({"error": "slot not found"}), 404

        result = http_json("POST", f"{slot.warm_url}/warm/goto", {"url": url})
        return jsonify({"ok": True, "slotId": slot.id, "result": result})

    @app.post("/pool/video/start")
    def video_start() -> Any:
        body = request.get_json(silent=True) or {}
        slot_id = body.get("slotId")
        if not slot_id:
            return jsonify({"error": "slotId is required"}), 400

        slot = pool.by_id(str(slot_id))
        if slot is None:
            return jsonify({"error": "slot not found"}), 404

        session_id = str(body.get("sessionId", slot.session_id))
        result = http_json("POST", f"{slot.warm_url}/warm/video/start", {"sessionId": session_id})
        return jsonify({"ok": True, "slotId": slot.id, "result": result})

    @app.post("/pool/video/stop")
    def video_stop() -> Any:
        body = request.get_json(silent=True) or {}
        slot_id = body.get("slotId")
        if not slot_id:
            return jsonify({"error": "slotId is required"}), 400

        slot = pool.by_id(str(slot_id))
        if slot is None:
            return jsonify({"error": "slot not found"}), 404

        result = http_json("POST", f"{slot.warm_url}/warm/video/stop")
        return jsonify({"ok": True, "slotId": slot.id, "result": result})

    return app


def main() -> None:
    parser = argparse.ArgumentParser(description="Warm pool orchestrator")
    parser.add_argument(
        "--config",
        default=os.environ.get("WARM_POOL_CONFIG", "config.example.yaml"),
        help="Path to pool config YAML",
    )
    parser.add_argument("--host", default=os.environ.get("WARM_POOL_HOST", "0.0.0.0"))
    parser.add_argument("--port", type=int, default=int(os.environ.get("WARM_POOL_PORT", "9090")))
    args = parser.parse_args()

    config_path = Path(args.config)
    if not config_path.exists():
        print(f"Config not found: {config_path}", file=sys.stderr)
        sys.exit(1)

    pool = load_pool(config_path)
    app = create_app(pool)
    app.run(host=args.host, port=args.port, debug=False)


if __name__ == "__main__":
    main()
