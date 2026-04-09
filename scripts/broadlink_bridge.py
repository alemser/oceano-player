#!/usr/bin/env python3
"""JSON-line bridge between the Oceano Go server and the python-broadlink SDK.

Protocol: one JSON object per line on stdin → one JSON object per line on stdout.

Supported commands:
  {"cmd": "pair",     "host": "<ip>"}
  {"cmd": "learn",    "host": "<ip>", "timeout": 30}
  {"cmd": "send_ir",  "host": "<ip>", "code": "<base64>"}

Responses:
  {"ok": true,  "token": "<hex>", "device_id": "<hex>"}   # pair success
  {"ok": true,  "code":  "<base64>"}                       # learn success
  {"ok": true}                                              # send_ir success
  {"ok": false, "error": "<message>"}                      # any failure
"""

import sys
import json
import base64
import time

try:
    import broadlink
except ImportError:
    for line in sys.stdin:
        line = line.strip()
        if line:
            print(json.dumps({"ok": False, "error": "python-broadlink not installed — run: python3 -m pip install broadlink"}), flush=True)
    sys.exit(0)


def _connect(host: str):
    """Return an authenticated device object for *host*."""
    dev = broadlink.hello(host)
    dev.auth()
    return dev


def _extract_key(dev) -> bytes:
    """Extract the session key regardless of python-broadlink version."""
    key = getattr(dev, "key", None)
    if not key:
        try:
            key = dev.aes.algorithm.key
        except AttributeError:
            pass
    return key


def pair(host: str) -> dict:
    """Discover and authenticate; return session key + device id."""
    try:
        dev = _connect(host)
    except Exception as exc:
        return {"ok": False, "error": f"connection failed: {exc}"}

    key_bytes = _extract_key(dev)
    if not key_bytes:
        return {"ok": False, "error": "auth succeeded but could not extract session key"}

    id_val = getattr(dev, "id", None)
    key_hex = key_bytes.hex() if isinstance(key_bytes, (bytes, bytearray)) else str(key_bytes)
    if isinstance(id_val, (bytes, bytearray)):
        id_hex = id_val.hex()
    elif id_val is not None:
        id_hex = format(int(id_val), "08x")
    else:
        id_hex = "00000000"

    return {"ok": True, "token": key_hex, "device_id": id_hex}


def learn(host: str, timeout: int = 30) -> dict:
    """Enter IR learning mode and wait up to *timeout* seconds for a code."""
    try:
        dev = _connect(host)
        dev.enter_learning()
    except Exception as exc:
        return {"ok": False, "error": f"enter learning failed: {exc}"}

    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            data = dev.check_data()
            return {"ok": True, "code": base64.b64encode(data).decode()}
        except Exception:
            time.sleep(0.4)

    return {"ok": False, "error": "timeout — no IR signal received within the time limit"}


def send_ir(host: str, code_b64: str) -> dict:
    """Send a base64-encoded Broadlink IR code."""
    try:
        dev = _connect(host)
        data = base64.b64decode(code_b64)
        dev.send_data(data)
        return {"ok": True}
    except Exception as exc:
        return {"ok": False, "error": str(exc)}


def handle(req: dict) -> dict:
    cmd = req.get("cmd", "")
    if cmd == "pair":
        host = req.get("host", "")
        if not host:
            return {"ok": False, "error": "host is required"}
        return pair(host)
    elif cmd == "learn":
        host = req.get("host", "")
        if not host:
            return {"ok": False, "error": "host is required"}
        return learn(host, timeout=int(req.get("timeout", 30)))
    elif cmd == "send_ir":
        for field in ("host", "code"):
            if not req.get(field):
                return {"ok": False, "error": f"{field} is required"}
        return send_ir(req["host"], req["code"])
    else:
        return {"ok": False, "error": f"unknown command: {cmd!r}"}


if __name__ == "__main__":
    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        try:
            req  = json.loads(raw)
            resp = handle(req)
        except json.JSONDecodeError as exc:
            resp = {"ok": False, "error": f"invalid JSON: {exc}"}
        except Exception as exc:
            resp = {"ok": False, "error": str(exc)}
        print(json.dumps(resp), flush=True)
