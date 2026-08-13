#!/usr/bin/env python3
"""Reference webhook receiver for manually watching SCIMage's signed
deliveries. Verifies X-SCIMage-Signature the same way internal/webhook.Verify
does, then prints the event. Stdlib only.

Listens on http://127.0.0.1:9099/scim-events — point SCIM_WEBHOOK_URL there.

Usage:
    SCIM_WEBHOOK_SECRET=<same secret the server is configured with> \
        python3 tests/webhook_receiver.py
"""
import hashlib
import hmac
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

SECRET = os.environ.get("SCIM_WEBHOOK_SECRET")
if not SECRET:
    sys.exit("set SCIM_WEBHOOK_SECRET to the same value configured on the SCIM server")

HOST = "127.0.0.1"
PORT = 9099
PATH = "/scim-events"
TOLERANCE_SECONDS = 300  # 5 minutes, matches the signature's freshness window


def verify(header, delivery_id, event, body):
    parts = dict(p.split("=", 1) for p in header.split(",") if "=" in p)
    stamp, sig = parts.get("t"), parts.get("v1")
    if not stamp or not sig:
        return False, "malformed signature header"

    if abs(time.time() - int(stamp)) > TOLERANCE_SECONDS:
        return False, "signature timestamp outside tolerance"

    # Newline-separated, body last — the same material internal/webhook.Sign
    # computes the HMAC over.
    material = f"{stamp}\n{delivery_id}\n{event}\n".encode() + body
    expected = hmac.new(SECRET.encode(), material, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, sig):
        return False, "signature does not match"
    return True, ""


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != PATH:
            self.send_response(404)
            self.end_headers()
            return

        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        delivery_id = self.headers.get("X-SCIMage-Delivery-Id", "")
        event = self.headers.get("X-SCIMage-Event", "")

        ok, reason = verify(self.headers.get("X-SCIMage-Signature", ""), delivery_id, event, body)
        if not ok:
            print(f"REJECTED delivery {delivery_id}: {reason}", file=sys.stderr, flush=True)
            self.send_response(401)
            self.end_headers()
            return

        try:
            pretty = json.dumps(json.loads(body), indent=2)
        except ValueError:
            pretty = body.decode(errors="replace")
        # flush=True: stdout is block-buffered once it's not a TTY (piped to a
        # file, say), and a delivery you're watching for shouldn't sit in that
        # buffer instead of showing up when it actually arrived.
        print(f"--- delivery {delivery_id}: {event} ---\n{pretty}\n", flush=True)

        self.send_response(200)
        self.end_headers()

    def log_message(self, *args):
        pass  # quiet the default access log; we print our own line per delivery


if __name__ == "__main__":
    print(f"webhook receiver listening on http://{HOST}:{PORT}{PATH}")
    HTTPServer((HOST, PORT), Handler).serve_forever()
